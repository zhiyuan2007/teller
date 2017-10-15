// Package scanner scans bitcoin blockchain and check all transactions
// to see if there are addresses in vout that can match our deposit addresses.
// If found, then generate an event and push to deposit event channel
//
// current scanner doesn't support reconnect after btcd shutdown, if
// any error occur when call btcd apis, the scan service will be closed.
package scanner

import (
	"fmt"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcutil"
	"github.com/sirupsen/logrus"
)

// SKYScanner blockchain scanner to check if there're deposit coins
type SKYScanner struct {
	log       logrus.FieldLogger
	cfg       Config
	btcClient BtcRPCClient
	store     *store
	depositC  chan DepositNote // deposit value channel
	quit      chan struct{}
}

// NewSKYScanner creates scanner instance
func NewSKYScanner(log logrus.FieldLogger, db *bolt.DB, btc BtcRPCClient, cfg Config) (*SKYScanner, error) {
	s, err := newStore(db)
	if err != nil {
		return nil, err
	}

	if cfg.ScanPeriod == 0 {
		cfg.ScanPeriod = checkHeadDepositValuePeriod
	}

	return &SKYScanner{
		btcClient: btc,
		log:       log.WithField("prefix", "scanner.btc"),
		cfg:       cfg,
		store:     s,
		depositC:  make(chan DepositNote),
		quit:      make(chan struct{}),
	}, nil
}

