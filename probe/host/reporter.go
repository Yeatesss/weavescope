package host

import (
	"fmt"
	docker_client "github.com/fsouza/go-dockerclient"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/weaveworks/common/mtime"
	"github.com/weaveworks/scope/probe/controls"
	"github.com/weaveworks/scope/report"
)

// Keys for use in Node.Latest.
const (
	DockerVersion    = report.DockerVersion
	DockerApiVersion = report.DockerApiVersion
	DockerGoVersion  = report.DockerGoVersion
	DockerDriver     = report.DockerDriver
	DockerRootDir    = report.DockerRootDir
	HostMasterIP     = report.HostMasterIP
	Timestamp        = report.Timestamp
	HostName         = report.HostName
	LocalNetworks    = report.HostLocalNetworks
	OS               = report.OS
	KernelVersion    = report.KernelVersion
	Uptime           = report.Uptime
	Load1            = report.Load1
	CPUUsage         = report.HostCPUUsage
	MemoryUsage      = report.HostMemoryUsage
	ScopeVersion     = report.ScopeVersion
)

var CollectorAddress string

// Exposed for testing.
const (
	ProcUptime  = "/proc/uptime"
	ProcLoad    = "/proc/loadavg"
	ProcStat    = "/proc/stat"
	ProcMemInfo = "/proc/meminfo"
)

// Exposed for testing.
var (
	MetadataTemplates = report.MetadataTemplates{
		KernelVersion: {ID: KernelVersion, Label: "Kernel version", From: report.FromLatest, Priority: 1},
		Uptime:        {ID: Uptime, Label: "Uptime", From: report.FromLatest, Priority: 2, Datatype: report.Duration},
		HostName:      {ID: HostName, Label: "Hostname", From: report.FromLatest, Priority: 11},
		OS:            {ID: OS, Label: "OS", From: report.FromLatest, Priority: 12},
		LocalNetworks: {ID: LocalNetworks, Label: "Local networks", From: report.FromSets, Priority: 13},
		ScopeVersion:  {ID: ScopeVersion, Label: "Scope version", From: report.FromLatest, Priority: 14},
	}

	MetricTemplates = report.MetricTemplates{
		CPUUsage:    {ID: CPUUsage, Label: "CPU", Format: report.PercentFormat, Priority: 1},
		MemoryUsage: {ID: MemoryUsage, Label: "Memory", Format: report.FilesizeFormat, Priority: 2},
		Load1:       {ID: Load1, Label: "Load (1m)", Format: report.DefaultFormat, Group: "load", Priority: 11},
	}
)

// Reporter generates Reports containing the host topology.
type Reporter struct {
	sync.RWMutex
	hostID          string
	hostName        string
	probeID         string
	version         string
	pipes           controls.PipeClient
	hostShellCmd    []string
	handlerRegistry *controls.HandlerRegistry
	pipeIDToTTY     map[string]uintptr
	dockerClient    Client
}

var NewDockerClientStub = newDockerClient

// Client interface for mocking.
type Client interface {
	Version() (*docker_client.Env, error)
	Info() (*docker_client.DockerInfo, error)
}

func newDockerClient(endpoint string) (Client, error) {
	if endpoint == "" {
		return docker_client.NewClientFromEnv()
	}
	return docker_client.NewClient(endpoint)
}

// NewReporter returns a Reporter which produces a report containing host
// topology for this host.
func NewReporter(hostID, hostName, probeID, version string, pipes controls.PipeClient, handlerRegistry *controls.HandlerRegistry) *Reporter {
	client, err := NewDockerClientStub("")
	if err != nil {
		client = nil
	}
	r := &Reporter{
		hostID:          hostID,
		hostName:        hostName,
		probeID:         probeID,
		pipes:           pipes,
		version:         version,
		hostShellCmd:    getHostShellCmd(),
		handlerRegistry: handlerRegistry,
		pipeIDToTTY:     map[string]uintptr{},
		dockerClient:    client,
	}
	r.registerControls()
	return r
}

