package proxy

import "github.com/skycoin/teller/src/daemon"

// BindResponse http response for /api/bind
type BindResponse struct {
	BtcAddress string `json:"address,omitempty"`
	CoinType   string `json:"coin_type"`
	// Error      string `json:"error,omitempty"`
}
type UnifiedResponse struct {
	Errmsg string `json:"errmsg,omitempty"`
	Ok     int    `json:"ok"`
	Data   string `json:"data"`
	// Error      string `json:"error,omitempty"`
}

// StatusResponse http response for /api/status
type StatusResponse struct {
	Statuses []daemon.DepositStatus `json:"statuses,omitempty"`
	// Error    string                 `json:"error,omitempty"`
}

func makeBindHTTPResponse(rsp daemon.BindResponse) BindResponse {
	return BindResponse{
		BtcAddress: rsp.BtcAddress,
		CoinType:   rsp.CoinType,
		// Error:      rsp.Error,
	}
}

func makeStatusHTTPResponse(rsp daemon.StatusResponse) StatusResponse {
	return StatusResponse{
		Statuses: rsp.Statuses,
		// Error:    rsp.Error,
	}
}
func makeUnifiedHTTPResponse(ok int, data, errmsg string) UnifiedResponse {
	return UnifiedResponse{
		Ok:     ok,
		Data:   data,
		Errmsg: errmsg,
		// Error:    rsp.Error,
	}
}
