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

	"strconv"

	"github.com/boltdb/bolt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/api/webrpc"
	"github.com/skycoin/skycoin/src/visor"
	"github.com/spaco/teller/src/sender"
)

var (
	scoinType = "skycoin"
)

// SKYScanner blockchain scanner to check if there're deposit coins
type SKYScanner struct {
	log       logrus.FieldLogger
	cfg       Config
	skyClient *sender.RPC
	store     *store
	depositC  chan DepositNote // deposit value channel
	quit      chan struct{}
}

// NewSKYScanner creates scanner instance
func NewSKYScanner(log logrus.FieldLogger, db *bolt.DB, rpc *sender.RPC, cfg Config) (*SKYScanner, error) {
	s, err := newStore(db, scoinType)
	if err != nil {
		return nil, err
	}

	if cfg.ScanPeriod == 0 {
		cfg.ScanPeriod = checkHeadDepositValuePeriod
	}

	return &SKYScanner{
		skyClient: rpc,
		log:       log.WithField("prefix", "scanner.sky"),
		cfg:       cfg,
		store:     s,
		depositC:  make(chan DepositNote),
		quit:      make(chan struct{}),
	}, nil
}

// Run starts the scanner
func (s *SKYScanner) Run() error {
	log := s.log
	log.Info("Start skycoin blockchain scan service")
	defer log.Info("Skycoin blockchain scan service closed")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			headDv, err := s.store.getHeadDepositValue(scoinType)
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
					if ddv, err := s.store.popDepositValue(scoinType); err != nil {
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
	lsb, err := s.store.getLastScanBlock(scoinType)
	if err != nil {
		return fmt.Errorf("get last scan block failed: %v", err)
	}

	height := lsb.Height
	hash := lsb.Hash

	log = log.WithFields(logrus.Fields{
		"blockHeight": height,
		"blockHash":   hash,
	})

	seq := height
	if height == 0 {
		// the first time the bot start
		// get the best block
		block, err := s.getBestBlock()
		if err != nil {
			return err
		}

		if err := s.scanBlock(block); err != nil {
			return fmt.Errorf("Scan block %s failed: %v", block.Head.BlockHash, err)
		}

		hash = block.Head.BlockHash
		seq = int64(block.Head.BkSeq)
		log = log.WithField("blockHash", hash)
	}

	wg.Add(1)
	errC := make(chan error, 1)
	go func() {
		defer wg.Done()

		for {
			//blockcount, err := s.skyClient.GetBlockCount()
			//if err != nil {
			//log.WithError(err).Error("getNextBlock failed")
			//select {
			//case <-s.quit:
			//return
			//default:
			//errC <- err
			//return
			//}
			//}
			//if uint64(seq+1) > blockcount {
			//select {
			//case <-s.quit:
			//return
			//case <-time.After(time.Duration(s.cfg.ScanPeriod) * time.Second):
			//continue
			//}
			//}
			nextBlock, err := s.getNextBlock(uint64(seq))
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

			hash = nextBlock.Head.BlockHash
			height = int64(nextBlock.Head.BkSeq)
			seq = height
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
					errC <- fmt.Errorf("Scan block %s failed: %v", nextBlock.Head.BlockHash, err)
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

func (s *SKYScanner) scanBlock(block *visor.ReadableBlock) error {
	return s.store.db.Update(func(tx *bolt.Tx) error {
		addrs, err := s.store.getScanAddressesTx(tx, scoinType)
		if err != nil {
			return err
		}
		fmt.Printf("scan skycoin addrss %+v\n", addrs)

		dvs, err := scanSKYBlock(s, block, addrs)
		if err != nil {
			return err
		}

		for _, dv := range dvs {
			if err := s.store.pushDepositValueTx(tx, dv, scoinType); err != nil {
				switch err.(type) {
				case DepositValueExistsErr:
					continue
				default:
					s.log.WithError(err).WithField("depositValue", dv).Error("pushDepositValueTx failed")
				}
			}
		}

		return s.store.setLastScanBlockTx(tx, LastScanBlock{
			Hash:   block.Head.BlockHash,
			Height: int64(block.Head.BkSeq),
		}, scoinType)
	})
}

// scanSKYBlock scan the given block and returns the next block hash or error
func scanSKYBlock(s *SKYScanner, block *visor.ReadableBlock, depositAddrs []string) ([]DepositValue, error) {
	addrMap := map[string]struct{}{}
	for _, a := range depositAddrs {
		addrMap[a] = struct{}{}
	}

	var dv []DepositValue
	for _, tx := range block.Body.Transactions {
		for i, v := range tx.Out {
			amt, ee := strconv.Atoi(v.Coins)
			if ee != nil {
				fmt.Printf("------wrong coin value %s\n-----", v.Coins)
				continue
			}

			a := v.Address
			if _, ok := addrMap[a]; ok {
				dv = append(dv, DepositValue{
					Address: a,
					Value:   int64(amt * 1e6),
					Height:  int64(block.Head.BkSeq),
					Tx:      tx.Hash,
					N:       uint32(i),
				})
			}
		}
	}

	return dv, nil
}

// AddScanAddress adds new scan address
func (s *SKYScanner) AddScanAddress(addr string) error {
	return s.store.addScanAddress(addr, scoinType)
}

// GetBestBlock returns the hash and height of the block in the longest (best)
// chain.
func (s *SKYScanner) getBestBlock() (*visor.ReadableBlock, error) {
	rb, err := s.skyClient.GetLastBlocks()
	if err != nil {
		return nil, err
	}

	return rb, err
}

// getBlock returns block of given hash
func (s *SKYScanner) getBlock(seq uint64) (*visor.ReadableBlock, error) {
	rb, err := s.skyClient.GetBlocksBySeq(seq)
	if err != nil {
		return nil, err
	}

	return rb, err
}

func (s *SKYScanner) getRawTransactionVerbose(hash string) (*webrpc.TxnResult, error) {
	return s.skyClient.GetTransaction(hash)
}

// getNextBlock returns the next block of given hash, return nil if next block does not exist
func (s *SKYScanner) getNextBlock(seq uint64) (*visor.ReadableBlock, error) {
	return s.getBlock(seq + 1)
}

// setLastScanBlock sets the last scan block hash and height
func (s *SKYScanner) setLastScanBlock(hash *chainhash.Hash, height int64) error {
	return s.store.setLastScanBlock(LastScanBlock{
		Hash:   hash.String(),
		Height: height,
	}, scoinType)
}

// GetScanAddresses returns the deposit addresses that need to scan
func (s *SKYScanner) GetScanAddresses() ([]string, error) {
	return s.store.getScanAddresses(scoinType)
}

// GetDepositValue returns deposit value channel
func (s *SKYScanner) GetDepositValue() <-chan DepositNote {
	return s.depositC
}

// Shutdown shutdown the scanner
func (s *SKYScanner) Shutdown() {
	s.log.Info("Closing SKY scanner")
	close(s.quit)
	s.skyClient.Shutdown()
}
