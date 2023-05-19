package httpclient

import (
	"crypto/tls"
	"fmt"
	"github.com/weaveworks/scope/common/target"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/hashicorp/go-cleanhttp"
)

const (
	dialTimeout = 5 * time.Second
)

// AppConfig contains all the info needed for a probe to do HTTP requests
type AppConfig struct {
	Insecure   bool
	Identity   string
	TargetHost string //中转的地址
}

func (pc AppConfig) authorizeHeaders(headers http.Header) {
	headers.Set("Target-Host", target.TargetHost)
}

func (pc AppConfig) request(method string, urlStr string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, urlStr, body)
	req.Header.Add("Target-App", "scope")
	req.Header.Add("Target-Host", pc.TargetHost)
	fmt.Println("pc target host:", pc.TargetHost)
	if pc.TargetHost == "" && target.TargetHost != "" {
		fmt.Println("target host:", target.TargetHost)
		req.Header.Set("Target-Host", target.TargetHost)
	}
	if method == "POST" {
		req.Header.Add("Content-Type", "application/json")
	}
	return req, err
}

func (pc AppConfig) getHTTPTransport() *http.Transport {
	transport := cleanhttp.DefaultTransport()
	transport.DialContext = (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	if pc.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return transport
}
