package containerd

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/nerdctl/pkg/statsutil"

	"github.com/containerd/containerd/namespaces"
	docker "github.com/fsouza/go-dockerclient"
	jsoniter "github.com/json-iterator/go"
	"github.com/weaveworks/common/mtime"
	"github.com/weaveworks/scope/probe/cri"
	"github.com/weaveworks/scope/report"
)

// These constants are keys used in node metadata
const (
	ContainerPrivileged    = report.DockerPrivileged
	ContainerName          = report.DockerContainerName
	ContainerCommand       = report.DockerContainerCommand
	ContainerPorts         = report.DockerContainerPorts
	ContainerCreated       = report.DockerContainerCreated
	ContainerNetworks      = report.DockerContainerNetworks
	ContainerIPs           = report.DockerContainerIPs
	ContainerHostname      = report.DockerContainerHostname
	ContainerIPsWithScopes = report.DockerContainerIPsWithScopes
	ContainerState         = report.DockerContainerState
	ContainerStateHuman    = report.DockerContainerStateHuman
	ContainerUptime        = report.DockerContainerUptime
	ContainerRestartCount  = report.DockerContainerRestartCount
	ContainerNetworkMode   = report.DockerContainerNetworkMode
	ContainerID            = report.DockerContainerID
	Name                   = report.Name
	MemoryUsage            = "docker_memory_usage"
	CPUTotalUsage          = "docker_cpu_total_usage"

	LabelPrefix      = report.DockerLabelPrefix
	EnvPrefix        = report.DockerEnvPrefix
	ImageID          = report.DockerImageID
	ImageName        = report.DockerImageName
	ImageNameReal    = report.DockerImageNameReal
	ImageTag         = report.DockerImageTag
	ImageSize        = report.DockerImageSize
	ImageVirtualSize = report.DockerImageVirtualSize
	ImageCreatedAt   = report.DockerImageCreatedAt
	IsInHostNetwork  = report.DockerIsInHostNetwork
	ImageLabelPrefix = report.DockerImageLabelPrefix
	ContainerEnvPath = report.ContainerEnvPath
	ClusterUUID      = report.DockerClusterUUID
	ImageTableID     = "image_table"
	ServiceName      = report.DockerServiceName
	StackNamespace   = report.DockerStackNamespace
	DefaultNamespace = report.DockerDefaultNamespace
)

var _ cri.Container[*docker.Container] = &container{}

type container struct {
	sync.RWMutex
	container   *docker.Container
	cancelStats context.CancelFunc
	latestStats statsutil.StatsEntry
	//pendingStats           [60]docker.Stats
	//numPending             int
	hostID                 string
	baseNode               report.Node
	noCommandLineArguments bool
	noEnvironmentVariables bool
}

func (c *container) NsCtx() context.Context {
	ns := getNsFromContainer(*c.container)
	if ns != "" {
		return namespaces.WithNamespace(context.Background(), ns)
	}
	return context.Background()
}

// NewContainer creates a new Container
func NewContainer(c *docker.Container, hostID string, noCommandLineArguments bool, noEnvironmentVariables bool) cri.Container[*docker.Container] {
	result := &container{
		container:              c,
		hostID:                 hostID,
		noCommandLineArguments: noCommandLineArguments,
		noEnvironmentVariables: noEnvironmentVariables,
	}
	result.baseNode = result.getBaseNode()
	return result
}
func (c *container) UpdateState(container *docker.Container) {
	c.Lock()
	defer c.Unlock()
	c.container = container
}

func (c *container) ID() string {
	return c.container.ID
}

func (c *container) Image() string {
	return c.container.Image
}

func (c *container) PID() int {
	return c.container.State.Pid
}

func (c *container) Hostname() string {
	if c.container.Config.Domainname == "" {
		return c.container.Config.Hostname
	}

	return fmt.Sprintf("%s.%s", c.container.Config.Hostname,
		c.container.Config.Domainname)
}

func (c *container) HasTTY() bool {
	return c.container.Config.Tty
}

func (c *container) State() string {
	return c.container.State.String()
}

func (c *container) StateString() string {
	return c.container.State.StateString()
}

