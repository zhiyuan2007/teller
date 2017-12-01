package exchange

import (
	"errors"
	"fmt"
	"math/big"
	"testing"

	"time"

	"github.com/stretchr/testify/require"

	"github.com/spaco/teller/src/scanner"
	"github.com/spaco/teller/src/sender"
	"github.com/spaco/teller/src/util/dbutil"
	"github.com/spaco/teller/src/util/testutil"
)

type dummySender struct {
	txid      string
	err       error
	sleepTime time.Duration
	sent      struct {
		Address string
		Value   uint64
	}
	closed         bool
	txidConfirmMap map[string]bool
}

func (send *dummySender) SendAsync(destAddr string, coins uint64) <-chan sender.Response {
	rspC := make(chan sender.Response, 1)

	if send.err != nil {
		rspC <- sender.Response{
			Err: send.err.Error(),
		}
		return rspC
	}

	stC := make(chan sender.SendStatus, 2)
	time.AfterFunc(100*time.Millisecond, func() {
		send.sent.Address = destAddr
		send.sent.Value = coins
		rspC <- sender.Response{
			StatusC: stC,
			Txid:    send.txid,
		}
		stC <- sender.Sent
	})

	time.AfterFunc(send.sleepTime, func() {
		stC <- sender.TxConfirmed
	})

	return rspC
}

func (send *dummySender) IsClosed() bool {
	return send.closed
}

func (send *dummySender) IsTxConfirmed(txid string) bool {
	return send.txidConfirmMap[txid]
}

type dummyScanner struct {
	dvC         chan scanner.DepositNote
	addrs       []string
	notifyC     chan struct{}
	notifyAfter time.Duration
	closed      bool
}

func (scan *dummyScanner) AddScanAddress(addr string) error {
	scan.addrs = append(scan.addrs, addr)
	return nil
}

func (scan *dummyScanner) GetDepositValue() <-chan scanner.DepositNote {
	defer func() {
		go func() {
			// notify after given duration, so that the test code know
			// it's time do checking
			time.Sleep(scan.notifyAfter)
			scan.notifyC <- struct{}{}
		}()
	}()
	return scan.dvC
}

func (scan *dummyScanner) GetScanAddresses() ([]string, error) {
	return []string{}, nil
}

