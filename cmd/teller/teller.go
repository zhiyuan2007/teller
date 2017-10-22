// Skycoin teller, which provides service of monitoring the bitcoin deposite
// and sending skycoin coins
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"sync"
	"time"

	"github.com/boltdb/bolt"
	"github.com/btcsuite/btcrpcclient"
	"github.com/google/gops/agent"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/teller/src/addrs"
	"github.com/skycoin/teller/src/config"
	"github.com/skycoin/teller/src/exchange"
	"github.com/skycoin/teller/src/monitor"
	"github.com/skycoin/teller/src/scanner"
	"github.com/skycoin/teller/src/sender"
	"github.com/skycoin/teller/src/teller"
	"github.com/skycoin/teller/src/util/logger"
)

const (
	appDir = ".skycoin-teller"
	dbName = "data.db"
)

type dummyBtcScanner struct {
	log logrus.FieldLogger
}

func (s *dummyBtcScanner) Run() error {
	return nil
}

func (s *dummyBtcScanner) Shutdown() {}

func (s *dummyBtcScanner) AddScanAddress(addr string) error {
	s.log.WithField("addr", addr).Info("dummyBtcScanner.AddDepositAddress")
	return nil
}

func (s *dummyBtcScanner) GetDepositValue() <-chan scanner.DepositNote {
	s.log.Info("dummyBtcScanner.GetDepositValue")
	c := make(chan scanner.DepositNote)
	close(c)
	return c
}

func (s *dummyBtcScanner) GetScanAddresses() ([]string, error) {
	return []string{}, nil
}

type dummySkySender struct {
	log logrus.FieldLogger
}

func (s *dummySkySender) SendAsync(destAddr string, coins uint64) <-chan sender.Response {
	s.log.WithFields(logrus.Fields{
		"destAddr": destAddr,
		"coins":    coins,
	}).Info("dummySkySender.SendAsync")

	c := make(chan sender.Response, 1)
	c <- sender.Response{
		Err: fmt.Sprintf("dummySender.SendAsync: %s %d", destAddr, coins),
	}
	return c
}

func (s *dummySkySender) IsClosed() bool {
	return true
}

