package httpclient

import (
	"net"
	"net/http"
	"time"
)

func pooledTransport(maxPerHost int) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.MaxIdleConns = maxPerHost * 2
	transport.MaxIdleConnsPerHost = maxPerHost
	transport.MaxConnsPerHost = maxPerHost
	transport.IdleConnTimeout = 90 * time.Second
	transport.ResponseHeaderTimeout = 15 * time.Second
	return transport
}
