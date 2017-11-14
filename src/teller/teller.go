package teller

import (
	"errors"

	"github.com/sirupsen/logrus"

	"github.com/spaco/teller/src/exchange"
)

var (
	// ErrMaxBind is returned when the maximum number of address to bind to a SKY address has been reached
	ErrMaxBind = errors.New("max bind reached")
)

// BtcAddrGenerator generate new deposit address
type BtcAddrGenerator interface {
	NewAddress() (string, error)
}

// Exchanger provids apis to interact with exchange service
type Exchanger interface {
	BindAddress(cAddr, skyAddr, ct string) error
	GetDepositStatuses(address, ct string) ([]exchange.DepositStatus, error)
	// Returns the number of btc address the skycoin address binded
	BindNum(skyAddr string) (int, error)
	SetRate(cointype string, rate int) error
	GetRate(cointype string) (int, error)
}

// Config configures Teller
type Config struct {
	Service ServiceConfig
	HTTP    HTTPConfig
}

// Teller provides the HTTP and teller service
type Teller struct {
	log logrus.FieldLogger
	cfg Config // Teller configuration info

	httpServ *httpServer // HTTP API

	quit chan struct{}
}

// New creates a Teller
func New(log logrus.FieldLogger, exchanger Exchanger, btcAddrGen, skyAddrGen, ethAddrGen BtcAddrGenerator, cfg Config) (*Teller, error) {
	if err := cfg.HTTP.Validate(); err != nil {
		return nil, err
	}

	return &Teller{
		cfg:  cfg,
		log:  log.WithField("prefix", "teller"),
		quit: make(chan struct{}),
		httpServ: newHTTPServer(log, cfg.HTTP, &service{
			cfg:        cfg.Service,
			exchanger:  exchanger,
			btcAddrGen: btcAddrGen,
			skyAddrGen: skyAddrGen,
			ethAddrGen: ethAddrGen,
		}),
	}, nil
}

// Run starts the Teller
func (s *Teller) Run() error {
	s.log.Info("Starting teller...")
	defer s.log.Info("Teller closed")

	if err := s.httpServ.Run(); err != nil {
		s.log.WithError(err).Error()
		select {
		case <-s.quit:
			return nil
		default:
			return err
		}
	}

	return nil
}

// Shutdown close the Teller
func (s *Teller) Shutdown() {
	close(s.quit)
	s.httpServ.Shutdown()
}

// ServiceConfig configures service
type ServiceConfig struct {
	MaxBind int // maximum number of addresses allowed to bind to a SKY address
}

// service combines Exchanger and BtcAddrGenerator
type service struct {
	cfg        ServiceConfig
	exchanger  Exchanger        // exchange Teller client
	btcAddrGen BtcAddrGenerator // btc address generator
	skyAddrGen BtcAddrGenerator // sky address generator
	ethAddrGen BtcAddrGenerator // eth address generator
}

// BindAddress binds skycoin address with a deposit btc address
// return btc address
func (s *service) BindAddress(samosAddr, coinType string) (string, error) {
	if s.cfg.MaxBind != 0 {
		num, err := s.exchanger.BindNum(samosAddr)
		if err != nil {
			return "", err
		}

		if num >= s.cfg.MaxBind {
			return "", ErrMaxBind
		}
	}

	switch coinType {
	case "bitcoin":
		btcAddr, err := s.btcAddrGen.NewAddress()
		if err != nil {
			return "", err
		}

		if err := s.exchanger.BindAddress(btcAddr, samosAddr, coinType); err != nil {
			return "", err
		}

		return btcAddr, nil
	case "skycoin":
		skyAddr, err := s.skyAddrGen.NewAddress()
		if err != nil {
			return "", err
		}

		if err := s.exchanger.BindAddress(skyAddr, samosAddr, coinType); err != nil {
			return "", err
		}

		return skyAddr, nil
	case "ethcoin":
		ethAddr, err := s.ethAddrGen.NewAddress()
		if err != nil {
			return "", err
		}

		if err := s.exchanger.BindAddress(ethAddr, samosAddr, coinType); err != nil {
			return "", err
		}

		return ethAddr, nil
	}

	return "", errors.New("not support cointype")

}

// GetDepositStatuses returns deposit status of given skycoin（family) address
func (s *service) GetDepositStatuses(address, coinType string) ([]exchange.DepositStatus, error) {
	switch coinType {
	case "bitcoin":
		return s.exchanger.GetDepositStatuses(address, coinType)
	case "skycoin":
		return s.exchanger.GetDepositStatuses(address, coinType)
	case "ethcoin":
		return s.exchanger.GetDepositStatuses(address, coinType)
	}

	return []exchange.DepositStatus{}, errors.New("not support cointype")
}
func (s *service) SetRate(cointype string, rate int) error {
	return s.exchanger.SetRate(cointype, rate)
}

func (s *service) GetRate(cointype string) (int, error) {
	return s.exchanger.GetRate(cointype)
}
