package exchange

import (
	"testing"

	"github.com/boltdb/bolt"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/teller/src/scanner"
	"github.com/skycoin/teller/src/util/dbutil"
	"github.com/skycoin/teller/src/util/testutil"
)

type MockStore struct {
	mock.Mock
}

func (m *MockStore) GetBindAddress(btcAddr string) (string, error) {
	args := m.Called(btcAddr)
	return args.String(0), args.Error(1)
}

func (m *MockStore) BindAddress(skyAddr, btcAddr string) error {
	args := m.Called(skyAddr, btcAddr)
	return args.Error(0)
}

func (m *MockStore) GetOrCreateDepositInfo(dv scanner.Deposit, rate string) (DepositInfo, error) {
	args := m.Called(dv, rate)
	return args.Get(0).(DepositInfo), args.Error(1)
}

func (m *MockStore) GetDepositInfoArray(filt DepositFilter) ([]DepositInfo, error) {
	args := m.Called(filt)

	dis := args.Get(0)
	if dis == nil {
		return nil, args.Error(1)
	}

	return dis.([]DepositInfo), args.Error(1)
}

func (m *MockStore) GetDepositInfoOfSkyAddress(skyAddr string) ([]DepositInfo, error) {
	args := m.Called(skyAddr)

	dis := args.Get(0)
	if dis == nil {
		return nil, args.Error(1)
	}

	return dis.([]DepositInfo), args.Error(1)
}

func (m *MockStore) UpdateDepositInfo(btcTx string, f func(DepositInfo) DepositInfo) (DepositInfo, error) {
	args := m.Called(btcTx, f)
	return args.Get(0).(DepositInfo), args.Error(1)
}

func (m *MockStore) UpdateDepositInfoCallback(btcTx string, f func(DepositInfo) DepositInfo, callback func(DepositInfo) error) (DepositInfo, error) {
	args := m.Called(btcTx, f, callback)
	return args.Get(0).(DepositInfo), args.Error(1)
}

func (m *MockStore) GetSkyBindBtcAddresses(skyAddr string) ([]string, error) {
	args := m.Called(skyAddr)

	btcAddrs := args.Get(0)
	if btcAddrs == nil {
		return nil, args.Error(1)
	}

	return btcAddrs.([]string), args.Error(1)
}

func (m *MockStore) GetDepositStats() (int64, int64, error) {
	args := m.Called()
	return args.Get(0).(int64), args.Get(1).(int64), args.Error(2)
}

func newTestStore(t *testing.T) (*Store, func()) {
	db, shutdown := testutil.PrepareDB(t)

	log, _ := testutil.NewLogger(t)
	s, err := NewStore(log, db)
	require.NoError(t, err)

	return s, shutdown
}

func TestStoreNewStore(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	// check the buckets
	err := s.db.View(func(tx *bolt.Tx) error {
		require.NotNil(t, tx.Bucket(exchangeMetaBkt))
		require.NotNil(t, tx.Bucket(depositInfoBkt))
		require.NotNil(t, tx.Bucket(bindAddressBkt))
		require.NotNil(t, tx.Bucket(skyDepositSeqsIndexBkt))
		require.NotNil(t, tx.Bucket(btcTxsBkt))
		return nil
	})
	require.NoError(t, err)
}

func TestStoreAddDepositInfo(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	di, err := s.addDepositInfo(DepositInfo{
		DepositID:      "btx1:2",
		SkyAddress:     "skyaddr1",
		DepositAddress: "btcaddr1",
		DepositValue:   1e6,
		ConversionRate: testSkyBtcRate,
		Status:         StatusWaitSend,
	})
	require.NoError(t, err)
	require.Equal(t, di.Seq, uint64(1))
	require.NotEmpty(t, di.UpdatedAt)

	// check in db
	err = s.db.View(func(tx *bolt.Tx) error {
		var dpi DepositInfo
		err := dbutil.GetBucketObject(tx, depositInfoBkt, "btx1:2", &dpi)
		require.NoError(t, err)
		if err != nil {
			return err
		}

		require.NotEmpty(t, dpi.UpdatedAt)

		var txns []string
		err = dbutil.GetBucketObject(tx, btcTxsBkt, "btcaddr1", &txns)
		require.NoError(t, err)
		if err != nil {
			return err
		}

		require.Equal(t, "btx1:2", txns[0])

		return nil
	})
	require.NoError(t, err)

	_, err = s.addDepositInfo(DepositInfo{
		DepositID:      "btx2:2",
		SkyAddress:     "skyaddr1",
		DepositAddress: "btcaddr2",
		DepositValue:   1e6,
		ConversionRate: testSkyBtcRate,
		Status:         StatusWaitSend,
	})
	require.NoError(t, err)

	err = s.db.View(func(tx *bolt.Tx) error {
		var dpi DepositInfo
		err := dbutil.GetBucketObject(tx, depositInfoBkt, "btx1:2", &dpi)
		require.NoError(t, err)
		if err != nil {
			return err
		}

		require.NotEmpty(t, dpi.UpdatedAt)

		return nil
	})
	require.NoError(t, err)

	// check invalid deposit info
	_, err = s.addDepositInfo(DepositInfo{})
	require.Error(t, err)
}

