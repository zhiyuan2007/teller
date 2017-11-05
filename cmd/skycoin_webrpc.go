/*************************************************************************
   > File Name: skycoin_webrpc.go
   > Author: ben
   > Mail: zhiyuan_06@126.com
   > Created Time: 一 10/16 23:09:41 2017
************************************************************************/
package main

import (
	"fmt"
	"github.com/skycoin/skycoin/src/api/webrpc"
	"github.com/skycoin/skycoin/src/visor"
)

func main() {
	rpcAddr := "127.0.0.1:6430"
	rpcClient := &webrpc.Client{
		Addr: rpcAddr,
	}
	//txid := ""
	//result, err := rpcClient.GetTransactionByID(txid)
	//if err != nil {
	//	fmt.Println("get tx failed\n")
	//}
	//fmt.Println("---%+v", reslt)
	st, _ := rpcClient.GetStatus()
	fmt.Printf("block count %d\n:", st.BlockNum)

	//start := uint64(3707)
	//end := uint64(3708)
	//param := []uint64{start, end}
	//blocks := visor.ReadableBlocks{}

	//if err := rpcClient.Do(&blocks, "get_blocks", param); err != nil {
	//fmt.Println("get_blocks failed\n")
	//}
	//fmt.Printf("%+v\n", blocks)
	//fmt.Printf("\n----------------------------\n")
	//fmt.Printf("%+v\n", blocks.Blocks[0])
	ss := []uint64{4707, 4709}
	blocks := visor.ReadableBlocks{}
	if err := rpcClient.Do(&blocks, "get_blocks_by_seq", ss); err != nil {
		fmt.Println("get_blocks_by_seq failed\n")
	}
	fmt.Printf("%+v\n", blocks)

	//param := []uint64{1}
	//blocks := visor.ReadableBlocks{}
	//if err := rpcClient.Do(&blocks, "get_lastblocks", param); err != nil {
	//fmt.Println("get_lastblocks failed\n")
	//}
	//fmt.Printf("%+v\n", blocks)
}
