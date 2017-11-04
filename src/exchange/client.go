package exchange

// Client provides helper apis to interact with exchange service
type Client struct {
	s *Service
}

// NewClient creates exchange client
func NewClient(s *Service) *Client {
	return &Client{s: s}
}

// BindAddress binds deposit btc address with skycoin address, and
// add the btc address to scan service, when detect deposit coin
// to the btc address, will send specific skycoin to the binded
// skycoin address
func (ec *Client) BindAddress(cAddr, skyAddr, ct string) error {
	return ec.s.bindAddress(cAddr, skyAddr, ct)
}

// GetDepositStatuses returns deamon.DepositStatus array of given skycoin address
func (ec *Client) GetDepositStatuses(skyAddr, ct string) ([]DepositStatus, error) {
	return ec.s.getDepositStatuses(skyAddr, ct)
}

// BindNum returns the number of btc address the given sky address binded
func (ec *Client) BindNum(skyAddr string) (int, error) {
	return ec.s.getBindNum(skyAddr)
}

// GetDepositStatusDetail returns deposit status details
func (ec *Client) GetDepositStatusDetail(flt DepositFilter) ([]DepositStatusDetail, error) {
	return ec.s.getDepositStatusDetail(flt)
}
