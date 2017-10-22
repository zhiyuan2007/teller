package addrs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/boltdb/bolt"
	"github.com/sirupsen/logrus"
	//"github.com/skycoin/skycoin/src/cipher"
)

const ethBucketKey = "used_eth_address"

// NewEthAddrs returns an Addrs loaded with BTC addresses
func NewEthAddrs(log logrus.FieldLogger, db *bolt.DB, addrsReader io.Reader) (*Addrs, error) {
	loader, err := loadEthAddresses(addrsReader)
	if err != nil {
		return nil, err
	}
	return NewAddrs(log, db, loader, ethBucketKey)
}

func loadEthAddresses(addrsReader io.Reader) ([]string, error) {
	var addrs struct {
		Addresses []string `json:"eth_addresses"`
	}

	if err := json.NewDecoder(addrsReader).Decode(&addrs); err != nil {
		return nil, fmt.Errorf("Decode loaded address json failed: %v", err)
	}

	if err := verifyETHAddresses(addrs.Addresses); err != nil {
		return nil, err
	}

	return addrs.Addresses, nil
}

func verifyETHAddresses(addrs []string) error {
	if len(addrs) == 0 {
		return errors.New("No ETH addresses")
	}

	addrMap := make(map[string]struct{}, len(addrs))

	for _, addr := range addrs {
		if _, ok := addrMap[addr]; ok {
			return fmt.Errorf("Duplicate deposit address `%s`", addr)
		}

		//if _, err := cipher.BitcoinDecodeBase58Address(addr); err != nil {
		//return fmt.Errorf("Invalid deposit address `%s`: %v", addr, err)
		//}

		addrMap[addr] = struct{}{}
	}

	return nil
}
