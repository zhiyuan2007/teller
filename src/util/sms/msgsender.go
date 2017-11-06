package sms

/*************************************************************************
   > File Name: ../util/sms/msgsender.go
   > Author: ben
   > Mail: zhiyuan_06@126.com
   > Created Time: 一 11/ 6 22:36:11 2017
************************************************************************/

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
)

func exec_shell(s string) {
	cmd := exec.Command("/bin/bash", "-c", s)
	var out bytes.Buffer

	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s", out.String())
}
func Sendmsg(addr, coins, ct string) {
	cmd := "/usr/local/bin/node /opt/project/smssender/send.js " + addr + " " + coins + " " + ct
	exec_shell(cmd)
}