func (c *container) Container() *docker.Container {
	return c.container
}
func (c *container) StartGatheringStats(client cri.StatsGatherer) error {
	//TODO 测验容器负载收集
	c.Lock()
	defer c.Unlock()

	if c.cancelStats != nil {
		return nil
	}
	var (
		ctx       context.Context
		statsChan = make(chan *AllStats, 1)
	)
	ctx, c.cancelStats = context.WithCancel(context.Background())
	CtrStatsLister.nsChan <- &NsChan{
		Namespace: getNsFromContainer(*c.container),
		Stats:     statsChan,
	}
	allStats := <-statsChan

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				task := NewGetCtrStats(c.container.ID)
				allStats.PushTask(task)
				stat := <-task.StatsRes
				//fmt.Println(stat)
				c.Lock()
				c.latestStats = stat
			}
			c.Unlock()
			time.Sleep(2 * time.Second)
		}

	}()

	return nil
}

func (c *container) StopGatheringStats() {
	c.Lock()
	defer c.Unlock()
	if c.cancelStats != nil {
		c.cancelStats()
	}
}

func (c *container) ports(localAddrs []net.IP) report.StringSet {
	if c.container.NetworkSettings == nil {
		return report.MakeStringSet()
	}

	var ports []string
	for port, bindings := range c.container.NetworkSettings.Ports {
		if len(bindings) == 0 {
			ports = append(ports, fmt.Sprintf("%s", port))
			continue
		}
		for _, b := range bindings {
			if b.HostIP != "0.0.0.0" {
				ports = append(ports, fmt.Sprintf("%s:%s->%s", b.HostIP, b.HostPort, port))
				continue
			}

			for _, ip := range localAddrs {
				if ip.To4() != nil {
					ports = append(ports, fmt.Sprintf("%s:%s->%s", ip, b.HostPort, port))
				}
			}
		}
	}

	return report.MakeStringSet(ports...)
}

func (c *container) NetworkMode() (string, bool) {
	c.RLock()
	defer c.RUnlock()
	if c.container.HostConfig != nil {
		return c.container.HostConfig.NetworkMode, true
	}
	return "", false
}

func addScopeToIPs(hostID string, ips []net.IP) []string {
	var ipsWithScopes []string
	for _, ip := range ips {
		ipsWithScopes = append(ipsWithScopes, report.MakeAddressNodeIDB(hostID, ip))
	}
	return ipsWithScopes
}

func (c *container) NetworkInfo(localAddrs []net.IP) report.Sets {
	c.RLock()
	defer c.RUnlock()

	ips := c.container.NetworkSettings.SecondaryIPAddresses
	if c.container.NetworkSettings.IPAddress != "" {
		ips = append(ips, c.container.NetworkSettings.IPAddress)
	}

	if c.container.State.Running && c.container.State.Pid != 0 {
		// Fetch IP addresses from the container's namespace
		//cidrs, err := namespaceIPAddresses(c.container.State.Pid)
		//if err != nil {
		//	log.Debugf("container %s: failed to get addresses: %s", c.container.ID, err)
		//}
		//for _, cidr := range cidrs {
		//	// This address can duplicate an address fetched from Docker earlier,
		//	// but we eventually turn the lists into sets which will remove duplicates.
		//	ips = append(ips, cidr.IP.String())
		//}
	}

	// For now, for the proof-of-concept, we just add networks as a set of
	// names. For the next iteration, we will probably want to create a new
	// Network topology, populate the network nodes with all of the details
	// here, and provide foreign key links from nodes to networks.
	networks := make([]string, 0, len(c.container.NetworkSettings.Networks))
	for name, settings := range c.container.NetworkSettings.Networks {
		if name == "none" {
			continue
		}
		networks = append(networks, name)
		if settings.IPAddress != "" {
			ips = append(ips, settings.IPAddress)
		}
	}

	// Filter out IPv6 addresses; nothing works with IPv6 yet
	var ipv4s []string
	var ipv4ips []net.IP
	for _, ip := range ips {
		ipaddr := net.ParseIP(ip)
		if ipaddr != nil && ipaddr.To4() != nil {
			ipv4s = append(ipv4s, ip)
			ipv4ips = append(ipv4ips, ipaddr)
		}
	}
	// Treat all Docker IPs as local scoped.
	ipsWithScopes := addScopeToIPs(c.hostID, ipv4ips)

	s := report.MakeSets()
	if len(networks) > 0 {
		s = s.Add(ContainerNetworks, report.MakeStringSet(networks...))
	}
	if len(c.container.NetworkSettings.Ports) > 0 {
		s = s.Add(ContainerPorts, c.ports(localAddrs))
	}
	if len(ipv4s) > 0 {
		s = s.Add(ContainerIPs, report.MakeStringSet(ipv4s...))
	}
	if len(ipsWithScopes) > 0 {
		s = s.Add(ContainerIPsWithScopes, report.MakeStringSet(ipsWithScopes...))
	}
	return s
}

