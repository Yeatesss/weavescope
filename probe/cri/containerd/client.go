package containerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/containerd/containerd/snapshots"
	"strings"
	"sync"
	"time"

	"github.com/weaveworks/scope/common/logger"

	"github.com/containerd/containerd/platforms"

	"github.com/containerd/containerd/api/types"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/protobuf"

	cimages "github.com/containerd/containerd/images"
	"github.com/containerd/nerdctl/pkg/imgutil"

	images2 "github.com/containerd/containerd/api/services/images/v1"

	wstats "github.com/Microsoft/hcsshim/cmd/containerd-shim-runhcs-v1/stats"
	v1 "github.com/containerd/cgroups/stats/v1"
	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/nerdctl/pkg/containerinspector"
	"github.com/containerd/nerdctl/pkg/formatter"
	"github.com/containerd/nerdctl/pkg/inspecttypes/dockercompat"
	"github.com/containerd/nerdctl/pkg/inspecttypes/native"
	"github.com/containerd/nerdctl/pkg/labels"
	"github.com/containerd/nerdctl/pkg/netutil"
	"github.com/containerd/nerdctl/pkg/portutil"
	"github.com/containerd/typeurl"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/weaveworks/scope/probe/cri"

	"log"
)

var _ cri.Client = &CriClient{}

const (
	privilegedCAP1 = "CAP_SYS_ADMIN"
	privilegedCAP2 = "CAP_SYS_RAWIO"

	CNIPath        = "/opt/cni/bin"
	CNINetConfPath = "/etc/cni/net.d"

	LabelNamespace = "containerd/namespaces"

	LabelPodNamespace  = "io.kubernetes.pod.namespace"
	LabelPodName       = "io.kubernetes.pod.name"
	LabelContainerName = "io.kubernetes.container.name"
)

var (
	ErrContainerNotFound = errors.New("container not found")
)

type CriClient struct {
	client         *containerd.Client
	snapshotClient snapshots.Snapshotter
	imageSize      sync.Map
}

func NewCriClient(endpoint string) *CriClient {
	if endpoint == "" {
		endpoint = "/var/run/containerd/containerd.sock"
	}
	client, err := containerd.New(endpoint)
	if err != nil {
		log.Fatal(err)
	}
	CtrStatsLister = NewCtrStats(client)
	return &CriClient{client: client, snapshotClient: client.SnapshotService("overlayfs"), imageSize: sync.Map{}}

}

// RangeNs executes the provided function for each namespace in the CriClient.
//
// The function takes a single parameter 'ns' of type string, representing the namespace.
// It returns an error if there is an issue executing the provided function for any of the namespaces.
// The function itself returns an error if there is an issue retrieving the list of namespaces.
//
// Returns: an error if there is any issue executing the provided function or retrieving the list of namespaces.
func (c *CriClient) RangeNs(f func(ns string) (next bool, e error)) (err error) {
	nses, err := c.client.NamespaceService().List(context.Background())
	if err != nil {
		return err
	}
	for _, ns := range nses {
		var next bool
		next, err = f(ns)
		if err != nil {
			return err
		}
		if !next {
			return
		}
	}
	return nil
}
func (c *CriClient) ListContainers(options docker.ListContainersOptions) ([]docker.APIContainers, error) {
	var result []docker.APIContainers
	err := RangeNs(c.client, func(ns string) error {
		nsCtx := namespaces.WithNamespace(context.Background(), ns)
		apiContainers, err := c.client.Containers(nsCtx)
		if err != nil {
			return err
		}

		for _, apiContainer := range apiContainers {
			var tmpCtr docker.APIContainers
			// ID
			tmpCtr.ID = apiContainer.ID()
			// Image sha256 digest
			tmpImg, imgerr := apiContainer.Image(nsCtx)
			if imgerr != nil {
				continue
			}

			tmpCtr.Image = tmpImg.Target().Digest.Hex()

			// spec data
			spec, err := apiContainer.Spec(nsCtx)
			if err != nil {
				return err
			}

			tmpCtr.Command = formatter.InspectContainerCommand(spec, false, true)
			//tmpCtr.Created = apiContainer.Spec().CreatedAt.Unix()
			tmpCtr.Status = formatter.ContainerStatus(nsCtx, apiContainer)
			// Ports
			tmpPorts, _ := apiContainer.Labels(nsCtx)
			ports, err := portutil.ParsePortsLabel(tmpPorts)
			if err != nil {
				return err
			}
			for _, port := range ports {
				var apiPort docker.APIPort
				apiPort.IP = port.HostIP
				apiPort.PublicPort = int64(port.HostPort)
				apiPort.PrivatePort = int64(port.ContainerPort)
				apiPort.Type = port.Protocol
				tmpCtr.Ports = append(tmpCtr.Ports, apiPort)
			}
			// fields not used
			// tmpCtr.SizeRootFs
			ctrLabels, _ := apiContainer.Labels(nsCtx)
			tmpCtr.Names = []string{func(containerLabels map[string]string) string {
				if name, ok := containerLabels[labels.Name]; ok {
					return name
				}
				if name, ok := containerLabels["io.kubernetes.container.name"]; ok {
					return name
				}
				return ""
			}(ctrLabels)}
			tmpCtr.Labels = ctrLabels
			tmpCtr.Labels[LabelNamespace] = ns
			// This 2 fields will not be used
			// tmpCtr.Networks
			// tmpCtr.Mounts
			result = append(result, tmpCtr)
		}
		return nil
	})
	return result, err
}