func TestRunExchangeService(t *testing.T) {

	var testCases = []struct {
		name        string
		initDpis    []DepositInfo
		bindBtcAddr string
		bindSkyAddr string
		dpAddr      string
		dpValue     int64
		dpTx        string
		dpN         uint32

		sendSleepTime  time.Duration
		sendReturnTxid string
		sendErr        error

		sendServClosed bool

		dvC           chan scanner.DepositValue
		scanServClose bool
		notifyAfter   time.Duration
		txmap         map[string]bool

		putDVTime    time.Duration
		writeToDBOk  bool
		expectStatus Status

		rate int64
		sent uint64
	}{
		{
			name:           "ok",
			initDpis:       []DepositInfo{},
			bindBtcAddr:    "btcaddr",
			bindSkyAddr:    "skyaddr",
			dpAddr:         "btcaddr",
			dpValue:        200000,
			dpTx:           "dptx",
			dpN:            1,
			sendSleepTime:  time.Second * 1,
			sendReturnTxid: "1111",
			sendErr:        nil,
			dvC:            make(chan scanner.DepositValue, 1),
			notifyAfter:    3 * time.Second,
			txmap:          make(map[string]bool),
			putDVTime:      1 * time.Second,
			writeToDBOk:    true,
			expectStatus:   StatusDone,
			rate:           500,
			sent:           1000000,
		},

		{
			name:           "deposit_addr_not_exist",
			initDpis:       []DepositInfo{},
			bindBtcAddr:    "btcaddr",
			bindSkyAddr:    "skyaddr",
			dpAddr:         "btcaddr1",
			dpValue:        200000,
			dpTx:           "dptx",
			dpN:            1,
			sendSleepTime:  time.Second * 1,
			sendReturnTxid: "1111",
			sendErr:        nil,
			dvC:            make(chan scanner.DepositValue, 1),
			notifyAfter:    3 * time.Second,
			txmap:          make(map[string]bool),
			putDVTime:      1 * time.Second,
			writeToDBOk:    false,
			expectStatus:   StatusWaitDeposit,
			rate:           500,
			sent:           1000000,
		},

		{
			name: "deposit_status_above_waiting_btc_deposit",
			initDpis: []DepositInfo{{
				BtcAddress: "btcaddr",
				SkyAddress: "skyaddr",
				Status:     StatusDone,
				BtcTx:      "dptx:1",
			}},
			bindBtcAddr:    "btcaddr",
			bindSkyAddr:    "skyaddr",
			dpAddr:         "btcaddr",
			dpValue:        200000,
			dpTx:           "dptx",
			dpN:            1,
			sendSleepTime:  time.Second * 1,
			sendReturnTxid: "1111",
			sendErr:        nil,
			dvC:            make(chan scanner.DepositValue, 1),
			notifyAfter:    3 * time.Second,
			txmap:          make(map[string]bool),
			putDVTime:      1 * time.Second,
			writeToDBOk:    true,
			expectStatus:   StatusDone,
			rate:           500,
			sent:           1000000,
		},

		{
			name:           "send_service_closed",
			initDpis:       []DepositInfo{},
			bindBtcAddr:    "btcaddr",
			bindSkyAddr:    "skyaddr",
			dpAddr:         "btcaddr",
			dpValue:        200000,
			dpTx:           "dptx",
			dpN:            1,
			sendSleepTime:  time.Second * 1,
			sendReturnTxid: "1111",
			sendErr:        sender.ErrServiceClosed,
			sendServClosed: true,
			dvC:            make(chan scanner.DepositValue, 1),
			notifyAfter:    3 * time.Second,
			txmap:          make(map[string]bool),
			putDVTime:      1 * time.Second,
			writeToDBOk:    true,
			expectStatus:   StatusWaitSend,
			rate:           500,
			sent:           1000000,
		},

		{
			name:           "send_failed",
			initDpis:       []DepositInfo{},
			bindBtcAddr:    "btcaddr",
			bindSkyAddr:    "skyaddr",
			dpAddr:         "btcaddr",
			dpValue:        200000,
			dpTx:           "dptx",
			dpN:            1,
			sendSleepTime:  time.Second * 3,
			sendReturnTxid: "",
			sendErr:        fmt.Errorf("send skycoin failed"),
			dvC:            make(chan scanner.DepositValue, 1),
			notifyAfter:    3 * time.Second,
			txmap:          make(map[string]bool),
			putDVTime:      1 * time.Second,
			writeToDBOk:    true,
			expectStatus:   StatusWaitSend,
			rate:           500,
			sent:           1000000,
		},

		{
			name:           "scan_service_closed",
			initDpis:       []DepositInfo{},
			bindBtcAddr:    "btcaddr",
			bindSkyAddr:    "skyaddr",
			dpAddr:         "btcaddr",
			dpValue:        200000,
			dpTx:           "dptx",
			dpN:            1,
			sendSleepTime:  time.Second * 3,
			sendReturnTxid: "",
			sendErr:        fmt.Errorf("send skycoin failed"),
			dvC:            make(chan scanner.DepositValue, 1),
			notifyAfter:    3 * time.Second,
			txmap:          make(map[string]bool),
			scanServClose:  true,
			putDVTime:      1 * time.Second,
			writeToDBOk:    true,
			expectStatus:   StatusWaitSend,
			rate:           500,
			sent:           1000000,
		},

		{
			name: "has_unconfirmed_tx",
			initDpis: []DepositInfo{{
				BtcAddress: "btcaddr",
				SkyAddress: "skyaddr",
				Txid:       "t1",
				Status:     StatusWaitConfirm,
				BtcTx:      "dptx:1",
			}},
			bindBtcAddr:    "btcaddr",
			bindSkyAddr:    "skyaddr",
			dpAddr:         "btcaddr",
			dpValue:        200000,
			dpTx:           "dptx",
			dpN:            1,
			sendSleepTime:  time.Second * 3,
			sendReturnTxid: "",
			sendErr:        fmt.Errorf("send skycoin failed"),
			dvC:            make(chan scanner.DepositValue, 1),
			notifyAfter:    3 * time.Second,
			txmap:          map[string]bool{"t1": true},
			scanServClose:  true,
			putDVTime:      1 * time.Second,
			writeToDBOk:    true,
			expectStatus:   StatusWaitSend,
			rate:           500,
			sent:           1000000,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			db, shutdown := testutil.PrepareDB(t)
			defer shutdown()

			send := &dummySender{
				sleepTime:      tc.sendSleepTime,
				txid:           tc.sendReturnTxid,
				err:            tc.sendErr,
				closed:         tc.sendServClosed,
				txidConfirmMap: tc.txmap,
			}

			dvC := make(chan scanner.DepositNote)
			scan := &dummyScanner{
				dvC:         dvC,
				notifyC:     make(chan struct{}, 1),
				notifyAfter: tc.notifyAfter,
				closed:      tc.scanServClose,
			}
			var service *Service

			require.NotPanics(t, func() {
				service = NewService(testutil.NewLogger(t), db, scan, send, Config{
					Rate: tc.rate,
				})

				// init the deposit infos
				for _, dpi := range tc.initDpis {
					err := service.store.AddDepositInfo(dpi)
					require.NoError(t, err)
				}
			})

			go service.Run()

			excli := NewClient(service)
			if len(tc.initDpis) == 0 {
				err := excli.BindAddress(tc.bindBtcAddr, tc.bindSkyAddr)
				require.NoError(t, err)
			}

			// fake deposit value
			time.AfterFunc(tc.putDVTime, func() {
				if scan.closed {
					close(dvC)
					return
				}
				dvC <- scanner.DepositNote{
					DepositValue: scanner.DepositValue{
						Address: tc.dpAddr,
						Value:   tc.dpValue,
						Tx:      tc.dpTx,
						N:       tc.dpN,
					},
					AckC: make(chan struct{}, 1),
				}
			})

			<-scan.notifyC

			if scan.closed {
				return
			}

			// check the info
			dpTxN := fmt.Sprintf("%s:%d", tc.dpTx, tc.dpN)
			dpi, err := service.store.GetDepositInfo(dpTxN)

			if tc.writeToDBOk {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.IsType(t, dbutil.ObjectNotExistErr{}, err)
			}

			if tc.writeToDBOk {
				require.Equal(t, tc.expectStatus, dpi.Status)

				if len(tc.initDpis) == 0 && tc.sendErr == nil {
					require.Equal(t, tc.bindSkyAddr, send.sent.Address)
					require.Equal(t, tc.sent, send.sent.Value)
				}
			}

			service.Shutdown()
		})
	}
}

