// Copyright (c) 2014-2015 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcrpcclient"
)

func getBlockAtHeight(btcClient *btcrpcclient.Client, height int64) (*btcjson.GetBlockVerboseResult, error) {

	hash, err := btcClient.GetBlockHash(height)
	if err != nil {
		return nil, err
	}

	block, err := btcClient.GetBlockVerbose(hash)
	if err != nil {
		return nil, err
	}

	return block, nil
}

func getblockandtx() {
	connCfg := &btcrpcclient.ConnConfig{
		Host:         "118.190.40.103:18332",
		User:         "RPCuser",
		Pass:         "RPCpasswd",
		HTTPPostMode: true, // Bitcoin core only supports HTTP POST mode
		DisableTLS:   true, // Bitcoin core does not provide TLS by default
	}
	// Notice the notification parameter is nil since notifications are
	// not supported in HTTP POST mode.
	client, err := btcrpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Shutdown()

	// Get the current block count.
	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("blockcount %d\n", blockCount)

	block, err := getBlockAtHeight(client, 494411)
	if err != nil {
		log.Fatal(err)
	}
	for _, txidstr := range block.Tx {
		txid, err := chainhash.NewHashFromStr(txidstr)
		if err != nil {
			fmt.Printf("new hash from str failed id: %s\n", txidstr)
			continue
		}
		tx, err := client.GetRawTransactionVerbose(txid)
		if err != nil {
			fmt.Printf("get getRawTransactionVerbose failed: %s, err: %s\n", txidstr, err.Error())
			continue
		}
		fmt.Printf("tx:%+v\n", tx.Txid)
	}
}

func main() {

	getblockandtx()
	return
	// Connect to local bitcoin core RPC server using HTTP POST mode.
	//connCfg := &btcrpcclient.ConnConfig{
	//Host:         "localhost:8822",
	//User:         "zoinrpc",
	//Pass:         "de86929b1dfdf1371826bcf6c57782fa",
	//HTTPPostMode: true, // Bitcoin core only supports HTTP POST mode
	//DisableTLS:   true, // Bitcoin core does not provide TLS by default
	//}
	connCfg := &btcrpcclient.ConnConfig{
		Host:         "118.190.40.103:18332",
		User:         "RPCuser",
		Pass:         "RPCpasswd",
		HTTPPostMode: true, // Bitcoin core only supports HTTP POST mode
		DisableTLS:   true, // Bitcoin core does not provide TLS by default
	}
	// Notice the notification parameter is nil since notifications are
	// not supported in HTTP POST mode.
	client, err := btcrpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Shutdown()

	// Get the current block count.
	blockCount, err := client.GetBlockCount()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Block count: %d", blockCount)
	blockdifficulty, err := client.GetDifficulty()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Block difficulty: %f", blockdifficulty)
	return
	//address := []string{"1BfnBDSdMJNQYqhzxCSAuhbNRihx3HhD3T", "1CvAyaHHqtTwp954Ev2i1ZUs1MQmCBytgf"}
	//addr, err := client.GetNewAddress("mymacbitcoin")
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("get address : %+v", addr)

	//address, err := client.GetAddressesByAccount("ZG6wTVgsDizmtkY3yESpBQevcNjCnQE7Jj")
	////address, err := client.GetAddressesByAccount("mymacbitcoin")
	//if err != nil {
	//log.Fatal(err)
	//}
	//addrs := "ZJXPUSbDCBpXJBrfHGXXoDi99zF6dt1wLD"
	//addrs := "ZTYTTBW2BGejD275GmugxMNDz14Qmq2SNa"
	//addrs := "ZMbNTToYFGsU23namMhffCMFZfT4AYiddo"
	//addr, err := btcutil.DecodeAddress(addrs, &chaincfg.MainNetParams)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("get address: %+s\n", addr)
	//tx, err := client.SendFrom("ZG6wTVgsDizmtkY3yESpBQevcNjCnQE7Jj", addr, 22220)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("get tx: %+v\n", tx)
	//address := []btcutil.Address{addr}
	//log.Printf("get address: %+v\n", address)

	hash, err := client.GetBestBlockHash()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("get hash: %+v\n", hash)
	h, err := client.GetBlockVerbose(hash)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("blk versbose: %+v\n", h)
	for _, txidstr := range h.Tx {
		txid, err := chainhash.NewHashFromStr(txidstr)
		if err != nil {
			fmt.Printf("new hash from str failed id: %s\n", txidstr)
			continue
		}
		tx, err := client.GetRawTransactionVerbose(txid)
		if err != nil {
			fmt.Printf("get getRawTransactionVerbose failed: %s, err: %s\n", txidstr, err.Error())
			continue
		}
		fmt.Printf("tx:%+v\n", tx)
	}

	//for _, addr := range address {
	//tx, err := client.SendFrom("ZG6wTVgsDizmtkY3yESpBQevcNjCnQE7Jj", addr, 1)
	//if err != nil {
	//log.Fatal(err)
	//}

	//log.Printf("get tx: %+v\n", tx)
	//hashinfo, err := client.GetTransaction(tx)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("----------hashinfo: %+v", hashinfo)
	//hashinfo1, err := client.GetRawTransactionVerbose(tx)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("----------hashinfo1111: %+v", hashinfo1)
	//hashinfo2, err := client.GetBlockVerboseTx(tx)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("----------hashinfo222: %+v", hashinfo2)

	//break
	//}
	//log.Printf("----------rawtx: %+v", hashinfo1)
	//hashinfo2, err := client.GetTxOut(tx, 1, true)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("----------hashinfo: %+v", hashinfo2)
	//break
	//}
	//private, err := client.DumpPrivKey(address[0])
	//if err != nil {
	//log.Fatal(err)
	//}
	//valids, err := client.ValidateAddress(address[0])
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("address: %s\n", address[0].String())
	////log.Printf("private sec: %s\n", private.String())
	//log.Printf("public : %s\n", valids.PubKey)
	////zebrad -conf=/Users/liuguirong/project/data/zebra/zebra.conf dumpprivkey  ZKNAKEFXV6Vqwkv2rGg7MhpPijUWtiueaf
	////account
	//acc, err := client.GetBalance("ZG6wTVgsDizmtkY3yESpBQevcNjCnQE7Jj")
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("get balance: %+v", acc)

	//unspents, err := client.ListUnspentMinMaxAddresses(0, 999999, address)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("get unspents: %+v", unspents)
	//bal, err := client.GetReceivedByAddressMinConf(address[0], 0)
	//if err != nil {
	//log.Fatal(err)
	//}
	//log.Printf("get bal: %+v", bal)
}