func TestStoreBindAddress(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	err := s.BindAddress("sa1", "ba1")
	require.NoError(t, err)

	// check bucket
	err = s.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(bindAddressBkt)
		require.NotNil(t, bkt)
		v := bkt.Get([]byte("ba1"))
		require.Equal(t, "sa1", string(v))

		var addrs []string
		err := dbutil.GetBucketObject(tx, skyDepositSeqsIndexBkt, "sa1", &addrs)
		require.NoError(t, err)
		require.Equal(t, "ba1", addrs[0])
		return nil
	})
	require.NoError(t, err)

	// A sky address can have multiple addresses bound to it
	err = s.BindAddress("sa1", "ba2")
	require.NoError(t, err)
}

func TestStoreBindAddressTwiceFails(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	err := s.BindAddress("a", "b")
	require.NoError(t, err)

	err = s.BindAddress("a", "b")
	require.Error(t, err)
	require.Equal(t, ErrAddressAlreadyBound, err)

	err = s.BindAddress("c", "b")
	require.Error(t, err)
	require.Equal(t, ErrAddressAlreadyBound, err)
}

func TestStoreGetBindAddress(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	// init the bind address bucket
	err := s.BindAddress("skyaddr1", "btcaddr1")
	require.NoError(t, err)
	err = s.BindAddress("skyaddr2", "btcaddr2")
	require.NoError(t, err)
	err = s.BindAddress("skyaddr2", "btcaddr3")
	require.NoError(t, err)

	var testCases = []struct {
		name          string
		btcAddr       string
		expectSkyAddr string
		ok            bool
		err           error
	}{
		{
			"get btcaddr1",
			"btcaddr1",
			"skyaddr1",
			true,
			nil,
		},
		{
			"get btcaddr2",
			"btcaddr2",
			"skyaddr2",
			true,
			nil,
		},
		{
			"get btcaddr3",
			"btcaddr3",
			"skyaddr2",
			true,
			nil,
		},
		{
			"get addr not exist",
			"btcaddr4",
			"",
			false,
			nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			addr, err := s.GetBindAddress(tc.btcAddr)
			require.NoError(t, err)
			if tc.ok {
				require.Equal(t, tc.expectSkyAddr, addr)
			} else {
				require.Empty(t, addr)
			}
		})
	}
}

func TestStoreGetDepositInfo(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	_, err := s.addDepositInfo(DepositInfo{
		DepositID:      "btx1:1",
		DepositAddress: "btcaddr1",
		SkyAddress:     "skyaddr1",
		DepositValue:   1e6,
		Txid:           "txid-1",
		ConversionRate: testSkyBtcRate,
		SkySent:        100e8,
		Status:         StatusDone,
	})
	require.NoError(t, err)

	dpi, err := s.getDepositInfo("btx1:1")
	require.NoError(t, err)
	require.Equal(t, "btcaddr1", dpi.DepositAddress)
	require.Equal(t, "skyaddr1", dpi.SkyAddress)
	require.Equal(t, StatusDone, dpi.Status)
	require.NotEmpty(t, dpi.UpdatedAt)
}

