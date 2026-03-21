package ipfsgateway

import (
	"net/http"
	"time"

	"github.com/ipfs/boxo/blockservice"
	"github.com/ipfs/boxo/gateway"
)

// NewHandler 建立唯讀閘道 HTTP 處理器（/ipfs/...）。
func NewHandler(fetchTimeout time.Duration, bs blockservice.BlockService) (http.Handler, error) {
	bb, err := gateway.NewBlocksBackend(bs)
	if err != nil {
		return nil, err
	}
	rt := fetchTimeout
	if rt <= 0 {
		rt = gateway.DefaultRetrievalTimeout
	}
	cfg := gateway.Config{
		DeserializedResponses: true,
		RetrievalTimeout:      rt,
		DisableHTMLErrors:     true,
	}
	return gateway.NewHandler(cfg, bb), nil
}