func (c *CriClient) InspectContainerWithContext(containerID string, ctx context.Context) (*docker.Container, error) {
	var (
		nativeCtr *native.Container
	)
	defer func() {
		if e := recover(); e != nil {
			log.Println(e)
		}
	}()
	ctr, err := c.client.LoadContainer(ctx, containerID)
	if errdefs.IsNotFound(err) {
		return nil, ErrContainerNotFound
	}

	if err != nil {
		return nil, err
	}
	nativeCtr, err = containerinspector.Inspect(ctx, ctr)
	if err != nil {
		return nil, err
	}
	image, err := ctr.Image(ctx)
	if err != nil {
		return nil, err
	}
	nativeCtr.Image = image.Target().Digest.Hex()
	container, err := c.nativeCtrToContainer(ctx, ctr, nativeCtr)
	if err != nil {
		return nil, err
	}
	spec, err := ctr.Spec(ctx)

	if err != nil {
		return nil, err
	}
	ns, _ := namespaces.Namespace(ctx)
	container.Config.Domainname = spec.Domainname
	container.HostConfig = new(docker.HostConfig)
	container.Config.Labels[LabelNamespace] = ns
	container.HostConfig.Privileged = checkPrivileged(spec.Process.Capabilities.Ambient) || checkPrivileged(spec.Process.Capabilities.Bounding) || checkPrivileged(spec.Process.Capabilities.Effective) || checkPrivileged(spec.Process.Capabilities.Inheritable) || checkPrivileged(spec.Process.Capabilities.Permitted)
	return container, nil
}

func (c *CriClient) ListImages(options docker.ListImagesOptions) (res []docker.APIImages, err error) {
	var imageSize = make(map[string]int64)
	err = c.RangeNs(func(ns string) (bool, error) {
		nsCtx := namespaces.WithNamespace(context.Background(), ns)
		images, err := images2.NewImagesClient(c.client.Conn()).List(nsCtx, &images2.ListImagesRequest{})
		if err != nil {
			return false, err
		}
		for _, tmpImage := range images.Images {
			var size int64
			image := cimages.Image{
				Name:      strings.ReplaceAll(tmpImage.Name, "docker.io/library/", ""),
				Labels:    tmpImage.Labels,
				Target:    descFromProto(tmpImage.Target),
				CreatedAt: protobuf.FromTimestamp(tmpImage.CreatedAt),
				UpdatedAt: protobuf.FromTimestamp(tmpImage.UpdatedAt),
			}
			if image.Labels == nil {
				image.Labels = make(map[string]string)
				image.Labels[LabelNamespace] = ns
			}
			ociPlatforms, err := cimages.Platforms(nsCtx, c.client.ContentStore(), image.Target)
			if err != nil {
				logger.Logger.Error("get oci platforms failed", "name", image.Name)
				continue
			}
			//logger.Logger.Debug("image:", image)
			//logger.Logger.Debug("ociPlatforms:", ociPlatforms)
			ociPlatform := ociPlatforms[0]
			for _, platform := range ociPlatforms {
				if platform.Architecture == "amd64" {
					ociPlatform = platform
					break
				}
			}
			if tmpSize, ok := c.imageSize.Load(image.Target.Digest.String()); ok {
				size = tmpSize.(int64)
			} else {
				size, err = imgutil.UnpackedImageSize(nsCtx, c.snapshotClient, containerd.NewImageWithPlatform(c.client, image, platforms.OnlyStrict(ociPlatform)))
				if err != nil {
					logger.Logger.Error("get image size failed", "name", image.Name, "d:", err)
					continue
				}
			}
			imageSize[image.Target.Digest.String()] = size
			res = append(res, docker.APIImages{
				ID:          image.Target.Digest.String(),
				RepoTags:    []string{image.Name},
				Created:     time.Now().Local().Unix() - image.CreatedAt.Round(time.Second).Local().Unix(),
				Size:        size,
				VirtualSize: size,
				Labels:      image.Labels,
			})
		}
		return true, nil
	})
	c.imageSize = sync.Map{}
	for key, value := range imageSize {
		c.imageSize.Store(key, value)
	}
	return
}