// Run starts the scanner
func (s *SKYScanner) Run() error {
	log := s.log
	log.Info("Start bitcoin blockchain scan service")
	defer log.Info("Bitcoin blockchain scan service closed")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			headDv, err := s.store.getHeadDepositValue()
			if err != nil {
				switch err.(type) {
				case DepositValuesEmptyErr:
					select {
					case <-time.After(checkHeadDepositValuePeriod):
						continue
					case <-s.quit:
						return
					}
				default:
					log.WithError(err).Error("getHeadDepositValue failed")
					return
				}
			}

			dn := makeDepositNote(headDv)
			select {
			case <-s.quit:
				return
			case s.depositC <- dn:
				select {
				case <-dn.AckC:
					// pop the head deposit value in store
					if ddv, err := s.store.popDepositValue(); err != nil {
						log.WithError(err).Error("popDepositValue failed")
					} else {
						log.WithField("depositValue", ddv).Info("DepositValue is processed")
					}
				case <-s.quit:
					return
				}
			}
		}
	}()

	// get last scan block
	lsb, err := s.getLastScanBlock()
	if err != nil {
		return fmt.Errorf("get last scan block failed: %v", err)
	}

	height := lsb.Height
	hash := lsb.Hash

	log = log.WithFields(logrus.Fields{
		"blockHeight": height,
		"blockHash":   hash,
	})

	if height == 0 {
		// the first time the bot start
		// get the best block
		block, err := s.getBestBlock()
		if err != nil {
			return err
		}

		if err := s.scanBlock(block); err != nil {
			return fmt.Errorf("Scan block %s failed: %v", block.Hash, err)
		}

		hash = block.Hash
		log = log.WithField("blockHash", hash)
	}

	wg.Add(1)
	errC := make(chan error, 1)
	go func() {
		defer wg.Done()

		for {
			nextBlock, err := s.getNextBlock(hash)
			if err != nil {
				log.WithError(err).Error("getNextBlock failed")
				select {
				case <-s.quit:
					return
				default:
					errC <- err
					return
				}
			}

			if nextBlock == nil {
				log.Debug("No new block to s...")
				select {
				case <-s.quit:
					return
				case <-time.After(time.Duration(s.cfg.ScanPeriod) * time.Second):
					continue
				}
			}

			hash = nextBlock.Hash
			height = nextBlock.Height
			log = log.WithFields(logrus.Fields{
				"blockHeight": height,
				"blockHash":   hash,
			})

			log.Debug("Scanned new block")
			if err := s.scanBlock(nextBlock); err != nil {
				select {
				case <-s.quit:
					return
				default:
					errC <- fmt.Errorf("Scan block %s failed: %v", nextBlock.Hash, err)
					return
				}
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case err := <-errC:
		return err
	}
}

func (s *SKYScanner) scanBlock(block *btcjson.GetBlockVerboseResult) error {
	return s.store.db.Update(func(tx *bolt.Tx) error {
		addrs, err := s.store.getScanAddressesTx(tx)
		if err != nil {
			return err
		}

		dvs, err := scanSKYBlock(s, block, addrs)
		if err != nil {
			return err
		}

		for _, dv := range dvs {
			if err := s.store.pushDepositValueTx(tx, dv); err != nil {
				switch err.(type) {
				case DepositValueExistsErr:
					continue
				default:
					s.log.WithError(err).WithField("depositValue", dv).Error("pushDepositValueTx failed")
				}
			}
		}

		hash, err := chainhash.NewHashFromStr(block.Hash)
		if err != nil {
			return err
		}

		return s.store.setLastScanBlockTx(tx, LastScanBlock{
			Hash:   hash.String(),
			Height: block.Height,
		})
	})
}

// scanSKYBlock scan the given block and returns the next block hash or error
func scanSKYBlock(s *SKYScanner, block *btcjson.GetBlockVerboseResult, depositAddrs []string) ([]DepositValue, error) {
	addrMap := map[string]struct{}{}
	for _, a := range depositAddrs {
		addrMap[a] = struct{}{}
	}

	var dv []DepositValue
	for _, txidstr := range block.Tx {
		txid, err := chainhash.NewHashFromStr(txidstr)
		if err != nil {
			fmt.Printf("new hash from str failed id: %s\n", txidstr)
			continue
		}
		tx, err := s.getRawTransactionVerbose(txid)
		if err != nil {
			fmt.Printf("get getRawTransactionVerbose failed: %s\n", txidstr)
			continue
		}
		for _, v := range tx.Vout {
			amt, err := btcutil.NewAmount(v.Value)
			if err != nil {
				return nil, err
			}

			for _, a := range v.ScriptPubKey.Addresses {
				if _, ok := addrMap[a]; ok {
					dv = append(dv, DepositValue{
						Address: a,
						Value:   int64(amt),
						Height:  block.Height,
						Tx:      tx.Txid,
						N:       v.N,
					})
				}
			}
		}
	}

	return dv, nil
}

// AddScanAddress adds new scan address
func (s *SKYScanner) AddScanAddress(addr string) error {
	return s.store.addScanAddress(addr)
}

// GetBestBlock returns the hash and height of the block in the longest (best)
// chain.
func (s *SKYScanner) getBestBlock() (*btcjson.GetBlockVerboseResult, error) {
	hash, err := s.btcClient.GetBestBlockHash()
	if err != nil {
		return nil, err
	}

	return s.getBlock(hash)
}

// getBlock returns block of given hash
func (s *SKYScanner) getBlock(hash *chainhash.Hash) (*btcjson.GetBlockVerboseResult, error) {
	return s.btcClient.GetBlockVerbose(hash)
}

func (s *SKYScanner) getRawTransactionVerbose(hash *chainhash.Hash) (*btcjson.TxRawResult, error) {
	return s.btcClient.GetRawTransactionVerbose(hash)
}

// getNextBlock returns the next block of given hash, return nil if next block does not exist
func (s *SKYScanner) getNextBlock(hash string) (*btcjson.GetBlockVerboseResult, error) {
	h, err := chainhash.NewHashFromStr(hash)
	if err != nil {
		return nil, err
	}

	b, err := s.getBlock(h)
	if err != nil {
		return nil, err
	}

	if b.NextHash == "" {
		return nil, nil
	}

	nxtHash, err := chainhash.NewHashFromStr(b.NextHash)
	if err != nil {
		return nil, err
	}

	return s.getBlock(nxtHash)
}

// setLastScanBlock sets the last scan block hash and height
func (s *SKYScanner) setLastScanBlock(hash *chainhash.Hash, height int64) error {
	return s.store.setLastScanBlock(LastScanBlock{
		Hash:   hash.String(),
		Height: height,
	})
}

// getLastScanBlock returns the last scanned block hash and height
func (s *SKYScanner) getLastScanBlock() (LastScanBlock, error) {
	return s.store.getLastScanBlock()
}

// GetScanAddresses returns the deposit addresses that need to scan
func (s *SKYScanner) GetScanAddresses() ([]string, error) {
	return s.store.getScanAddresses()
}

// GetDepositValue returns deposit value channel
func (s *SKYScanner) GetDepositValue() <-chan DepositNote {
	return s.depositC
}

// Shutdown shutdown the scanner
func (s *SKYScanner) Shutdown() {
	s.log.Info("Closing SKY scanner")
	close(s.quit)
	s.btcClient.Shutdown()
}