func (c *container) memoryUsageMetric(stat statsutil.StatsEntry) report.Metric {
	var (
		max     float64
		samples = make([]report.Sample, 1)
	)

	samples[0].Timestamp = time.Now()
	samples[0].Value = stat.Memory
	if stat.MemoryLimit > max {
		max = stat.MemoryLimit
	}
	//fmt.Println("mem:  ", stat.MemUsage())
	return report.MakeMetric(samples).WithMax(max)
}

func (c *container) cpuPercentMetric(stat statsutil.StatsEntry) report.Metric {
	var (
		samples = make([]report.Sample, 1)
	)
	samples[0].Timestamp = time.Now()
	samples[0].Value = stat.CPUPercentage
	//fmt.Println("cpu:  ", stat.CPUPerc())
	return report.MakeMetric(samples).WithMax(100.0)
}

func (c *container) metrics() report.Metrics {
	var result report.Metrics

	result = report.Metrics{
		MemoryUsage:   c.memoryUsageMetric(c.latestStats),
		CPUTotalUsage: c.cpuPercentMetric(c.latestStats),
	}
	//fmt.Println(c.container.ID, result)

	// leave one stat to help with relative metrics
	return result
}

func (c *container) env() map[string]string {
	result := map[string]string{}
	for _, value := range c.container.Config.Env {
		v := strings.SplitN(value, "=", 2)
		if len(v) != 2 {
			continue
		}
		result[v[0]] = v[1]
	}
	return result
}

func (c *container) getSanitizedCommand() string {
	result := c.container.Path
	if !c.noCommandLineArguments {
		result = result + " " + strings.Join(c.container.Args, " ")
	}
	return result
}

func (c *container) getBaseNode() report.Node {
	result := report.MakeNodeWith(report.MakeContainerNodeID(c.ID()), map[string]string{
		ContainerID:       c.ID(),
		ContainerCreated:  c.container.Created.Format(time.RFC3339Nano),
		ContainerCommand:  c.getSanitizedCommand(),
		ImageID:           c.Image(),
		ContainerHostname: c.Hostname(),
	}).WithParent(report.ContainerImage, report.MakeContainerImageNodeID(c.Image()))
	if len(c.container.Mounts) > 0 {
		result = result.WithSets(c.makeMountSet())
	}
	for _, env := range c.container.Config.Env {
		if strings.Contains(env, "PATH=") {
			result = result.WithLatest(ContainerEnvPath, mtime.Now(), env)
			break
		}
	}
	if len(c.container.NetworkSettings.Networks) > 0 {
		result.Sets = result.Sets.Delete(report.Network)
		result = result.WithSets(c.makeNetworkSet())
	}
	result = result.AddPrefixPropertyList(LabelPrefix, c.container.Config.Labels)
	if !c.noEnvironmentVariables {
		result = result.AddPrefixPropertyList(EnvPrefix, c.env())
	}
	return result
}

func (c *container) makeMountSet() report.Sets {
	var mountSlice []string
	for _, mount := range c.container.Mounts {
		mountStr, _ := jsoniter.MarshalToString(docker.Mount{
			Name:        mount.Name,
			Type:        mount.Type,
			Source:      mount.Source,
			Driver:      mount.Driver,
			Destination: mount.Destination,
			Mode:        mount.Mode,
			RW:          mount.RW,
			Propagation: mount.Propagation,
		})
		mountSlice = append(mountSlice, mountStr)
	}
	return report.MakeSets().Add(report.Mount, report.MakeStringSet(mountSlice...))

}