func (c *CriClient) ListNetworks() ([]docker.Network, error) {
	e, err := netutil.NewCNIEnv(CNIPath, CNINetConfPath)
	if err != nil {
		return nil, err
	}
	netConfigs, err := e.NetworkList()
	if err != nil {
		return nil, err
	}
	var result []docker.Network
	for _, netConfig := range netConfigs {
		r := &native.Network{
			CNI:           json.RawMessage(netConfig.Bytes),
			NerdctlID:     netConfig.NerdctlID,
			NerdctlLabels: netConfig.NerdctlLabels,
			File:          netConfig.File,
		}
		compat, err := dockercompat.NetworkFromNative(r)
		if err != nil {
			return nil, err
		}
		var network = docker.Network{
			Containers: make(map[string]docker.Endpoint),
			Options:    make(map[string]string),
			Labels:     make(map[string]string),
		}
		network.Name = compat.Name
		network.ID = compat.ID
		for k, v := range compat.Labels {
			network.Labels[k] = v
		}
		for _, v := range compat.IPAM.Config {
			var i docker.IPAMConfig
			i.Gateway = v.Gateway
			i.IPRange = v.IPRange
			i.Subnet = v.Subnet
			network.IPAM.Config = append(network.IPAM.Config, i)
		}
		result = append(result, network)
	}
	return result, nil
}

func (c *CriClient) AddEventListener(events chan<- *docker.APIEvents) error {
	//无需实现
	panic("implement me")
}

func (c *CriClient) RemoveEventListener(events chan *docker.APIEvents) error {
	//无需实现
	panic("implement me")
}

func (c *CriClient) StopContainer(s string, u uint) error {
	//TODO implement me
	panic("implement me")
}

func (c *CriClient) StartContainer(s string, config *docker.HostConfig) error {
	//TODO implement me
	panic("implement me")
}

func (c *CriClient) RestartContainer(s string, u uint) error {
	//TODO implement me
	panic("implement me")
}

func (c *CriClient) PauseContainer(s string) error {
	//TODO implement me
	panic("implement me")
}

func (c *CriClient) UnpauseContainer(s string) error {
	//TODO implement me
	panic("implement me")
}

func (c *CriClient) RemoveContainer(options docker.RemoveContainerOptions) error {
	//TODO implement me
	panic("implement me")
}

func (c *CriClient) Stats(options docker.StatsOptions) error {
	//TODO implement me
	panic("implement me")
}

