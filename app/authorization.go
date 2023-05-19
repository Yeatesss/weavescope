package app

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/weaveworks/scope/app/httpclient"
	"math/rand"
	"net/url"
	"os"
	"strings"
	"sync"
)

var (
	pushSlaveSignal                                                                                                    = make(chan string, 100)
	LimitNode         int64                                                                                            = -2 //-2：未设置 -1:无限制 >=0:限制
	AutoAuthorize     bool                                                                                                  //-2：未设置 -1:无限制 >=0:限制
	AuthorizeNodeList = &AuthorizeNode{Lock: sync.RWMutex{}, Nodes: make(map[string]struct{}), NodesSlice: []string{}}      //已授权节点列表
	SlaveCollectors   = &SlaveCollector{Lock: sync.RWMutex{}, Collectors: make(map[string]struct{})}                        //已授权节点列表,形如http://127.0.0.1,http://127.0.0.2,目标地址:中转地址
)

type Authorization struct {
	AppConfig
}
type AppConfig struct {
	Identity string
	Insecure bool
}

func NewAuthorization(appConfig AppConfig) *Authorization {
	CollectorappIdentity = appConfig.Identity
	fmt.Println("Control host:", os.Getenv("CONTROL_GATEWAY_HOST"))
	fmt.Println("Control Proxy host:", os.Getenv("CONTROL_PROXY_HOST"))
	switch appConfig.Identity {
	case "master":
		var err error
		gatewayU, err := url.Parse(os.Getenv("CONTROL_GATEWAY_HOST"))
		gatewayClient := httpclient.NewAppClient(httpclient.AppConfig{
			Insecure: appConfig.Insecure,
			Identity: appConfig.Identity,
		}, *gatewayU)
		res, err := gatewayClient.GetNodeLimitFromControlGateway()
		if err == nil {
			LimitNode = res.Limit
			AutoAuthorize = res.AutoAuthorize
			for _, rp := range res.Nodes {
				AuthorizeNodeList.Set(rp)
			}
			log.Infof("Node limit: %d,Auth authorize:%v,Node list length:%v", LimitNode, AutoAuthorize, AuthorizeNodeList.Len())
		} else {
			log.Errorf("Error setting node limit: %v", err)
		}
		go func() {
			log.Infof("Push node limit to slaves")
			for signal := range pushSlaveSignal {
				tmpNodeLimit := httpclient.NodeLimit{
					AutoAuthorize: false,
					Limit:         LimitNode,
					Nodes:         AuthorizeNodeList.NodesSlice,
				}
				do := func(addrs string) error {
					var targetHost string
					addrSlice := strings.Split(addrs, ",")
					u, _ := url.Parse(addrSlice[0])
					if len(addrSlice) > 1 {
						targetHost = addrSlice[1]
					}
					httpclient.NewAppClient(httpclient.AppConfig{
						Insecure:   appConfig.Insecure,
						Identity:   appConfig.Identity,
						TargetHost: targetHost,
					}, *u).SetSlaveNodeLimit(&tmpNodeLimit)
					return nil
				}
				if signal == "all" {
					SlaveCollectors.Range(do)
				} else {
					do(signal)
				}
			}
		}()
	case "slave":
		proxyU, err := url.Parse(os.Getenv("CONTROL_PROXY_HOST"))
		proxyClient := httpclient.NewAppClient(httpclient.AppConfig{
			Insecure: appConfig.Insecure,
			Identity: appConfig.Identity,
		}, *proxyU)
		res, err := proxyClient.GetNodeLimitFromMaster()
		if err == nil {
			SetNodeLimit(res)
			log.Infof("Node limit: %d,Auth authorize:%v,Node list length:%v", LimitNode, AutoAuthorize, AuthorizeNodeList.Len())
		} else {
			log.Errorf("Error setting node limit: %v", err)
		}
	}
	return &Authorization{
		AppConfig: appConfig,
	}
}
func SetNodeLimit(limit *httpclient.NodeLimit) {
	LimitNode = limit.Limit
	AutoAuthorize = limit.AutoAuthorize
	AuthorizeNodeList.Init(limit.Nodes)
	SlaveCollectors.Init(limit.SlaveCollector)
	log.Info("Node limit response: ", LimitNode, AutoAuthorize, AuthorizeNodeList.Len())
}

const (
	LimitUnSet int64 = -2
	LimitZero  int64 = 0
	UnLimit    int64 = -1
)

type AuthorizeNode struct {
	Lock       sync.RWMutex
	Nodes      map[string]struct{}
	NodesSlice []string
}

func (l *AuthorizeNode) Exists(key string) bool {
	l.Lock.RLock()
	defer l.Lock.RUnlock()
	_, ok := l.Nodes[key]
	return ok
}
func (l *AuthorizeNode) Init(nodes []string) {
	l.Lock.Lock()
	defer l.Lock.Unlock()
	l.Nodes = make(map[string]struct{})
	l.NodesSlice = []string{}
	for _, node := range nodes {
		l.Nodes[node] = struct{}{}
		l.NodesSlice = append(l.NodesSlice, node)
	}
	if CollectorappIdentity == "master" {
		pushSlaveSignal <- "all"

	}
	return
}
func (l *AuthorizeNode) Set(key string) {
	l.Lock.Lock()
	defer l.Lock.Unlock()
	l.Nodes[key] = struct{}{}
	l.NodesSlice = append(l.NodesSlice, key)
	if CollectorappIdentity == "master" {
		pushSlaveSignal <- "all"

	}
	return
}
func (l *AuthorizeNode) Del() {
	l.Lock.Lock()
	defer l.Lock.Unlock()
	randIndex := rand.Intn(len(l.NodesSlice))
	delete(l.Nodes, l.NodesSlice[randIndex])
	l.NodesSlice = append(l.NodesSlice[:randIndex], l.NodesSlice[randIndex+1:]...)
	if CollectorappIdentity == "master" {
		pushSlaveSignal <- "all"
	}
	return
}
func (l *AuthorizeNode) Len() (length int) {
	l.Lock.RLock()
	defer l.Lock.RUnlock()
	return len(l.Nodes)
}