// Name of this reporter, for metrics gathering
func (*Reporter) Name() string { return "Host" }

// GetLocalNetworks is exported for mocking
var GetLocalNetworks = report.GetLocalNetworks

// Report implements Reporter.
func (r *Reporter) Report() (report.Report, error) {
	var (
		rep           = report.MakeReport()
		ip            string
		localCIDRs    []string
		dockerVersion string
		dockerDrive   string
		dockerRootDir string
		apiVersion    string
		goVersion     string
	)

	localNets, err := GetLocalNetworks()
	if err != nil {
		return rep, nil
	}
	for _, localNet := range localNets {
		localCIDRs = append(localCIDRs, localNet.String())
	}

	uptime, err := GetUptime()
	if err != nil {
		return rep, err
	}

	kernelRelease, kernelVersion, err := GetKernelReleaseAndVersion()
	if err != nil {
		return rep, err
	}
	kernel := fmt.Sprintf("%s %s", kernelRelease, kernelVersion)

	rep.Host = rep.Host.WithMetadataTemplates(MetadataTemplates)
	rep.Host = rep.Host.WithMetricTemplates(MetricTemplates)

	now := mtime.Now()
	metrics := GetLoad(now)
	cpuUsage, max := GetCPUUsagePercent()
	metrics[CPUUsage] = report.MakeSingletonMetric(now, cpuUsage).WithMax(max)
	memoryUsage, max := GetMemoryUsageBytes()
	metrics[MemoryUsage] = report.MakeSingletonMetric(now, memoryUsage).WithMax(max)
	if r.dockerClient != nil {
		env, e := r.dockerClient.Version()
		if e == nil && env != nil && env.Exists("Version") {
			dockerVersion = env.Get("Version")
			apiVersion = env.Get("ApiVersion")
			goVersion = env.Get("GoVersion")
		}
		info, e := r.dockerClient.Info()
		if e == nil {
			dockerDrive = info.Driver
			dockerRootDir = info.DockerRootDir
		}

	}
	uuid, _ := os.ReadFile("/etc/cluster/uuid")
	if hostIP := os.Getenv("HOST_IP"); hostIP != "" {
		ip = hostIP
	} else if CollectorAddress != "" {
		i, e := GetOutboundIP(CollectorAddress)
		if e == nil {
			ip = i.String()
		}
	}

	rep.Host.AddNode(
		report.MakeNodeWith(report.MakeHostNodeID(r.hostID), map[string]string{
			DockerVersion:         dockerVersion,
			DockerApiVersion:      apiVersion,
			HostMasterIP:          ip,
			DockerDriver:          dockerDrive,
			DockerRootDir:         dockerRootDir,
			DockerGoVersion:       goVersion,
			report.ControlProbeID: r.probeID,
			Timestamp:             mtime.Now().UTC().Format(time.RFC3339Nano),
			HostName:              r.hostName,
			OS:                    runtime.GOOS,
			KernelVersion:         kernel,
			Uptime:                strconv.Itoa(int(uptime / time.Second)), // uptime in seconds
			ScopeVersion:          r.version,
		}).
			WithSets(report.MakeSets().
				Add(LocalNetworks, report.MakeStringSet(localCIDRs...)),
			).
			WithMetrics(metrics).
			WithLatest("cluster_uuid", mtime.Now(), string(uuid)).
			WithLatestActiveControls(ExecHost),
	)

	rep.Host.Controls.AddControl(report.Control{
		ID:    ExecHost,
		Human: "Exec shell",
		Icon:  "fa fa-terminal",
	})

	return rep, nil
}

// Stop stops the reporter.
func (r *Reporter) Stop() {
	r.deregisterControls()
}

func GetOutboundIP(address string) (net.IP, error) {
	conn, err := net.Dial("tcp", address)
	if err != nil {
		return []byte{}, err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr().(*net.TCPAddr)
	return localAddr.IP, nil
}