func (c *CriClient) nativeCtrToContainer(ctx context.Context, oriCtr containerd.Container, nativeContainer *native.Container) (*docker.Container, error) {
	dockercompatContainer, err := dockercompat.ContainerFromNative(nativeContainer)
	if err != nil {
		return nil, err
	}
	//a, _ := jsoniter.MarshalToString(nativeContainer)
	//b, _ := jsoniter.MarshalToString(dockercompatContainer)
	//fmt.Println(1111111111, a)
	//fmt.Println(222222222, b)
	// Container format transformation
	var container = &docker.Container{}
	container.ID = dockercompatContainer.ID
	if dockercompatContainer.Created != "" {
		container.Created, err = time.Parse("2006-01-02T15:04:05.999999999Z", dockercompatContainer.Created)
		if err != nil {
			return nil, err
		}
	}

	container.Path = dockercompatContainer.Path
	container.Args = dockercompatContainer.Args

	// Configurations
	container.Config = &docker.Config{
		ExposedPorts: make(map[docker.Port]struct{}),
		Volumes:      make(map[string]struct{}),
		Labels:       make(map[string]string),
	}
	container.Config.Hostname = dockercompatContainer.Config.Hostname
	container.Config.Labels = dockercompatContainer.Config.Labels
	// container.Config.AttachStdin = dockercompatContainer.Config.AttachStdin
	// container.Config.Cmd = dockercompatContainer.Config.Cmd
	// container.Config.Entrypoint = dockercompatContainer.Config.Entrypoint
	// **NOTICE**: According to the implementation of dockercompat, this field is invalid
	container.Config.Env = dockercompatContainer.Config.Env
	// container.Config.ExposedPorts = dockercompatContainer.Config.ExposedPorts
	// container.Config.User = dockercompatContainer.Config.User
	// container.Config.Volumes = dockercompatContainer.Config.Volumes
	// container.Config.WorkingDir = dockercompatContainer.Config.WorkingDir

	// States
	container.State.Error = dockercompatContainer.State.Error
	container.State.ExitCode = dockercompatContainer.State.ExitCode
	if dockercompatContainer.State.FinishedAt != "" {
		container.State.FinishedAt, err = time.Parse(time.RFC3339Nano, dockercompatContainer.State.FinishedAt)

	}

	if err != nil {
		return nil, err
	}
	container.State.Status = dockercompatContainer.State.Status
	if container.State.Status == "" {
		status := formatter.ContainerStatus(ctx, oriCtr)
		switch {
		case strings.Contains(status, "Up"):
			container.State.Running = true
		case strings.Contains(status, "Created"):
			container.State.Running = false
		case strings.Contains(status, "Restarting"):
			container.State.Running = true
			container.State.Restarting = true
		}
	} else {
		container.State.Paused = dockercompatContainer.State.Paused
		container.State.Pid = dockercompatContainer.State.Pid
		container.State.Restarting = dockercompatContainer.State.Restarting
		if container.State.Paused {
			dockercompatContainer.State.Running = true
		}
		if dockercompatContainer.State.Status == "exited" {
			container.State.StartedAt = container.Created
		}
		container.State.Running = dockercompatContainer.State.Running
	}

	// Container format transformation
	container.Image = nativeContainer.Image
	// container.Node = dockercompatContainer.Node

	// NetworkSettings
	container.NetworkSettings = &docker.NetworkSettings{
		Ports:       make(map[docker.Port][]docker.PortBinding),
		PortMapping: make(map[string]docker.PortMapping),
		Networks:    make(map[string]docker.ContainerNetwork),
	}
	defer func() {
		if e := recover(); e != nil {
			fmt.Println(e)
		}
	}()
	// manually copy ports
	if dockercompatContainer.NetworkSettings != nil {
		if dockercompatContainer.NetworkSettings.Ports != nil {
			for k, v := range *dockercompatContainer.NetworkSettings.Ports {
				p := docker.Port(k)
				var pb []docker.PortBinding
				for _, v2 := range v {
					pb = append(pb, docker.PortBinding(v2))
				}
				container.NetworkSettings.Ports[p] = pb
			}
		}

		container.NetworkSettings.GlobalIPv6Address = dockercompatContainer.NetworkSettings.GlobalIPv6Address
		container.NetworkSettings.GlobalIPv6PrefixLen = dockercompatContainer.NetworkSettings.GlobalIPv6PrefixLen
		container.NetworkSettings.IPAddress = dockercompatContainer.NetworkSettings.IPAddress
		container.NetworkSettings.IPPrefixLen = dockercompatContainer.NetworkSettings.IPPrefixLen
		container.NetworkSettings.MacAddress = dockercompatContainer.NetworkSettings.MacAddress
		// manually copy networks
		for k, v := range dockercompatContainer.NetworkSettings.Networks {
			var cn docker.ContainerNetwork
			cn.GlobalIPv6Address = v.GlobalIPv6Address
			cn.GlobalIPv6PrefixLen = v.GlobalIPv6PrefixLen
			cn.IPAddress = v.IPAddress
			cn.IPPrefixLen = v.IPPrefixLen
			cn.MacAddress = v.MacAddress
			container.NetworkSettings.Networks[k] = cn
		}
	}

	// Container format transformation
	container.ResolvConfPath = dockercompatContainer.ResolvConfPath
	container.HostnamePath = dockercompatContainer.HostnamePath
	container.LogPath = dockercompatContainer.LogPath
	container.Name = dockercompatContainer.Name
	if container.Name == "" {
		container.Name = getContainerName(nativeContainer.Labels)
	}
	container.Driver = dockercompatContainer.Driver
	// manually copy mounts
	for _, v := range dockercompatContainer.Mounts {
		var m docker.Mount
		m.Name = v.Name
		m.Type = v.Type
		m.Source = v.Source
		m.Destination = v.Destination
		m.Driver = v.Driver
		m.Mode = v.Mode
		m.RW = v.RW
		container.Mounts = append(container.Mounts, m)
	}
	container.RestartCount = dockercompatContainer.RestartCount
	container.AppArmorProfile = dockercompatContainer.AppArmorProfile
	container.Platform = dockercompatContainer.Platform
	//d, _ := jsoniter.MarshalToString(container)
	//fmt.Println(33333333333, d)

	return container, nil
}