func (s *dummySkySender) IsTxConfirmed(txid string) bool {
	return true
}

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	configFile := flag.String("cfg", "config.json", "config.json file")
	btcAddrs := flag.String("btc-addrs", "btc_addresses.json", "btc_addresses.json file")
	skyAddrs := flag.String("sky-addrs", "sky_addresses.json", "sky_addresses.json file")
	ethAddrs := flag.String("eth-addrs", "eth_addresses.json", "eth_addresses.json file")
	debug := flag.Bool("debug", false, "debug mode will show more detail logs")
	dummyMode := flag.Bool("dummy", false, "run without real btcd or skyd service")
	profile := flag.Bool("prof", false, "start gops profiling tool")
	logFilename := flag.String("log-file", "teller.log", "teller log filename")

	// TODO -- merge flags with config.json loading -- should use a library for this
	httpAddr := flag.String("http-service-addr", "127.0.0.1:7071", "http api service address")
	httpsAddr := flag.String("https-service-addr", "", "https api service address")
	autoTLSHost := flag.String("auto-tls-host", "", "generate certificate with Let's Encrypt for this hostname and use it")
	tlsKey := flag.String("tls-key", "", "tls key file (if not using -auto-tls-host)")
	tlsCert := flag.String("tls-cert", "", "tls cert file (if not using -auto-tls-host)")
	staticDir := flag.String("static-dir", "./web/build", "static directory to serve html interface from")
	startAt := flag.String("start-time", "", "Don't process API requests until after this timestamp (RFC3339 format)")
	thrMax := flag.Int64("throttle-max", 5, "max allowd per ip in specific duration")
	thrDur := flag.Int64("throttle-duration", int64(time.Minute), "throttle duration")

	flag.Parse()

	// init logger
	rusloggger, err := logger.NewLogger(*logFilename, *debug)
	if err != nil {
		fmt.Println("Failed to create Logrus logger:", err)
		return err
	}

	log := rusloggger.WithField("prefix", "teller")

	if *profile {
		// start gops agent, for profilling
		if err := agent.Listen(&agent.Options{
			NoShutdownCleanup: true,
		}); err != nil {
			log.WithError(err).Error("Start profile agent failed")
			return err
		}
	}

	quit := make(chan struct{})
	go catchInterrupt(quit)

	// load config
	cfg, err := config.New(*configFile)
	cfg.CoinTypes = []string{"bitcoin", "skycoin"}
	if err != nil {
		log.WithError(err).Error("Load config failed")
		return err
	}

	startAtStamp := time.Time{}
	if *startAt != "" {
		var err error
		startAtStamp, err = time.Parse(time.RFC3339, *startAt)
		if err != nil {
			log.WithField("format", time.RFC3339).Error("Invalid -start-time, must be in RCF3339 format")
			return err
		}
		startAtStamp = startAtStamp.UTC()
	}

	httpConfig := teller.HTTPConfig{
		HTTPAddr:    *httpAddr,
		HTTPSAddr:   *httpsAddr,
		StaticDir:   *staticDir,
		StartAt:     startAtStamp,
		AutoTLSHost: *autoTLSHost,
		TLSCert:     *tlsCert,
		TLSKey:      *tlsKey,
		Throttle: teller.Throttle{
			Max:      *thrMax,
			Duration: time.Duration(*thrDur),
		},
	}

	if err := httpConfig.Validate(); err != nil {
		log.WithError(err).Error("Invalid HTTP config")
		return err
	}

	appDir, err := createAppDirIfNotExist(appDir)
	if err != nil {
		log.WithError(err).Error("Create AppDir failed")
		return err
	}

	// open db
	dbPath := filepath.Join(appDir, dbName)
	db, err := bolt.Open(dbPath, 0700, &bolt.Options{
		Timeout: 1 * time.Second,
	})
	if err != nil {
		log.WithError(err).Error("Open db failed")
		return err
	}

	errC := make(chan error)
	wg := sync.WaitGroup{}

	background := func(name string, errC chan<- error, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := f()
			if err != nil {
				log.WithError(err).Errorf("%s failed", name)
				errC <- err
			}
		}()
	}

	var btcScanner scanner.Scanner
	var skyScanner scanner.Scanner
	var ethScanner scanner.Scanner
	var scanRPC exchange.BtcScanner
	var sendService *sender.SendService
	var sendRPC exchange.SkySender

	if *dummyMode {
		log.Info("btcd and skyd disabled, running in dummy mode")
		btcScanner = &dummyBtcScanner{log: log}
		skyScanner = &dummyBtcScanner{log: log}
		ethScanner = &dummyBtcScanner{log: log}
		sendRPC = &dummySkySender{log: log}
	} else {
		// check skycoin setup
		if err := checkSkycoinSetup(*cfg); err != nil {
			log.WithError(err).Error("checkSkycoinSetup failed")
			return err
		}
		// check samoscoin setup
		if err := checkSamoscoinSetup(*cfg); err != nil {
			log.WithError(err).Error("checkSamoscoinSetup failed")
			return err
		}
		// create btc rpc client
		btcrpcConnConf := makeBtcrpcConfg(*cfg)
		btcrpc, err := btcrpcclient.New(&btcrpcConnConf, nil)
		if err != nil {
			log.WithError(err).Error("Connect btcd failed")
			return err
		}
		log.Info("Connect to btcd success")
		skyrpc := sender.NewRPC(cfg.Skynode.WalletPath, cfg.Skynode.RPCAddress)
		log.Info("Connect to skycoind success")

		// create scan service
		btcScanner, err = scanner.NewBTCScanner(log, db, btcrpc, scanner.Config{
			ScanPeriod: cfg.Btcscan.CheckPeriod,
			CoinTypes:  cfg.CoinTypes,
		})
		if err != nil {
			log.WithError(err).Error("Open scan service failed")
			return err
		}

		background("btcScanner.Run", errC, btcScanner.Run)

		// create scan service
		skyScanner, err = scanner.NewSKYScanner(log, db, skyrpc, scanner.Config{
			ScanPeriod: cfg.Btcscan.CheckPeriod,
			CoinTypes:  cfg.CoinTypes,
		})
		if err != nil {
			log.WithError(err).Error("Open scan service failed")
			return err
		}

		background("skyScanner.Run", errC, skyScanner.Run)

		ethScanner, err = scanner.NewETHScanner(log, db, cfg.Ethurl, scanner.Config{
			ScanPeriod: cfg.Btcscan.CheckPeriod,
			CoinTypes:  cfg.CoinTypes,
		})
		if err != nil {
			log.WithError(err).Error("Open scan service failed")
			return err
		}

		background("ethScanner.Run", errC, ethScanner.Run)

		samosRPC := sender.NewRPC(cfg.Samosnode.WalletPath, cfg.Samosnode.RPCAddress)

		// create skycoin send service
		sendService = sender.NewService(makeSendConfig(*cfg), log, samosRPC)

		background("sendService.Run", errC, sendService.Run)

		sendRPC = sender.NewSender(sendService)
	}

	// create exchange service
	exchangeService := exchange.NewService(log, db, btcScanner, skyScanner, ethScanner, sendRPC, exchange.Config{
		Rate: cfg.ExchangeRate,
	})
	background("exchangeService.Run", errC, exchangeService.Run)

	exchangeClient := exchange.NewClient(exchangeService)

	// create bitcoin address manager
	f, err := ioutil.ReadFile(*btcAddrs)
	if err != nil {
		log.WithError(err).Error("Load deposit bitcoin address list failed")
		return err
	}

	btcAddrMgr, err := addrs.NewBTCAddrs(log, db, bytes.NewReader(f))
	if err != nil {
		log.WithError(err).Error("Create bitcoin deposit address manager failed")
		return err
	}
	f, err = ioutil.ReadFile(*skyAddrs)
	if err != nil {
		log.WithError(err).Error("Load deposit skycoin address list failed")
		return err
	}

	skyAddrMgr, err := addrs.NewSkyAddrs(log, db, bytes.NewReader(f))
	if err != nil {
		log.WithError(err).Error("Create skycoin deposit address manager failed")
		return err
	}
	f, err = ioutil.ReadFile(*ethAddrs)
	if err != nil {
		log.WithError(err).Error("Load deposit ethcoin address list failed")
		return err
	}

	ethAddrMgr, err := addrs.NewEthAddrs(log, db, bytes.NewReader(f))
	if err != nil {
		log.WithError(err).Error("Create ethcoin deposit address manager failed")
		return err
	}

	tellerServer, err := teller.New(log, exchangeClient, btcAddrMgr, skyAddrMgr, ethAddrMgr, teller.Config{
		Service: teller.ServiceConfig{
			MaxBind: cfg.MaxBind,
		},
		HTTP: httpConfig,
	})
	if err != nil {
		log.WithError(err).Error("teller.New failed")
		return err
	}

	// Run the service
	background("tellerServer.Run", errC, tellerServer.Run)

	// start monitor service
	monitorCfg := monitor.Config{
		Addr: cfg.MonitorAddr,
	}
	monitorService := monitor.New(log, monitorCfg, btcAddrMgr, skyAddrMgr, exchangeClient, scanRPC)

	background("monitorService.Run", errC, monitorService.Run)

	var finalErr error
	select {
	case <-quit:
	case finalErr = <-errC:
		if finalErr != nil {
			log.WithError(finalErr).Error("Goroutine error")
		}
	}

	log.Info("Shutting down...")

	if monitorService != nil {
		log.Info("Shutting down monitorService")
		monitorService.Shutdown()
	}

	// close the skycoin send service
	if sendService != nil {
		log.Info("Shutting down sendService")
		sendService.Shutdown()
	}

	// close exchange service
	log.Info("Shutting down exchangeService")
	exchangeService.Shutdown()

	// close the teller service
	log.Info("Shutting down tellerServer")
	tellerServer.Shutdown()

	// close the scan service
	if btcScanner != nil {
		log.Info("Shutting down btcScanner")
		btcScanner.Shutdown()
	}

	log.Info("Waiting for goroutines to exit")

	wg.Wait()

	log.Info("Shutdown complete")

	return finalErr
}

