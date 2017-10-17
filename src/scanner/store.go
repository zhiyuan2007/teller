package scanner

import (
	"errors"
	"fmt"

	"github.com/boltdb/bolt"
	"github.com/skycoin/teller/src/util/dbutil"
)

var (
	scanMetaBkt     = []byte("scan_meta")
	depositValueBkt = []byte("deposit_value")

	lastScanBlockKey    = "last_scan_block"
	depositAddressesKey = "deposit_addresses"
	dvIndexListKey      = "dv_index_list" // deposit value index list

)

// DepositValuesEmptyErr is returned if there are no deposit values
type DepositValuesEmptyErr struct{}

func (e DepositValuesEmptyErr) Error() string {
	return "No deposit values available"
}

// DepositValueExistsErr is returned when a deposit value already exists
type DepositValueExistsErr struct{}

func (e DepositValueExistsErr) Error() string {
	return "Deposit value already exists"
}

// DuplicateDepositAddressErr is returned if a certain deposit address already
// exists when adding it to a bucket
type DuplicateDepositAddressErr struct {
	Address string
}

func (e DuplicateDepositAddressErr) Error() string {
	return fmt.Sprintf("Deposit address \"%s\" already exists", e.Address)
}

// NewDuplicateDepositAddressErr return a DuplicateDepositAddressErr
func NewDuplicateDepositAddressErr(addr string) error {
	return DuplicateDepositAddressErr{
		Address: addr,
	}
}

// store records scanner meta info
type store struct {
	db *bolt.DB
}

func newStore(db *bolt.DB, ct string) (*store, error) {
	if db == nil {
		return nil, errors.New("new store failed: db is nil")
	}

	if err := db.Update(func(tx *bolt.Tx) error {
		// create LastScanBlock bucket if not exist
		if _, err := tx.CreateBucketIfNotExists(dbutil.ByteJoin(scanMetaBkt, ct, "_")); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(depositValueBkt)
		return err

	}); err != nil {
		return nil, err
	}
	return &store{
		db: db,
	}, nil
}

// LastScanBlock stores the last scanned block's hash and height
type LastScanBlock struct {
	Hash   string
	Height int64
}

// getLastScanBlock returns the last scanned block hash and height
func (s *store) getLastScanBlock(ct string) (LastScanBlock, error) {
	var lsb LastScanBlock

	if err := s.db.View(func(tx *bolt.Tx) error {
		scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
		return dbutil.GetBucketObject(tx, scanMetaFullName, lastScanBlockKey, &lsb)
	}); err != nil {
		switch err.(type) {
		case dbutil.ObjectNotExistErr:
			err = nil
		default:
			return LastScanBlock{}, err
		}
	}

	return lsb, nil
}

func (s *store) setLastScanBlock(lsb LastScanBlock, ct string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
		return dbutil.PutBucketValue(tx, scanMetaFullName, lastScanBlockKey, lsb)
	})
}

func (s *store) setLastScanBlockTx(tx *bolt.Tx, lsb LastScanBlock, ct string) error {
	scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
	return dbutil.PutBucketValue(tx, scanMetaFullName, lastScanBlockKey, lsb)
}

func (s *store) getScanAddressesTx(tx *bolt.Tx, ct string) ([]string, error) {
	var addrs []string

	scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
	if err := dbutil.GetBucketObject(tx, scanMetaFullName, depositAddressesKey, &addrs); err != nil {
		switch err.(type) {
		case dbutil.ObjectNotExistErr:
			err = nil
		default:
			return nil, err
		}
	}

	if len(addrs) == 0 {
		addrs = nil
	}

	return addrs, nil
}

func (s *store) getScanAddresses(ct string) ([]string, error) {
	var addrs []string

	if err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		addrs, err = s.getScanAddressesTx(tx, ct)
		return err
	}); err != nil {
		return nil, err
	}

	return addrs, nil
}