func TestStoreUpdateDepositInfo(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	_, err := s.addDepositInfo(DepositInfo{
		DepositID:      "btx1:1",
		SkyAddress:     "skyaddr1",
		DepositAddress: "btcaddr1",
		DepositValue:   1e6,
		ConversionRate: testSkyBtcRate,
		Status:         StatusWaitSend,
	})
	require.NoError(t, err)

	_, err = s.addDepositInfo(DepositInfo{
		DepositID:      "btx2:1",
		SkyAddress:     "skyaddr1",
		DepositAddress: "btcaddr2",
		DepositValue:   1e6,
		ConversionRate: testSkyBtcRate,
		Status:         StatusWaitSend,
	})
	require.NoError(t, err)

	err = s.db.View(func(tx *bolt.Tx) error {
		var dpi1 DepositInfo
		err := dbutil.GetBucketObject(tx, depositInfoBkt, "btx1:1", &dpi1)
		require.NoError(t, err)
		if err != nil {
			return err
		}

		require.Equal(t, dpi1.Status, StatusWaitSend)

		var dpi2 DepositInfo
		err = dbutil.GetBucketObject(tx, depositInfoBkt, "btx2:1", &dpi2)
		require.NoError(t, err)
		if err != nil {
			return err
		}

		require.Equal(t, dpi2.Status, StatusWaitSend)

		return nil
	})

	require.NoError(t, err)

	dpi, err := s.UpdateDepositInfo("btx1:1", func(dpi DepositInfo) DepositInfo {
		dpi.Status = StatusWaitConfirm
		dpi.Txid = "121212"
		return dpi
	})

	require.NoError(t, err)
	require.Equal(t, dpi.Txid, "121212")
	require.Equal(t, dpi.Status, StatusWaitConfirm)

	err = s.db.View(func(tx *bolt.Tx) error {
		var dpi1 DepositInfo
		err := dbutil.GetBucketObject(tx, depositInfoBkt, "btx1:1", &dpi1)
		require.NoError(t, err)
		if err != nil {
			return err
		}

		// check updated value
		require.Equal(t, dpi1.Status, StatusWaitConfirm)
		require.Equal(t, "121212", dpi1.Txid)

		return nil
	})

	require.NoError(t, err)

	// TODO: test no exist deposit info
}

func TestStoreGetDepositInfoOfSkyAddress(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	err := s.BindAddress("skyaddr1", "btcaddr1")
	require.NoError(t, err)

	dpis, err := s.GetDepositInfoOfSkyAddress("skyaddr1")
	require.NoError(t, err)
	require.Len(t, dpis, 1)
	require.Equal(t, dpis[0].DepositAddress, "btcaddr1")

	err = s.BindAddress("skyaddr1", "btcaddr2")
	require.NoError(t, err)

	dpis, err = s.GetDepositInfoOfSkyAddress("skyaddr1")
	require.NoError(t, err)
	require.Len(t, dpis, 2)
	require.Equal(t, dpis[0].DepositAddress, "btcaddr1")
	require.Equal(t, dpis[1].DepositAddress, "btcaddr2")

	// Multiple txns saved
	di3 := DepositInfo{
		SkyAddress:     "skyaddr3",
		DepositAddress: "btcaddr3",
		DepositID:      "btctx:3",
		DepositValue:   100e8,
		ConversionRate: testSkyBtcRate,
		Status:         StatusWaitSend,
	}
	di3, err = s.addDepositInfo(di3)
	require.Equal(t, di3.Seq, uint64(1))
	require.NoError(t, err)

	err = s.BindAddress("skyaddr3", "btcaddr3")
	require.NoError(t, err)
	err = s.BindAddress("skyaddr3", "btcaddr4")
	require.NoError(t, err)

	di4 := DepositInfo{
		SkyAddress:     "skyaddr3",
		DepositAddress: "btcaddr4",
		DepositID:      "btctx:4",
		DepositValue:   1000e8,
		ConversionRate: testSkyBtcRate,
		Status:         StatusWaitSend,
	}
	di4, err = s.addDepositInfo(di4)
	require.NoError(t, err)

	dpis, err = s.GetDepositInfoOfSkyAddress("skyaddr3")
	require.NoError(t, err)
	t.Logf("%v", dpis)
	require.Len(t, dpis, 2)

	// Sequences are renumbered in the result, starting from 0
	di3.Seq = 0
	di4.Seq = 1

	require.Equal(t, di3, dpis[0])
	require.Equal(t, di4, dpis[1])
}

