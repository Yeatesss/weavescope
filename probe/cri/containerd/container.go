package containerd

import (
	"context"
	"fmt"
	"github.com/containerd/containerd/namespaces"
	docker "github.com/fsouza/go-dockerclient"
	jsoniter "github.com/json-iterator/go"
	log "github.com/sirupsen/logrus"
	"github.com/weaveworks/common/mtime"
	"github.com/weaveworks/scope/probe/cri"
	"github.com/weaveworks/scope/report"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
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
	container              *docker.Container
	stopStats              chan<- bool
	latestStats            docker.Stats
	pendingStats           [60]docker.Stats
	numPending             int
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
	c.Lock()
	defer c.Unlock()

	if c.stopStats != nil {
		return nil
	}
	done := make(chan bool)
	c.stopStats = done

	stats := make(chan *docker.Stats)
	opts := docker.StatsOptions{
		ID:     c.container.ID,
		Stats:  stats,
		Stream: true,
		Done:   done,
	}

	log.Debugf("docker container: collecting stats for %s", c.container.ID)

	go func() {
		if err := client.Stats(opts); err != nil && err != io.EOF && err != io.ErrClosedPipe {

			//if err := client.GetContainerStat(client, opts); err != nil && err != io.EOF && err != io.ErrClosedPipe {
			log.Errorf("docker container: error collecting stats for %s: %v", c.container.ID, err)
		}
	}()

	go func() {
		for s := range stats {
			c.Lock()
			if c.numPending >= len(c.pendingStats) {
				log.Warnf("docker container: dropping stats for %s", c.container.ID)
			} else {
				c.latestStats = *s
				c.pendingStats[c.numPending] = *s
				c.numPending++
			}
			c.Unlock()
		}
		log.Debugf("docker container: stopped collecting stats for %s", c.container.ID)
		c.Lock()
		if c.stopStats == done {
			c.stopStats = nil
		}
		c.Unlock()
	}()

	return nil
}

//func StartGatheringStats(c *containerd.Client, opts StatsOptions) error {
//	if opts.Stats == nil {
//		return errors.New("can not send on nil channel")
//	}
//
//	var mutex sync.Mutex
//
//	defer func() {
//		mutex.Lock()
//		if opts.Stats != nil {
//			close(opts.Stats)
//			opts.Stats = nil
//		}
//		mutex.Unlock()
//	}()
//
//	quit := make(chan struct{})
//	defer close(quit)
//	go func() {
//		select {
//		case <-opts.Done:
//			mutex.Lock()
//			if opts.Stats != nil {
//				close(opts.Stats)
//				opts.Stats = nil
//			}
//			mutex.Unlock()
//		case <-quit:
//			return
//		}
//
//	}()
//
//	for {
//		RangeNs(c, func(ns string) error {
//			nsCtx := namespaces.WithNamespace(context.Background(), ns)
//			m, err := c.TaskService().Metrics(nsCtx, &tasks.MetricsRequest{
//				Filters: []string{
//					"id==" + opts.ID,
//				},
//			})
//			if err != nil {
//				return errors.New("gRPC error")
//			}
//			if m.Metrics == nil {
//				return errors.New("no metrics received")
//			}
//
//			metric := m.Metrics[0]
//			var data interface{}
//			stats := new(Stats)
//			stats.Read = time.Now()
//			switch {
//			case typeurl.Is(metric.Data, (*v1.Metrics)(nil)):
//				data = &v1.Metrics{}
//			case typeurl.Is(metric.Data, (*v2.Metrics)(nil)):
//				data = &v2.Metrics{}
//			case typeurl.Is(metric.Data, (*wstats.Statistics)(nil)):
//				data = &wstats.Statistics{}
//			}
//			if err := typeurl.UnmarshalTo(metric.Data, data); err != nil {
//				return err
//			}
//			switch v := data.(type) {
//			case *v1.Metrics:
//				stats.MemoryStats.Stats.Cache = v.Memory.Cache
//				stats.MemoryStats.Usage = v.Memory.Usage.Usage
//				stats.MemoryStats.Limit = v.Memory.Usage.Limit
//				stats.CPUStats.CPUUsage.TotalUsage = v.CPU.Usage.Total
//				stats.CPUStats.SystemCPUUsage = v.CPU.Usage.Kernel
//			case *v2.Metrics:
//				// no cache field in v2 stats
//				// according to https://github.com/containerd/nerdctl/blob/main/pkg/statsutil/stats_linux.go, cache might be related with v.Memory.InactiveFile
//				// an alternative scheme should be like "stats.MemoryStats.Stats.Cache = v.Memory.InactiveFile"
//				stats.MemoryStats.Stats.Cache = 0
//				stats.MemoryStats.Usage = v.Memory.Usage
//				stats.MemoryStats.Limit = v.Memory.UsageLimit
//				stats.CPUStats.CPUUsage.TotalUsage = v.CPU.UsageUsec
//				stats.CPUStats.SystemCPUUsage = v.CPU.SystemUsec
//			case *wstats.Statistics:
//				v1m := v.GetLinux()
//				stats.MemoryStats.Stats.Cache = v1m.Memory.Cache
//				stats.MemoryStats.Usage = v1m.Memory.Usage.Usage
//				stats.MemoryStats.Limit = v1m.Memory.Usage.Limit
//				stats.CPUStats.CPUUsage.TotalUsage = v1m.CPU.Usage.Total
//				stats.CPUStats.SystemCPUUsage = v1m.CPU.Usage.Kernel
//			}
//			mutex.Lock()
//			if opts.Stats != nil {
//				opts.Stats <- stats
//			} else {
//				mutex.Unlock()
//				return nil
//			}
//			mutex.Unlock()
//		})
//
//		time.Sleep(1 * time.Second)
//	}
//}

