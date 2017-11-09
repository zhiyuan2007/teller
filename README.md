# Teller

[![Build Status](https://travis-ci.org/skycoin/teller.svg?branch=master)](https://travis-ci.org/skycoin/teller)

## Releases & Branches

Last stable release is in `master` branch, which will match the latest tagged release.

Pull requests should target the `develop` branch, which is the default branch.

If you clone, make sure you `git checkout master` to get the latest stable release.

When a new release is published, `develop` will be merged to `master` then tagged.

## Setup project

### Prerequisites

* Have go1.8+ installed
* Have `GOPATH` env set
* Have btcd started
* Have skycoin node started

### Install teller and start
最简单的方法：
进到目录：spaco/teller/cmd/teller
运行命令：go build
如果缺少哪个库，则运行go get xxxx,
比如缺少 skycoin则运行go get github.com/skycoin/skycoin

### Summary of setup 

```bash
# Generate btc_addresses.json file. 'foobar' is an arbitrary seed, and 10 is an arbitrary number of addresses to generate
go run cmd/tool/tool.go -json newbtcaddress foobar 10 > /tmp/btc_addresses.json

### Start teller

Install `spo-cli`

```bash
make install-skycoin-cli
```

add pregenerated bitcoin deposit address list in `btc_addresses.json`.

```bash
{
    "btc_addresses": [
        "1PZ63K3G4gZP6A6E2TTbBwxT5bFQGL2TLB",
        "14FG8vQnmK6B7YbLSr6uC5wfGY78JFNCYg",
        "17mMWfVWq3pSwz7BixNmfce5nxaD73gRjh",
        "1Bmp9Kv9vcbjNKfdxCrmL1Ve5n7gvkDoNp"
    ]
}
```
add pregenerated ethcoin deposit address list in `eth_addresses.json`.

```bash
{
    "eth_addresses": [
        "0x2cf014d432e92685ef1cf7bc7967a4e4debca092",
        "0xea674fdde714fd979de3edf0f56aa9716b898ec8"
    ]
}
```

add pregenerated skycoin deposit address list in `sky_addresses.json`.

```bash
{
    "sky_addresses": [
        "2do3K1YLMy3Aq6EcPMdncEurP5BfAUdFPJj",
        "vX7vey11wqagGjXv4znJoGFL2zLgvAK8Dj"
    ]
}
```
use `tool` to pregenerate bitcoin address list:

```bash
cd cmd/tool
go run tool.go -json newbtcaddress $seed $num
```

Example:

```bash
go run tool.go -json newbtcaddress 12323 3

2Q5sR1EgTWesxX9853AnNpbBS1grEY1JXn3
2Q8y3vVAqY8Q3paxS7Fz4biy1RUTY5XQuzb
216WfF5EcvpVk6ypSRP3Lg9BxqpUrgBJBco
```

generate json file Example:

```bash
go run tool.go -json newbtcaddress 12323 3 > new_btc_addresses.json
```


teller's config is managed in `config.json`, need to set the `wallet_path`
in `sponode` field to an absolute path of spocoin wallet file, and set up the `btcd`
config in `btc_rpc` field, including server address, username, password and
absolute path to the cert file, ethcoin and skycoin is also.

config.json:

```
{
    "http_address": "127.0.0.1:7071",
    "reconnect_time": 5,
    "dial_timeout": 5,
    "ping_timeout": 5,
    "pong_timeout": 10,
    "btc_exchange_rate": 250000,
    "sky_exchange_rate": 100,
    "eth_exchange_rate": 10000,
    "max_bind": 5,
    "session_write_bufer_size": 100,
    "monitor_address": "127.0.0.1:7711",
    "spaconode": {
        "rpc_address": "127.0.0.1:8431",
        "wallet_path": "/path/wallets/spo.wlt"
    },
    "skynode": {
        "rpc_address": "127.0.0.1:6430",
        "wallet_path": "/path/wallets/sky.wlt"
    },
    "btc_scan": {
        "check_period": 20,
        "deposit_buffer_size": 1024
    },
    "btc_rpc": {
        "server": "127.0.0.1:8822",
        "user": "testrpc",
        "pass": "testpasswd",
        "cert": "/path/btc/btc.conf"
    },
    "sky_sender": {
        "request_buffer_size": 1024
    },
    "spo_sender": {
        "request_buffer_size": 1024        
    },
    "support_cointypes": ["bitcoin", "skycoin", "ethcoin"],
    "ethurl" : "http://127.0.0.1:8545"
}

```

run teller service
- confirmations-required 确认数
- scan-height 从指定高度开始扫描

```bash
go run teller.go -cfg config.json  -btc-addrs btc_address.json -sky-addrs sky_address.json  -eth-addrs eth_address.json -btc-confirmations-required 1 -eth-scan-height 4495493
```

## Service apis

The HTTP API service is provided by the proxy and serve on port 7071 by default.

The API returns JSON for all 200 OK responses.

the API returns JSON object format as 
```
{
    "errmsg": "",
    "code": 0,
    "data": {
        "address": "2do3K1YLMy3Aq6EcPMdncEurP5BfAUdFPJj",
        "coin_type": "skycoin"
    }
}
```
code = 0 is success, code != 0 is failed

### Bind

```bash
Method: POST
Accept: application/json
Content-Type: application/json
URI: /api/bind
Request Body: {
    "address": "..."
    "plan_coin_type": "spocoin"
    "tokenType":"[bitcoin|ethcoin|skycoin]"
}
```

Binds a spocoin address to a BTC address. A spocoin address can be bound to
multiple BTC addresses.  The default maximum number of bound addresses is 10.

Example:

```bash
curl -X POST -H "Content-Type:application/json" -d '{"address":"2AzuN3aqF53vUC2yHqdfMKnw4i8eRrwye71","plan_coin_type":"spocoin","coin_type":"skycoin"}' http://localhost:7071/api/bind/
```

response:

```bash
{
    "errmsg": "",
    "code": 0,
    "data": {
        "address": "2do3K1YLMy3Aq6EcPMdncEurP5BfAUdFPJj",
        "coin_type": "skycoin"
    }
}
```

### Status

```bash
Method: GET
Content-Type: application/json
URI: /api/status
Query Args: skyaddr
```

Returns statuses of a spocoin address.

Since a single spocoin address can be bound to multiple BTC addresses the result is in an array.
The default maximum number of BTC addresses per spocoin address is 5.

Possible statuses are:

* `waiting_deposit` - Spocoin address is bound, no deposit seen on BTC address yet
* `waiting_send` - BTC deposit detected, waiting to send spocoin out
* `waiting_confirm` - Spocoin sent out, waiting to confirm the spocoin transaction
* `done` - Skycoin transaction confirmed

Example:

```bash
curl http://localhost:7071/api/status?address=2AzuN3aqF53vUC2yHqdfMKnw4i8eRrwye71\&coin_type=bitcoin
```

response:

```bash
{
    "statuses": [
        {
            "seq": 1,
            "update_at": 1501137828,
            "address":"ZJHwZfwXrqq49bEKmXXCqjcMTzF8RkQSXm",
            "tokenType":"bitcoin"
            "status": "done"
        },
        {
            "seq": 2,
            "update_at": 1501128062,
            "address":"ZJHwZfwXrqq49bEKmXXCqjcMTzF8RkQSXm",
            "tokenType":"bitcoin"
            "status": "waiting_deposit"
        },
        {
            "seq": 3,
            "update_at": 1501128063,
            "address":"ZJHwZfwXrqq49bEKmXXCqjcMTzF8RkQSXm",
            "tokenType":"bitcoin"
            "status": "waiting_deposit"
        },
    ]
}
```

### Code linting

```bash
make install-linters
make lint
```

### Run tests

```bash
make test
```

### Database structure

```
Bucket: used_btc_address
File: btcaddrs/store.go

Maps: `btcaddr -> ""`
Note: Marks a btc address as used
```

```
Bucket: exchange_meta
File: exchange/store.go

Note: unused
```

```
Bucket: deposit_info
File: exchange/store.go

Maps: btcTx[%tx:%n] -> exchange.DepositInfo
Note: Maps a btc txid:seq to exchange.DepositInfo struct
```

```
Bucket: bind_address
File: exchange/store.go

Maps: btcaddr -> skyaddr
Note: Maps a btc addr to a sky addr
```

```
Bucket: sky_deposit_seqs_index
File: exchange/store.go

Maps: skyaddr -> [btcaddrs]
Note: Maps a sky addr to multiple btc addrs
```

```
Bucket: btc_txs
File: exchange/store.go

Maps: btcaddr -> [txs]
Note: Maps a btcaddr to multiple btc txns
```

```
Bucket: scan_meta
File: scanner/store.go

Maps: "last_scan_block" -> scanner.lastScanBlock[json]
Note: Saves scanner.lastScanBlock struct (as JSON) to "last_scan_block" key

Maps: "deposit_addresses" -> [btcaddrs]
Note: Saves list of btc addresss being scanned

Maps: "dv_index_list" -> [btcTx[%tx:%n]][json]
Note: Saves list of btc txid:seq (as JSON)
```

```
Bucket: deposit_value
File: scanner/store.go

Maps: btcTx[%tx:%n] -> scanner.DepositValue
Note: Maps a btc txid:seq to scanner.DepositValue struct
```