func (c *container) makeNetworkSet() report.Sets {
	var networkSlice []string
	//networkName := c.container.NetworkSettings.Bridge
	for mode, network := range c.container.NetworkSettings.Networks {
		if network.IPAddress != "" || network.MacAddress != "" {
			networkStr, _ := jsoniter.MarshalToString(cri.NetworkSet{
				Name:       mode,
				Gateway:    network.Gateway,
				Mode:       networkDrives[mode],
				IPv6:       network.GlobalIPv6Address,
				IPv4:       network.IPAddress,
				NetworkID:  network.NetworkID,
				EndpointID: network.EndpointID,
				Mac:        network.MacAddress,
			})

			networkSlice = append(networkSlice, networkStr)
		}
	}

	return report.MakeSets().Add(report.Network, report.MakeStringSet(networkSlice...))

}

// Return a slice including all controls that should be shown on this container
func (c *container) controls() []string {
	switch {
	case c.container.State.Paused:
		return []string{UnpauseContainer}
	case c.container.State.Running:
		return []string{RestartContainer, StopContainer, PauseContainer, AttachContainer, ExecContainer}
	default:
		return []string{StartContainer, RemoveContainer}
	}
}

var boolToString = map[bool]string{true: "1", false: "0"}

func (c *container) GetNode() report.Node {
	c.RLock()
	defer c.RUnlock()
	latest := map[string]string{
		ContainerPrivileged: boolToString[c.container.HostConfig.Privileged],
		ContainerName:       strings.TrimPrefix(c.container.Name, "/"),
		ContainerState:      c.StateString(),
		ContainerStateHuman: c.StateString(),
	}
	//	c.container.HostConfig.Privileged
	if !c.container.State.Paused && c.container.State.Running {
		//uptimeSeconds := int(mtime.Now().Sub(c.container.State.StartedAt) / time.Second)
		networkMode := ""
		if c.container.HostConfig != nil {
			networkMode = c.container.HostConfig.NetworkMode
		}
		//latest[ContainerUptime] = strconv.Itoa(uptimeSeconds)
		latest[ContainerRestartCount] = strconv.Itoa(c.container.RestartCount)
		latest[ContainerNetworkMode] = networkMode

	}

	result := c.baseNode.WithLatests(latest)
	result.Sets = result.Sets.Delete(report.Network)
	result = result.WithSets(c.makeNetworkSet())
	result = result.WithLatestActiveControls(c.controls()...)
	result = result.WithMetrics(c.metrics())
	return result
}

// ExtractContainerIPs returns the list of container IPs given a Node from the Container topology.
func ExtractContainerIPs(nmd report.Node) []string {
	v, _ := nmd.Sets.Lookup(ContainerIPs)
	return []string(v)
}

// ExtractContainerIPsWithScopes returns the list of container IPs, prepended
// with scopes, given a Node from the Container topology.
func ExtractContainerIPsWithScopes(nmd report.Node) []string {
	v, _ := nmd.Sets.Lookup(ContainerIPsWithScopes)
	return v
}

// ContainerIsStopped checks if the docker container is in one of our "stopped" states
func ContainerIsStopped(c cri.Container[*docker.Container]) bool {
	state := c.StateString()
	return state != report.StateRunning && state != report.StateRestarting && state != report.StatePaused
}

// splitImageName returns parts of the full image name (image name, image tag).
func splitImageName(imageName string) []string {
	lastIdx := strings.LastIndex(imageName, ":")
	return []string{imageName[:lastIdx], imageName[lastIdx+1:]}
}

// ImageNameWithoutTag splits the image name apart, returning the name
// without the version, if possible
func ImageNameWithoutTag(imageName string) string {
	return splitImageName(imageName)[0]
}

// ImageNameTag splits the image name apart, returning the version tag, if possible
func ImageNameTag(imageName string) string {
	imageNameParts := splitImageName(imageName)
	if len(imageNameParts) < 2 {
		return ""
	}
	return imageNameParts[1]
}