func TestStoreGetDepositInfoArray(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	dpis := []DepositInfo{
		{
			DepositID:      "t1:1",
			DepositAddress: "b1",
			SkyAddress:     "s1",
			DepositValue:   1e6,
			ConversionRate: testSkyBtcRate,
			Status:         StatusWaitSend,
		},
		{
			DepositID:      "t2:1",
			DepositAddress: "b2",
			SkyAddress:     "s2",
			DepositValue:   1e6,
			Txid:           "txid-2",
			ConversionRate: testSkyBtcRate,
			SkySent:        100e8,
			Status:         StatusWaitConfirm,
		},
	}

	for _, dpi := range dpis {
		_, err := s.addDepositInfo(dpi)
		require.NoError(t, err)
	}

	ds, err := s.GetDepositInfoArray(func(dpi DepositInfo) bool {
		return dpi.Status == StatusWaitSend
	})

	require.NoError(t, err)

	require.Len(t, ds, 1)
	require.Equal(t, dpis[0].Status, ds[0].Status)
	require.Equal(t, dpis[0].DepositAddress, ds[0].DepositAddress)
	require.Equal(t, dpis[0].SkyAddress, ds[0].SkyAddress)

	ds1, err := s.GetDepositInfoArray(func(dpi DepositInfo) bool {
		return dpi.Status == StatusWaitConfirm
	})

	require.NoError(t, err)

	require.Len(t, ds1, 1)
	require.Equal(t, dpis[1].Status, ds1[0].Status)
	require.Equal(t, dpis[1].DepositAddress, ds1[0].DepositAddress)
	require.Equal(t, dpis[1].SkyAddress, ds1[0].SkyAddress)
}

func TestStoreIsValidBtcTx(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
		btctx string
	}{
		{
			"empty string",
			false,
			"",
		},
		{
			"colon only",
			false,
			":",
		},
		{
			"multiple colons",
			false,
			"txid:2:2",
		},
		{
			"no txid",
			false,
			":2",
		},
		{
			"no n",
			false,
			"txid:",
		},
		{
			"n not int",
			false,
			"txid:b",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.valid, isValidBtcTx(tc.btctx))
		})
	}
}

func TestStoreGetOrCreateDepositInfoAlreadyExists(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	di := DepositInfo{
		Status:         StatusWaitSend,
		DepositAddress: "foo-btc-addr",
		DepositID:      "foo-tx:1",
		SkyAddress:     "foo-sky-addr",
		DepositValue:   1e6,
		ConversionRate: testSkyBtcRate,
		Deposit: scanner.Deposit{
			Address: "foo-btc-addr",
			Value:   1e6,
			Height:  20,
			Tx:      "foo-tx",
			N:       1,
		},
	}

	_, err := s.addDepositInfo(di)
	require.NoError(t, err)

	// Check the saved deposit info
	foundDi, err := s.getDepositInfo(di.DepositID)
	require.NoError(t, err)
	// Seq and UpdatedAt should be set by addDepositInfo
	require.Equal(t, uint64(1), foundDi.Seq)
	require.NotEmpty(t, foundDi.UpdatedAt)

	// Other fields should be unchanged
	di.Seq = foundDi.Seq
	di.UpdatedAt = foundDi.UpdatedAt
	require.Equal(t, di, foundDi)

	// GetOrCreateDepositInfo, deposit info exists
	dv := scanner.Deposit{
		Address: di.Deposit.Address + "-2",
		Value:   di.Deposit.Value * 2,
		Height:  di.Deposit.Height + 1,
		Tx:      di.Deposit.Tx,
		N:       di.Deposit.N,
	}
	require.Equal(t, di.Deposit.ID(), dv.ID())

	differentRate := "112233"
	require.NotEqual(t, differentRate, di.ConversionRate)
	existsDi, err := s.GetOrCreateDepositInfo(dv, differentRate)

	// di.Deposit won't be changed
	require.Equal(t, di, existsDi)
}

func TestStoreGetOrCreateDepositInfoNoBoundSkyAddr(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	dv := scanner.Deposit{
		Address: "foo-btc-addr",
	}

	rate := "100"
	_, err := s.GetOrCreateDepositInfo(dv, rate)
	require.Error(t, err)
	require.Equal(t, err, ErrNoBoundAddress)
}

func TestStoreGetSkyBindBtcAddresses(t *testing.T) {
	s, shutdown := newTestStore(t)
	defer shutdown()

	skyAddr := "skyAddr"
	addrs, err := s.GetSkyBindBtcAddresses(skyAddr)
	require.NoError(t, err)
	require.Nil(t, addrs)

	btcAddr1 := "btcaddr1"
	err = s.BindAddress(skyAddr, btcAddr1)
	require.NoError(t, err)

	addrs, err = s.GetSkyBindBtcAddresses(skyAddr)
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	require.Equal(t, addrs[0], btcAddr1)

	btcAddr2 := "btcaddr2"
	err = s.BindAddress(skyAddr, btcAddr2)
	require.NoError(t, err)

	addrs, err = s.GetSkyBindBtcAddresses(skyAddr)
	require.NoError(t, err)
	require.Len(t, addrs, 2)
	require.Equal(t, addrs[0], btcAddr1)
	require.Equal(t, addrs[1], btcAddr2)
}