func (s *store) addScanAddress(addr string, ct string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		addrs, err := s.getScanAddressesTx(tx, ct)
		if err != nil {
			return err
		}

		for _, a := range addrs {
			if a == addr {
				return NewDuplicateDepositAddressErr(addr)
			}
		}

		addrs = append(addrs, addr)

		scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
		return dbutil.PutBucketValue(tx, scanMetaFullName, depositAddressesKey, addrs)
	})
}

func (s *store) removeScanAddr(addr string, ct string) error {
	// FIXME: This will be very slow with large number of scan addresses.
	// FIXME: Save scan addresses differently

	return s.db.Update(func(tx *bolt.Tx) error {
		addrs, err := s.getScanAddressesTx(tx, ct)
		if err != nil {
			return err
		}

		idx := -1

		for i, a := range addrs {
			if a == addr {
				idx = i
				break
			}
		}

		if idx == -1 {
			return nil
		}

		addrs = append(addrs[:idx], addrs[idx+1:]...)
		scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
		return dbutil.PutBucketValue(tx, scanMetaFullName, depositAddressesKey, addrs)
	})
}

func (s *store) getHeadDepositValue(ct string) (DepositValue, error) {
	var dv DepositValue

	if err := s.db.View(func(tx *bolt.Tx) error {
		index, err := s.getDepositValueIndexTx(tx, ct)
		if err != nil {
			return err
		}

		if len(index) == 0 {
			return DepositValuesEmptyErr{}
		}

		head := index[0]

		return dbutil.GetBucketObject(tx, depositValueBkt, head, &dv)
	}); err != nil {
		return DepositValue{}, err
	}

	return dv, nil
}

func (s *store) pushDepositValueTx(tx *bolt.Tx, dv DepositValue, ct string) error {
	key := dv.TxN()

	// Check if the deposit value already exists
	if hasKey, err := dbutil.BucketHasKey(tx, depositValueBkt, key); err != nil {
		return err
	} else if hasKey {
		return DepositValueExistsErr{}
	}

	// Save deposit value
	if err := dbutil.PutBucketValue(tx, depositValueBkt, key, dv); err != nil {
		return err
	}

	// Update deposit value index
	index, err := s.getDepositValueIndexTx(tx, ct)
	if err != nil {
		return err
	}

	index = append(index, key)

	scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
	return dbutil.PutBucketValue(tx, scanMetaFullName, dvIndexListKey, index)
}

func (s *store) popDepositValue(ct string) (DepositValue, error) {
	var dv DepositValue

	if err := s.db.Update(func(tx *bolt.Tx) error {
		index, err := s.getDepositValueIndexTx(tx, ct)
		if err != nil {
			return err
		}

		if len(index) == 0 {
			return DepositValuesEmptyErr{}
		}

		head := index[0]
		index = index[1:]

		// write index back to db
		scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
		if err := dbutil.PutBucketValue(tx, scanMetaFullName, dvIndexListKey, index); err != nil {
			return err
		}

		// mark deposit value in bucket as used
		if err := dbutil.GetBucketObject(tx, depositValueBkt, head, &dv); err != nil {
			return err
		}

		dv.IsUsed = true

		return dbutil.PutBucketValue(tx, depositValueBkt, head, dv)
	}); err != nil {
		return DepositValue{}, err
	}

	return dv, nil
}

// Returns the deposit value index from the db.
// If there is no deposit value index in the db, nil is returned instead of
// dbutil.ObjectNotExistErr
func (s *store) getDepositValueIndexTx(tx *bolt.Tx, ct string) ([]string, error) {
	var index []string
	scanMetaFullName := dbutil.ByteJoin(scanMetaBkt, ct, "_")
	if err := dbutil.GetBucketObject(tx, scanMetaFullName, dvIndexListKey, &index); err != nil {
		switch err.(type) {
		case dbutil.ObjectNotExistErr:
			err = nil
		default:
			return nil, err
		}
	}

	if len(index) == 0 {
		index = nil
	}

	return index, nil
}

func (s *store) getDepositValueIndex(ct string) ([]string, error) {
	var index []string

	if err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		index, err = s.getDepositValueIndexTx(tx, ct)
		return err
	}); err != nil {
		return nil, err
	}

	return index, nil
}