func (c *CriClient) GetContainerStat(opts docker.StatsOptions) error {
	if opts.Stats == nil {
		return errors.New("can not send on nil channel")
	}

	var mutex sync.Mutex

	defer func() {
		mutex.Lock()
		if opts.Stats != nil {
			close(opts.Stats)
			opts.Stats = nil
		}
		mutex.Unlock()
	}()

	quit := make(chan struct{})
	defer close(quit)
	go func() {
		select {
		case <-opts.Done:
			mutex.Lock()
			if opts.Stats != nil {
				close(opts.Stats)
				opts.Stats = nil
			}
			mutex.Unlock()
		case <-quit:
			return
		}

	}()

	for {
		m, err := c.client.TaskService().Metrics(opts.Context, &tasks.MetricsRequest{
			Filters: []string{
				"id==" + opts.ID,
			},
		})
		if err != nil {
			return errors.New("gRPC error")
		}
		if m.Metrics == nil {
			return errors.New("no metrics received")
		}

		metric := m.Metrics[0]
		var data interface{}
		stats := new(docker.Stats)
		stats.Read = time.Now()
		switch {
		case typeurl.Is(metric.Data, (*v1.Metrics)(nil)):
			data = &v1.Metrics{}
		case typeurl.Is(metric.Data, (*v2.Metrics)(nil)):
			data = &v2.Metrics{}
		case typeurl.Is(metric.Data, (*wstats.Statistics)(nil)):
			data = &wstats.Statistics{}
		}
		if err := typeurl.UnmarshalTo(metric.Data, data); err != nil {
			return err
		}
		switch v := data.(type) {
		case *v1.Metrics:
			stats.MemoryStats.Stats.Cache = v.Memory.Cache
			stats.MemoryStats.Usage = v.Memory.Usage.Usage
			stats.MemoryStats.Limit = v.Memory.Usage.Limit
			stats.CPUStats.CPUUsage.TotalUsage = v.CPU.Usage.Total
			stats.CPUStats.SystemCPUUsage = v.CPU.Usage.Kernel
		case *v2.Metrics:
			// no cache field in v2 stats
			// according to https://github.com/containerd/nerdctl/blob/main/pkg/statsutil/stats_linux.go, cache might be related with v.Memory.InactiveFile
			// an alternative scheme should be like "stats.MemoryStats.Stats.Cache = v.Memory.InactiveFile"
			stats.MemoryStats.Stats.Cache = 0
			stats.MemoryStats.Usage = v.Memory.Usage
			stats.MemoryStats.Limit = v.Memory.UsageLimit
			stats.CPUStats.CPUUsage.TotalUsage = v.CPU.UsageUsec
			stats.CPUStats.SystemCPUUsage = v.CPU.SystemUsec
		case *wstats.Statistics:
			v1m := v.GetLinux()
			stats.MemoryStats.Stats.Cache = v1m.Memory.Cache
			stats.MemoryStats.Usage = v1m.Memory.Usage.Usage
			stats.MemoryStats.Limit = v1m.Memory.Usage.Limit
			stats.CPUStats.CPUUsage.TotalUsage = v1m.CPU.Usage.Total
			stats.CPUStats.SystemCPUUsage = v1m.CPU.Usage.Kernel
		}
		mutex.Lock()
		if opts.Stats != nil {
			opts.Stats <- stats
		} else {
			mutex.Unlock()
			return nil
		}
		mutex.Unlock()
		time.Sleep(1 * time.Second)
	}
}

func checkPrivileged(toCheck []string) bool {
	for _, v := range toCheck {
		if v == privilegedCAP1 || v == privilegedCAP2 {
			return true
		}
	}
	return false
}

func getNsFromAPIContainer(container docker.APIContainers) string {
	return container.Labels[LabelNamespace]
}
func getNsFromContainer(container docker.Container) string {
	return container.Config.Labels[LabelNamespace]
}

func getContainerName(containerLabels map[string]string) string {
	if name, ok := containerLabels[labels.Name]; ok {
		return name
	}

	if ns, ok := containerLabels[LabelPodNamespace]; ok {
		if podName, ok := containerLabels[LabelPodName]; ok {
			if containerName, ok := containerLabels[LabelContainerName]; ok {
				// Container
				return fmt.Sprintf("k8s://%s/%s/%s", ns, podName, containerName)
			}
			// Pod sandbox
			return fmt.Sprintf("k8s://%s/%s", ns, podName)
		}
	}
	return ""
}

func descFromProto(desc *types.Descriptor) ocispec.Descriptor {
	return ocispec.Descriptor{
		MediaType:   desc.MediaType,
		Size:        desc.Size,
		Digest:      digest.Digest(desc.Digest),
		Annotations: desc.Annotations,
	}
}