func makeBtcrpcConfg(cfg config.Config) btcrpcclient.ConnConfig {
	//certs, err := ioutil.ReadFile(cfg.Btcrpc.Cert)
	//if err != nil {
	//	panic(fmt.Sprintf("btc rpc cert file does not exist in %s", cfg.Btcrpc.Cert))
	//}

	return btcrpcclient.ConnConfig{
		//Endpoint:     "ws",
		Host:         cfg.Btcrpc.Server,
		User:         cfg.Btcrpc.User,
		Pass:         cfg.Btcrpc.Pass,
		DisableTLS:   true,
		HTTPPostMode: true,

		//Certificates: certs,
	}
}

func makeSendConfig(cfg config.Config) sender.Config {
	return sender.Config{
		ReqBufSize: cfg.SkySender.ReqBuffSize,
	}
}

func createAppDirIfNotExist(app string) (string, error) {
	cur, err := user.Current()
	if err != nil {
		return "", err
	}
	path := filepath.Join(cur.HomeDir, app)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// create the dir
		if err := os.Mkdir(path, 0700); err != nil {
			return "", err
		}
	}
	return path, nil
}

func catchInterrupt(quit chan<- struct{}) {
	sigchan := make(chan os.Signal, 1)
	signal.Notify(sigchan, os.Interrupt)
	<-sigchan
	signal.Stop(sigchan)
	close(quit)
}

// checks skycoin setups
func checkSkycoinSetup(cfg config.Config) error {
	// check whether the skycoin wallet file does exist
	if _, err := os.Stat(cfg.Skynode.WalletPath); os.IsNotExist(err) {
		return fmt.Errorf("skycoin wallet file: %s does not exist", cfg.Skynode.WalletPath)
	}

	// test if skycoin node rpc service is reachable
	conn, err := net.Dial("tcp", cfg.Skynode.RPCAddress)
	if err != nil {
		return fmt.Errorf("connect to skycoin node %s failed: %v", cfg.Skynode.RPCAddress, err)
	}

	conn.Close()

	return nil
}

// checks skycoin setups
func checkSamoscoinSetup(cfg config.Config) error {
	// check whether the skycoin wallet file does exist
	if _, err := os.Stat(cfg.Samosnode.WalletPath); os.IsNotExist(err) {
		return fmt.Errorf("samoscoin wallet file: %s does not exist", cfg.Samosnode.WalletPath)
	}

	// test if skycoin node rpc service is reachable
	conn, err := net.Dial("tcp", cfg.Samosnode.RPCAddress)
	if err != nil {
		return fmt.Errorf("connect to samoscoin node %s failed: %v", cfg.Samosnode.RPCAddress, err)
	}

	conn.Close()

	return nil
}
