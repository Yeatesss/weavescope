package cri

import (
	"context"
	"github.com/coocood/freecache"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/weaveworks/scope/report"
	"net"
)

type Container[T any] interface {
	UpdateState(T)
	NsCtx() context.Context

	ID() string
	Image() string
	PID() int
	Hostname() string
	GetNode() report.Node
	State() string
	StateString() string
	HasTTY() bool
	Container() T
	StartGatheringStats(StatsGatherer) error
	StopGatheringStats()
	NetworkMode() (string, bool)
	NetworkInfo([]net.IP) report.Sets
}

// StatsGatherer gathers container stats
type StatsGatherer interface {
	Stats(docker.StatsOptions) error
}

type PortBinding struct {
	HostIP    string
	HostPort  string
	Protocols string
}

type NetworkSet struct {
	Name       string `json:"name"`
	Gateway    string `json:"gateway"`
	Mode       string `json:"mode"`
	IPv6       string `json:"ipv6"`
	IPv4       string `json:"ipv4"`
	NetworkID  string `json:"network_id"`
	EndpointID string `json:"endpoint_id"`
	Mac        string `json:"mac"`
}

var ExecCache = freecache.NewCache(1024 * 1024)
var UserCache = freecache.NewCache(1024 * 1024)
var BindPorts = freecache.NewCache(1024 * 64)
var NsPids = freecache.NewCache(1024 * 64)
