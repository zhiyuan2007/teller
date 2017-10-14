package main

import (
	"flag"
	"log"
	"os"
	"os/user"

	"encoding/json"
	"fmt"
	"strconv"

	"path/filepath"

	"bytes"
	"io/ioutil"

	"math"

	"github.com/boltdb/bolt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcrpcclient"
	"github.com/skycoin/skycoin/src/cipher"
)

// lastScanBlock struct in bucket
type lastScanBlock struct {
	Hash   string
	Height int64
}

// btc address json struct
type addressJSON struct {
	BtcAddresses []string `json:"btc_addresses"`
}

var (
	scanMetaBkt      = []byte("scan_meta")
	lastScanBlockKey = []byte("last_scan_block")
)

var usage = fmt.Sprintf(`%s is a teller helper tool:
Usage:
    %s command [arguments]

The commands are:

    setlastscanblock    set the last scan block height and hash in teller's data.db
    getlastscanblock    get the last scan block in teller
    addbtcaddress       add the bitcoin address to the deposit address pool
    getbtcaddress       list all bitcoin deposit address in the pool
    newbtcaddress       generate bitcoin address
    scanblock           scan block from specific height to get all vout with interger value
`, filepath.Base(os.Args[0]), filepath.Base(os.Args[0]))

func main() {
	u, _ := user.Current()
	dbFile := flag.String("db", filepath.Join(u.HomeDir, ".skycoin-teller/data.db"), "db file path")
	btcAddrFile := flag.String("btcfile", "../teller/btc_addresses.json", "btc addresses json file")
	useJson := flag.Bool("json", false, "Print newbtcaddress output as json")

	flag.Parse()

	args := flag.Args()

	if len(args) == 0 {
		fmt.Println(usage)
		return
	}

	cmd := args[0]

	var db *bolt.DB
	var err error
	switch cmd {
	case "setlastscanblock", "getlastscanblock", "scanblock":
		if _, err := os.Stat(*dbFile); os.IsNotExist(err) {
			fmt.Println(*dbFile, "does not exist")
			return
		}

		db, err = bolt.Open(*dbFile, 0700, nil)
		if err != nil {
			log.Printf("Open db failed: %v\n", err)
			return
		}
		defer db.Close()
	default:
	}

	switch cmd {
	case "help", "h":
		if len(args) == 1 {
			fmt.Println(usage)
			return
		}

		switch args[1] {
		case "setlastscanblock":
			fmt.Println("usage: setlastscanblock block_height block_hash")
		case "getlastscanblock":
			fmt.Println("usage: getlastscanblock")
		case "addbtcaddress":
			fmt.Println("usage: addbtcaddress btc_address")
		case "getbtcaddress":
			fmt.Println("usage: getbtcaddress")
		case "newbtcaddress":
			fmt.Println("usage: [-json] newbtcaddress seed num. -json will print as json.")
		case "scanblock":
			fmt.Println("usage: server user pass cert_path height")
		case "newkeys":
			fmt.Println("usage: newkeys")
		}
		return
	case "newkeys":
		pub, sec := cipher.GenerateKeyPair()
		var keypair = struct {
			Pubkey string
			Seckey string
		}{
			pub.Hex(),
			sec.Hex(),
		}
		v, err := json.MarshalIndent(keypair, "", "    ")
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(string(v))
	case "setlastscanblock":
		height, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			fmt.Println("Invalid argument for set_lastscan_block command")
			return
		}

		hash := args[2]
		lb := lastScanBlock{
			Height: height,
			Hash:   hash,
		}

		db.Update(func(tx *bolt.Tx) error {
			bkt := tx.Bucket(scanMetaBkt)
			if bkt == nil {
				fmt.Println("scan meta bucket is empty")
				return nil
			}
			v, err := json.Marshal(lb)
			if err != nil {
				fmt.Println("Marshal lastscanblock failed:", err)
				return nil
			}

			return bkt.Put(lastScanBlockKey, v)
		})
	case "getlastscanblock":
		db.View(func(tx *bolt.Tx) error {
			v := tx.Bucket(scanMetaBkt).Get(lastScanBlockKey)
			var lsb lastScanBlock
			if err := json.Unmarshal(v, &lsb); err != nil {
				fmt.Println("Unmarshal failed:", err)
				return nil
			}

			v, err := json.MarshalIndent(lsb, "", "    ")
			if err != nil {
				fmt.Println("MarshalIndent failed:", err)
				return nil
			}

			fmt.Println(string(v))
			return nil
		})
	case "addbtcaddress":
		v, err := ioutil.ReadFile(*btcAddrFile)
		if err != nil {
			fmt.Println("Read btcAddr json file failed:", err)
			return
		}

		var addrJSON addressJSON
		if err := json.NewDecoder(bytes.NewReader(v)).Decode(&addrJSON); err != nil {
			fmt.Println("Decode btcAddr json file failed:", err)
			return
		}

		addrJSON.BtcAddresses = append(addrJSON.BtcAddresses, args[1])
		v, err = json.MarshalIndent(addrJSON, "", "    ")
		if err != nil {
			fmt.Println("MarshalIndent btc addresses failed:", err)
			return
		}
		ioutil.WriteFile(*btcAddrFile, v, 0700)
	case "getbtcaddress":
		v, err := ioutil.ReadFile(*btcAddrFile)
		if err != nil {
			fmt.Println("Read btcAddr json file failed:", err)
			return
		}
		fmt.Println(string(v))
	case "newbtcaddress":
		count := args[2]
		var n int
		var err error
		if count != "" {
			n, err = strconv.Atoi(count)
			if err != nil {
				fmt.Println("Invalid argument: ", err)
				return
			}
		} else {
			n = 1
		}

		seckeys := cipher.GenerateDeterministicKeyPairs([]byte(args[1]), n)

		var addrs []string
		for _, sec := range seckeys {
			addr := cipher.BitcoinAddressFromPubkey(cipher.PubKeyFromSecKey(sec))
			addrs = append(addrs, addr)
		}

		if *useJson {
			s := addressJSON{
				BtcAddresses: addrs,
			}

			b, err := json.MarshalIndent(&s, "", "    ")
			if err != nil {
				log.Fatal(b)
				return
			}

			fmt.Println(string(b))

		} else {
			for _, addr := range addrs {
				fmt.Println(addr)
			}
		}

		return
	case "scanblock":
		if len(args) != 5 {
			fmt.Println("Invalid arguments")
			fmt.Println(usage)
			return
		}
		rpcserv := args[1]
		rpcuser := args[2]
		rpcpass := args[3]
		//rpccert := args[4]
		//v, err := ioutil.ReadFile(rpccert)
		//if err != nil {
		//fmt.Println("Read cert file failed:", err)
		//return
		//}

		fmt.Printf("host:%s,user:%s,pass:%s\n", rpcserv, rpcuser, rpcpass)
		btcrpcli, err := btcrpcclient.New(&btcrpcclient.ConnConfig{
			Host: rpcserv,
			//Endpoint:   "ws",
			User:         rpcuser,
			Pass:         rpcpass,
			DisableTLS:   true,
			HTTPPostMode: true,
		}, nil)
		if err != nil {
			fmt.Println("Connect btcd failed:", err)
			return
		}

		h := args[4]
		height, err := strconv.ParseInt(h, 10, 64)
		if err != nil {
			fmt.Println("Invalid block height")
			return
		}

		hash, err := btcrpcli.GetBlockHash(height)
		if err != nil {
			fmt.Println("Get block hash failed:", err)
			return
		}
		fmt.Println("block hash:", hash.String())

		for {
			bv, err := btcrpcli.GetBlockVerboseTx(hash)
			if err != nil {
				fmt.Printf("Get block of %s failed: %v\n", hash, err)
				return
			}

			for _, tx := range bv.RawTx {
				for _, o := range tx.Vout {
					if math.Trunc(o.Value) == o.Value {
						fmt.Printf("Height: %v Address: %v Value: %v\n",
							bv.Height, o.ScriptPubKey.Addresses, o.Value)
					}
				}
			}

			if bv.NextHash == "" {
				return
			}

			hash, err = chainhash.NewHashFromStr(bv.NextHash)
			if err != nil {
				fmt.Println("Invalid hash:", bv.NextHash, err)
				return
			}
		}

	default:
		log.Printf("Unknown command: %s\n", cmd)
	}
}