func (c *container) StopGatheringStats() {
	c.Lock()
	defer c.Unlock()
	if c.stopStats != nil {
		close(c.stopStats)
		c.stopStats = nil
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

func (c *container) memoryUsageMetric(stats []docker.Stats) report.Metric {
	var max float64
	samples := make([]report.Sample, len(stats))
	for i, s := range stats {
		samples[i].Timestamp = s.Read
		// This code adapted from
		// https://github.com/docker/cli/blob/5931fb4276be0afdd6e5ed338d1b2b4b9b5ec8e5/cli/command/container/stats_helpers.go
		// so that Scope numbers match Docker numbers. Page cache is intentionally excluded.
		samples[i].Value = float64(s.MemoryStats.Usage - s.MemoryStats.Stats.Cache)
		if float64(s.MemoryStats.Limit) > max {
			max = float64(s.MemoryStats.Limit)
		}
	}
	return report.MakeMetric(samples).WithMax(max)
}

func (c *container) cpuPercentMetric(stats []docker.Stats) report.Metric {
	if len(stats) < 2 {
		return report.MakeMetric(nil)
	}

	samples := make([]report.Sample, len(stats)-1)
	previous := stats[0]
	for i, s := range stats[1:] {
		// Copies from docker/api/client/stats.go#L205
		cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage - previous.CPUStats.CPUUsage.TotalUsage)
		systemDelta := float64(s.CPUStats.SystemCPUUsage - previous.CPUStats.SystemCPUUsage)
		cpuPercent := 0.0
		if systemDelta > 0.0 && cpuDelta > 0.0 {
			cpuPercent = (cpuDelta / systemDelta) * 100.0
		}
		samples[i].Timestamp = s.Read
		samples[i].Value = cpuPercent
		previous = s
	}
	return report.MakeMetric(samples).WithMax(100.0)
}

func (c *container) metrics() report.Metrics {
	if c.numPending == 0 {
		return report.Metrics{}
	}
	pendingStats := c.pendingStats[:c.numPending]
	result := report.Metrics{
		MemoryUsage:   c.memoryUsageMetric(pendingStats),
		CPUTotalUsage: c.cpuPercentMetric(pendingStats),
	}

	// leave one stat to help with relative metrics
	c.pendingStats[0] = c.pendingStats[c.numPending-1]
	c.numPending = 1
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

	networkName := c.container.NetworkSettings.Bridge
	for mode, network := range c.container.NetworkSettings.Networks {
		if network.NetworkID != "" || network.EndpointID != "" {
			networkStr, _ := jsoniter.MarshalToString(cri.NetworkSet{
				Name:       networkName,
				Gateway:    network.Gateway,
				Mode:       mode,
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
		ContainerStateHuman: c.State(),
	}
	//	c.container.HostConfig.Privileged
	if !c.container.State.Paused && c.container.State.Running {
		uptimeSeconds := int(mtime.Now().Sub(c.container.State.StartedAt) / time.Second)
		networkMode := ""
		if c.container.HostConfig != nil {
			networkMode = c.container.HostConfig.NetworkMode
		}
		latest[ContainerUptime] = strconv.Itoa(uptimeSeconds)
		latest[ContainerRestartCount] = strconv.Itoa(c.container.RestartCount)
		latest[ContainerNetworkMode] = networkMode

	}

	result := c.baseNode.WithLatests(latest)
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
	parts := strings.SplitN(imageName, "/", 3)
	if len(parts) == 3 {
		imageName = fmt.Sprintf("%s/%s", parts[1], parts[2])
	}
	return strings.SplitN(imageName, ":", 2)
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