func TestCalculateSkyValue(t *testing.T) {
	cases := []struct {
		satoshis int64
		rate     int64
		result   uint64
		err      error
	}{
		{
			satoshis: -1,
			rate:     1,
			err:      errors.New("negative satoshis or negative skyPerBTC"),
		},

		{
			satoshis: 1,
			rate:     -1,
			err:      errors.New("negative satoshis or negative skyPerBTC"),
		},

		{
			satoshis: 1e8,
			rate:     1,
			result:   1e6,
		},

		{
			satoshis: 1e8,
			rate:     500,
			result:   500e6,
		},

		{
			satoshis: 100e8,
			rate:     500,
			result:   50000e6,
		},

		{
			satoshis: 2e5, // 0.002 BTC
			rate:     500, // 500 SKY/BTC = 1 SKY / 0.002 BTC
			result:   1e6, // 1 SKY
		},
		{
			satoshis: 1e5, // 0.002 BTC
			rate:     500, // 500 SKY/BTC = 1 SKY / 0.002 BTC
			result:   0,   // 1 SKY
		},
		{
			satoshis: 12345678, // 0.12345678 BTC
			rate:     500,      // 500 SKY/BTC
			result:   61000000, // 61 SKY
		},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("satoshis=%d rate=%d", tc.satoshis, tc.rate)
		t.Run(name, func(t *testing.T) {
			result, err := calculateSkyValue(tc.satoshis, tc.rate)
			if tc.err == nil {
				require.NoError(t, err)
				require.Equal(t, tc.result, result, "%d != %d", tc.result, result)
			} else {
				require.Error(t, err)
				require.Equal(t, tc.err, err)
				require.Equal(t, uint64(0), result, "%d != 0", result)
			}
		})
	}
}
func TestCalculateEthSkyValue(t *testing.T) {
	cases := []struct {
		wei    *big.Int
		rate   int64
		result uint64
		err    error
	}{
		{
			wei:  big.NewInt(-1),
			rate: 1,
			err:  errors.New("wei must be greater than or equal to 0"),
		},

		{
			wei:    big.NewInt(0),
			rate:   1,
			result: 0,
		},

		{
			wei:    big.NewInt(1e18),
			rate:   1,
			result: 1e6,
		},

		{
			wei:    big.NewInt(1e18),
			rate:   500,
			result: 500e6,
		},

		{
			wei:    big.NewInt(1).Mul(big.NewInt(100), big.NewInt(1e18)),
			rate:   500,
			result: 50000e6,
		},

		{
			wei:    big.NewInt(2e15), // 0.002 ETH
			rate:   500,              // 500 SKY/ETH = 1 SKY / 0.002 ETH
			result: 1e6,              // 1 SKY
		},

		{
			wei:    big.NewInt(1e17), // 0.1 ETH
			rate:   5,
			result: 0, // 0.5 SKY
		},

		{
			wei:    big.NewInt(11345e13), // 0.11345 ETH
			rate:   100,
			result: 11000000, // 11 SKY
		},
	}
	testEth := big.NewInt(1).Mul(big.NewInt(1e18), big.NewInt(1e3)) //1e21 1000ETH
	bigNum1 := big.NewInt(1).Div(testEth, big.NewInt(1e8))          //1e13
	midDepositValue := bigNum1.Int64()                              //1e13
	require.Equal(t, midDepositValue, big.NewInt(1e13).Int64())
	originEth := big.NewInt(1).Mul(big.NewInt(midDepositValue), big.NewInt(1e8)) //1e21
	require.Equal(t, 0, originEth.Cmp(testEth))
	droplets := int64(2240200000)
	amt := big.NewInt(1).Div(big.NewInt(droplets), big.NewInt(1000000)).Int64() * int64(1000000)
	require.Equal(t, amt, int64(2240000000))

	for _, tc := range cases {
		name := fmt.Sprintf("wei=%d rate=%s", tc.wei, tc.rate)
		t.Run(name, func(t *testing.T) {
			result, err := calculateSkyValueForEth(tc.wei, tc.rate)
			if tc.err == nil {
				require.NoError(t, err)
				require.Equal(t, tc.result, result, "%d != %d", tc.result, result)
			} else {
				require.Error(t, err)
				require.Equal(t, tc.err, err)
				require.Equal(t, uint64(0), result, "%d != 0", result)
			}
		})
	}
}
