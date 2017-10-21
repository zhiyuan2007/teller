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

	"context"
	"github.com/boltdb/bolt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/sirupsen/logrus"
	"math/big"
	"strconv"
	"time"
)

var (
	scoinType = "ethcoin"
)

// ETHScanner blockchain scanner to check if there're deposit coins
type ETHScanner struct {
	log       logrus.FieldLogger
	cfg       Config
	ethClient *rpc.Client
	store     *store
	depositC  chan DepositNote // deposit value channel
	quit      chan struct{}
}

// NewETHScanner creates scanner instance
func NewETHScanner(log logrus.FieldLogger, db *bolt.DB, client *rpc.Client, cfg Config) (*ETHScanner, error) {
	s, err := newStore(db, scoinType)
	if err != nil {
		return nil, err
	}

	if cfg.ScanPeriod == 0 {
		cfg.ScanPeriod = checkHeadDepositValuePeriod
	}

	return &ETHScanner{
		ethClient: client,
		log:       log.WithField("prefix", "scanner.btc"),
		cfg:       cfg,
		store:     s,
		depositC:  make(chan DepositNote),
		quit:      make(chan struct{}),
	}, nil
}

// Run starts the scanner
func (s *ETHScanner) Run() error {
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

func (s *ETHScanner) scanBlock(block *visor.ReadableBlock) error {
	return s.store.db.Update(func(tx *bolt.Tx) error {
		addrs, err := s.store.getScanAddressesTx(tx, scoinType)
		if err != nil {
			return err
		}
		fmt.Printf("scan skycoin addrss %+v\n", addrs)

		dvs, err := scanETHBlock(s, block, addrs)
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

// scanETHBlock scan the given block and returns the next block hash or error
func scanETHBlock(s *ETHScanner, block *visor.ReadableBlock, depositAddrs []string) ([]DepositValue, error) {
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
					Value:   int64(amt),
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
func (s *ETHScanner) AddScanAddress(addr string) error {
	return s.store.addScanAddress(addr, scoinType)
}

// GetBestBlock returns the hash and height of the block in the longest (best)
// chain.
func (s *ETHScanner) getBestBlock() (*visor.ReadableBlock, error) {
	rb, err := s.skyClient.GetLastBlocks()
	if err != nil {
		return nil, err
	}

	return rb, err
}

// getBlock returns block of given hash
func (s *ETHScanner) getBlock(seq uint64) (*visor.ReadableBlock, error) {
	rb, err := s.skyClient.GetBlocksBySeq(seq)
	if err != nil {
		return nil, err
	}

	return rb, err
}

func (s *ETHScanner) getRawTransactionVerbose(hash string) (*webrpc.TxnResult, error) {
	return s.skyClient.GetTransaction(hash)
}

// getNextBlock returns the next block of given hash, return nil if next block does not exist
func (s *ETHScanner) getNextBlock(seq uint64) (*visor.ReadableBlock, error) {
	return s.getBlock(seq + 1)
}

// setLastScanBlock sets the last scan block hash and height
func (s *ETHScanner) setLastScanBlock(hash *chainhash.Hash, height int64) error {
	return s.store.setLastScanBlock(LastScanBlock{
		Hash:   hash.String(),
		Height: height,
	}, scoinType)
}

// GetScanAddresses returns the deposit addresses that need to scan
func (s *ETHScanner) GetScanAddresses() ([]string, error) {
	return s.store.getScanAddresses(scoinType)
}

// GetDepositValue returns deposit value channel
func (s *ETHScanner) GetDepositValue() <-chan DepositNote {
	return s.depositC
}

// Shutdown shutdown the scanner
func (s *ETHScanner) Shutdown() {
	s.log.Info("Closing ETH scanner")
	close(s.quit)
	s.skyClient.Shutdown()
}
