package httpclient

import (
	"encoding/json"
	"fmt"
	jsoniter "github.com/json-iterator/go"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/go-cleanhttp"
)

const (
	httpClientTimeout = 12 * time.Second // a bit less than default app.window
	initialBackoff    = 1 * time.Second
	maxBackoff        = 60 * time.Second
)

// AppClient is a client to an app, dealing with report publishing, controls and pipes.
type AppClient interface {
	GetNodeLimitFromControlGateway() (*NodeLimit, error)
	GetNodeLimitFromMaster() (*NodeLimit, error)
	SetSlaveNodeLimit(data *NodeLimit) error
}

// appClient is a client to an app, dealing with report publishing, controls and pipes.
type appClient struct {
	AppConfig
	target url.URL //请求的地址
	client *http.Client
}

// NewAppClient makes a new appClient.
func NewAppClient(pc AppConfig, target url.URL) AppClient {
	httpTransport := pc.getHTTPTransport()
	httpClient := cleanhttp.DefaultClient()
	httpClient.Transport = httpTransport
	httpClient.Timeout = httpClientTimeout

	return &appClient{
		AppConfig: pc,
		target:    target,
		client:    httpClient,
	}
}
func (c *appClient) GetNodeLimitFromControlGateway() (*NodeLimit, error) {
	var (
		resp      *http.Response
		controRes = ControRes{}
	)
	req, err := c.AppConfig.request("GET", c.url("/api/authorization/node/limit"), nil)
	if err != nil {
		return nil, err
	}
	resp, err = c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Error response from %s: %s", c.url("/api"), resp.Status)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&controRes); err != nil {
		return nil, err
	}
	return &controRes.Data, nil
}

func (c *appClient) GetNodeLimitFromMaster() (*NodeLimit, error) {
	var (
		resp      *http.Response
		nodeLimit = NodeLimit{}
	)
	req, err := c.AppConfig.request("GET", c.url("/api/node/authorize/info"), nil)
	if err != nil {
		return nil, err
	}
	resp, err = c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Error response from %s: %s", c.url("/api/node/authorize/info"), resp.Status)
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(&nodeLimit); err != nil {
		return nil, err
	}
	return &nodeLimit, nil
}

func (c *appClient) SetSlaveNodeLimit(data *NodeLimit) error {
	var (
		resp *http.Response
	)
	tmpRequest, _ := jsoniter.MarshalToString(data)

	req, err := c.AppConfig.request("POST", c.url("/api/node/limit"), strings.NewReader(tmpRequest))
	if err != nil {
		return err
	}
	resp, err = c.client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Error response from %s: %s", c.url("/api/node/limit"), resp.Status)
	}
	defer resp.Body.Close()

	return nil
}
func (c *appClient) url(path string) string {
	return c.target.String() + path
}
