// Package scanner scans bitcoin blockchain and check all transactions
// to see if there are addresses in vout that can match our deposit addresses.
// If found, then generate an event and push to deposit event channel
//
// current scanner doesn't support reconnect after btcd shutdown, if
// any error occur when call btcd apis, the scan service will be closed.
package scanner

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"context"

	"github.com/boltdb/bolt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/sirupsen/logrus"
	"github.com/spaco/teller/src/util/dbutil"
)

var (
	ecoinType = "ethcoin"
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
func NewETHScanner(log logrus.FieldLogger, db *bolt.DB, ethurl string, cfg Config) (*ETHScanner, error) {
	s, err := newStore(db, ecoinType)
	if err != nil {
		return nil, err
	}

	if cfg.ScanPeriod == 0 {
		cfg.ScanPeriod = checkHeadDepositValuePeriod
	}

	client, err := rpc.Dial(ethurl)
	if err != nil {
		return nil, err
	}

	return &ETHScanner{
		ethClient: client,
		log:       log.WithField("prefix", "scanner.eth"),
		cfg:       cfg,
		store:     s,
		depositC:  make(chan DepositNote),
		quit:      make(chan struct{}),
	}, nil
}

// Run starts the scanner
func (s *ETHScanner) Run() error {
	log := s.log
	log.Info("Start ethcoin blockchain scan service")
	defer log.Info("Ethcoin blockchain scan service closed")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			headDv, err := s.store.getHeadDepositValue(ecoinType)
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
					if ddv, err := s.store.popDepositValue(ecoinType); err != nil {
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
	lsb, err := s.store.getLastScanBlock(ecoinType)
	if err != nil {
		return fmt.Errorf("get last scan block failed: %v", err)
	}

	height := lsb.Height
	hash := lsb.Hash

	log = log.WithFields(logrus.Fields{
		"blockHeight": height,
		"blockHash":   hash,
	})

	currentheight := height

	if s.cfg.InitialScanHeight == 0 {
		currentheight = s.cfg.InitialScanHeight
	}

	if currentheight == 0 {
		bestHeight, err := s.getBlockCount()
		if err != nil {
			log.WithError(err).Error("btcClient.GetBlockCount failed")
		} else {
			currentheight = bestHeight - 1
		}
	}

	wg.Add(1)
	errC := make(chan error, 1)
	go func() {
		defer wg.Done()

		for {
			bestHeight, err := s.getBlockCount()
			if err != nil {
				log.WithError(err).Error("btcClient.GetBlockCount failed")
				select {
				case <-s.quit:
					return
				case <-time.After(time.Duration(s.cfg.ScanPeriod) * time.Second):
					continue
				}
			}

			// If not enough confirmations exist for this block, wait
			if currentheight+s.cfg.ConfirmationsRequired > bestHeight {
				log.Debug("Not enough confirmations, waiting")
				select {
				case <-s.quit:
					return
				case <-time.After(time.Duration(s.cfg.ScanPeriod) * time.Second):
					continue
				}
			}
			currBlock, err := s.getBlock(uint64(currentheight))
			if err != nil {
				log.WithError(err).Error("getBlock failed")
				select {
				case <-s.quit:
					return
				default:
					errC <- err
					return
				}
			}

			if currBlock == nil {
				log.Debug("No new block to s...")
				select {
				case <-s.quit:
					return
				case <-time.After(time.Duration(s.cfg.ScanPeriod) * time.Second):
					continue
				}
			}

			hash = currBlock.HashNoNonce().String()
			height = int64(currBlock.NumberU64())
			log = log.WithFields(logrus.Fields{
				"blockHeight": height,
				"blockHash":   hash,
			})

			log.Debugf("Scanned new block %u\n", height)
			if err := s.scanBlock(currBlock); err != nil {
				select {
				case <-s.quit:
					return
				default:
					errC <- fmt.Errorf("Scan block %s failed: %v", currBlock.HashNoNonce().String(), err)
					return
				}
			}
			fmt.Printf("current height: %d, confirm %d, block count %d\n", currentheight, s.cfg.ConfirmationsRequired, bestHeight)
			currentheight = height + 1
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

func (s *ETHScanner) scanBlock(block *types.Block) error {
	return s.store.db.Update(func(tx *bolt.Tx) error {
		addrs, err := s.store.getScanAddressesTx(tx, ecoinType)
		if err != nil {
			return err
		}
		dvs, err := scanETHBlock(s, block, addrs)
		if err != nil {
			return err
		}

		for _, dv := range dvs {
			if err := s.store.pushDepositValueTx(tx, dv, ecoinType); err != nil {
				switch err.(type) {
				case DepositValueExistsErr:
					continue
				default:
					s.log.WithError(err).WithField("depositValue", dv).Error("pushDepositValueTx failed")
				}
			}
		}

		return s.store.setLastScanBlockTx(tx, LastScanBlock{
			Hash:   block.HashNoNonce().String(),
			Height: int64(block.NumberU64()),
		}, ecoinType)
	})
}

// scanETHBlock scan the given block and returns the next block hash or error
func scanETHBlock(s *ETHScanner, block *types.Block, depositAddrs []string) ([]DepositValue, error) {
	addrMap := map[string]struct{}{}
	for _, a := range depositAddrs {
		addrMap[a] = struct{}{}
	}

	var dv []DepositValue
	for i, txid := range block.Transactions() {

		tx, err := s.GetTransaction(txid.Hash())
		if err != nil {
			fmt.Printf("------wrong get transcation %s\n-----", txid.Hash().String())
			continue
		}
		to := tx.To()
		if to == nil {
			//-fmt.Printf("this is a contract transcation +%v\n", tx)
			continue
		}
		//1 eth = 1e18 wei, so tx.Value() is very big that may overflow(int64) ,so store it by Gwei and recover it when used
		amt := dbutil.Wei2Gwei(tx.Value())
		a := strings.ToLower(to.String())
		if _, ok := addrMap[a]; ok {
			dv = append(dv, DepositValue{
				Address: a,
				Value:   amt,
				Height:  int64(block.NumberU64()),
				Tx:      tx.Hash().String(),
				N:       uint32(i),
			})
		}
	}

	return dv, nil
}

// AddScanAddress adds new scan address
func (s *ETHScanner) AddScanAddress(addr string) error {
	return s.store.addScanAddress(addr, ecoinType)
}

// GetBestBlock returns the hash and height of the block in the longest (best)
// chain.
func (s *ETHScanner) getBlockCount() (int64, error) {
	var bn string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.ethClient.CallContext(ctx, &bn, "eth_blockNumber"); err != nil {
		return 0, err
	}
	bnRealStr := bn[2:]
	blockNum, err := strconv.ParseInt(bnRealStr, 16, 32)
	if err != nil {
		return 0, err
	}
	return int64(blockNum), nil
}

// GetBestBlock returns the hash and height of the block in the longest (best)
// chain.
func (s *ETHScanner) getBestBlock() (*types.Block, error) {
	var bn string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.ethClient.CallContext(ctx, &bn, "eth_blockNumber"); err != nil {
		return nil, err
	}
	bnRealStr := bn[2:]
	blockNum, err := strconv.ParseInt(bnRealStr, 16, 32)
	if err != nil {
		return nil, err
	}
	rb, err := s.getBlock(uint64(blockNum))
	if err != nil {
		return nil, err
	}

	return rb, err
}

// getBlock returns block of given hash
func (s *ETHScanner) getBlock(seq uint64) (*types.Block, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	block, err := ethclient.NewClient(s.ethClient).BlockByNumber(ctx, big.NewInt(int64(seq)))
	if err != nil && err.Error() != "not found" {
		return nil, err
	}

	return block, nil
}

// getNextBlock returns the next block of given hash, return nil if next block does not exist
func (s *ETHScanner) getNextBlock(seq uint64) (*types.Block, error) {
	return s.getBlock(seq + 1)
}

func (s *ETHScanner) GetTransaction(txhash common.Hash) (*types.Transaction, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	//txhash := common.StringToHash(txid)
	tx, _, err := ethclient.NewClient(s.ethClient).TransactionByHash(ctx, txhash)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// setLastScanBlock sets the last scan block hash and height
func (s *ETHScanner) setLastScanBlock(hash *chainhash.Hash, height int64) error {
	return s.store.setLastScanBlock(LastScanBlock{
		Hash:   hash.String(),
		Height: height,
	}, ecoinType)
}

// GetScanAddresses returns the deposit addresses that need to scan
func (s *ETHScanner) GetScanAddresses() ([]string, error) {
	return s.store.getScanAddresses(ecoinType)
}

// GetDepositValue returns deposit value channel
func (s *ETHScanner) GetDepositValue() <-chan DepositNote {
	return s.depositC
}

// Shutdown shutdown the scanner
func (s *ETHScanner) Shutdown() {
	s.log.Info("Closing ETH scanner")
	close(s.quit)
	s.ethClient.Close()
}
