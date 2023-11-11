package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/tylerb/graceful"
	common_controls "github.com/weaveworks/scope/common/controls"
	"github.com/weaveworks/scope/probe/cri/containerd"
	"github.com/weaveworks/scope/probe/cri/docker"
	"github.com/weaveworks/scope/tools/vars"

	log "github.com/sirupsen/logrus"

	"github.com/weaveworks/common/logging"
	"github.com/weaveworks/common/network"
	"github.com/weaveworks/common/sanitize"
	"github.com/weaveworks/common/signals"
	"github.com/weaveworks/common/tracing"
	"github.com/weaveworks/scope/common/hostname"
	"github.com/weaveworks/scope/common/weave"
	"github.com/weaveworks/scope/common/xfer"
	"github.com/weaveworks/scope/probe"
	"github.com/weaveworks/scope/probe/appclient"
	"github.com/weaveworks/scope/probe/controls"
	"github.com/weaveworks/scope/probe/endpoint"
	"github.com/weaveworks/scope/probe/host"
	"github.com/weaveworks/scope/probe/kubernetes"
	"github.com/weaveworks/scope/probe/overlay"
	"github.com/weaveworks/scope/probe/process"
	"github.com/weaveworks/scope/report"
)

const (
	versionCheckPeriod    = 6 * time.Hour
	defaultServiceHost    = "https://cloud.weave.works.:443"
	kubernetesRoleHost    = "host"
	kubernetesRoleCluster = "cluster"
)

var (
	pluginAPIVersion = "1"
)

func checkNewScopeVersion(flags probeFlags) {
	checkpointFlags := makeBaseCheckpointFlags()
	if flags.kubernetesEnabled {
		checkpointFlags["kubernetes_enabled"] = "true"
	}

	//go func() {
	//	handleResponse := func(r *checkpoint.CheckResponse, err error) {
	//		if err != nil {
	//			log.Errorf("Error checking version: %v", err)
	//		} else if r.Outdated {
	//			log.Infof("Scope version %s is available; please update at %s",
	//				r.CurrentVersion, r.CurrentDownloadURL)
	//		}
	//	}
	//
	//	// Start background version checking
	//	params := checkpoint.CheckParams{
	//		Product: "scope-probe",
	//		Version: version,
	//		Flags:   checkpointFlags,
	//	}
	//	resp, err := checkpoint.Check(&params)
	//	handleResponse(resp, err)
	//	checkpoint.CheckInterval(&params, versionCheckPeriod, handleResponse)
	//}()
}

func maybeExportProfileData(flags probeFlags) {
	if flags.httpListen != "" {
		go func() {
			log.Infof("Profiling data being exported to %s", flags.httpListen)
			log.Infof("go tool pprof http://%s/debug/pprof/{profile,heap,block}", flags.httpListen)
			log.Infof("Profiling endpoint %s terminated: %v", flags.httpListen, http.ListenAndServe(flags.httpListen, nil))
		}()
	}
}
func getNodeSku(customNodeSku bool) string {
	os.Mkdir("/etc/host", 0766)
	for {
		_, err := os.Stat("/etc/host/ngep-sku")
		if os.IsNotExist(err) {
			if customNodeSku {
				_, err := os.Stat("/etc/host/machine-id")
				if os.IsNotExist(err) {
					panic("miss machine-id")
				} else {
					machineID, _ := os.ReadFile("/etc/host/machine-id")
					machineID = bytes.TrimFunc(machineID, func(r rune) bool {
						return r == '\n' || r == '\t' || r == ' '
					})
					for len(machineID) < 64 {
						machineID = append(machineID, machineID...)
					}
					fmt.Println(os.WriteFile("/etc/host/ngep-sku", machineID[:64], 0777))

				}
				continue
			} else {
				fmt.Println("wait ngep-sku")
				time.Sleep(5 * time.Second)
				continue
			}
		}
		if err != nil {
			log.Fatalf("Failed to get node sku:%v", err)
			return ""
		}
		uuid, _ := os.ReadFile("/etc/host/ngep-sku")
		for idx, uuidByte := range uuid {
			if uuidByte == 0 {
				uuid = uuid[:idx]
				break
			}
		}
		return string(bytes.TrimSpace(uuid))
	}

}

// Main runs the probe
func probeMain(flags probeFlags, targets []appclient.Target) {
	var (
		hostId      = os.Getenv("PROBE_HOSTID")
		hostID      = hostname.Get()
		controlChan chan *common_controls.ControlAction
	)
	fmt.Println("Control Collection Addr:", os.Getenv("RESOURCE_COLLECTION_ADDR"))

	setLogLevel(flags.logLevel)
	setLogFormatter(flags.logPrefix)
	log.Infof("HostId being acquired ...")

	if flags.hostId != "" {
		hostId = flags.hostId

	}
	log.Infof("HostId has been acquired:" + hostId)

	if flags.basicAuth {
		log.Infof("Basic authentication enabled")
	} else {
		log.Infof("Basic authentication disabled")
	}

	traceCloser, err := tracing.NewFromEnv("scope-probe")
	if err != nil {
		log.Infof("Tracing not initialized: %s", err)
	} else {
		defer traceCloser.Close()
	}

	logCensoredArgs()
	defer log.Info("probe exiting")

	switch flags.kubernetesRole {
	case "": // nothing special
	case kubernetesRoleHost:
		flags.kubernetesEnabled = true
	case kubernetesRoleCluster:
		//cluster agent need redirect
		//appclient.Redirect = os.Getenv("MY_NODE_IP")
		fmt.Println("probe node ip:", appclient.Redirect)

		flags.kubernetesEnabled = true
		flags.spyProcs = false
		flags.procEnabled = false
		flags.useConntrack = false
		flags.useEbpfConn = false
	default:
		log.Warnf("unrecognized --probe.kubernetes.role: %s", flags.kubernetesRole)
	}

	if flags.spyProcs && os.Getegid() != 0 {
		log.Warn("--probe.proc.spy=true, but that requires root to find everything")
	}
	if flags.kubernetesRole == "host" {
		hostID = hostname.Get() + ":" + getNodeSku(flags.customNodeSku)
	}
	//TODO
	clusterUUIDByte, _ := os.ReadFile("/etc/cluster/uuid")
	vars.ClusterUUID = string(clusterUUIDByte)
	//vars.ClusterUUID = "TEST_UUID"
	rand.Seed(time.Now().UnixNano())

	var (
		probeID  = strconv.FormatInt(rand.Int63(), 16)
		hostName = hostname.Get()
	)

	log.Infof("probe starting, version %s, ID %s", version, probeID)
	checkNewScopeVersion(flags)

	handlerRegistry := controls.NewDefaultHandlerRegistry()
	clientFactory := func(hostname string, url url.URL) (appclient.AppClient, error) {
		token := flags.token
		if url.User != nil {
			token = url.User.Username()
			url.User = nil // erase credentials, as we use a special header
		}

		if flags.basicAuth {
			token = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", flags.username, flags.password)))
		}

		probeConfig := appclient.ProbeConfig{
			BasicAuth:    flags.basicAuth,
			Token:        token,
			ProbeVersion: version,
			ProbeID:      probeID,
			Uid:          flags.userid,
			Insecure:     flags.insecure,
		}
		return appclient.NewAppClient(
			probeConfig, hostname, url,
			xfer.ControlHandlerFunc(handlerRegistry.HandleControlRequest),
		)
	}

	var clients interface {
		probe.ReportPublisher
		controls.PipeClient
	}
	if flags.printOnStdout {
		if len(targets) > 0 {
			log.Warnf("Dumping to stdout only: targets %v will be ignored", targets)
		}
		clients = new(struct {
			report.StdoutPublisher
			controls.DummyPipeClient
		})

	} else {
		multiClients := appclient.NewMultiAppClient(clientFactory, flags.noControls)
		defer multiClients.Stop()

		dnsLookupFn := net.LookupIP
		if flags.resolver != "" {
			dnsLookupFn = appclient.LookupUsing(flags.resolver)
		}
		resolver, err := appclient.NewResolver(appclient.ResolverConfig{
			Targets: targets,
			Lookup:  dnsLookupFn,
			Set:     multiClients.Set,
		})
		if err != nil {
			log.Fatalf("Failed to create resolver: %v", err)
			return
		}
		defer resolver.Stop()

		if flags.weaveEnabled && flags.weaveHostname != "" {
			dockerBridgeIP, err := network.GetFirstAddressOf(flags.dockerBridge)
			if err != nil {
				log.Errorf("Error getting docker bridge ip: %v", err)
			} else {
				weaveDNSLookup := appclient.LookupUsing(dockerBridgeIP + ":53")
				weaveTargets, err := appclient.ParseTargets([]string{flags.weaveHostname})
				if err != nil {
					log.Errorf("Failed to parse weave targets: %v", err)
				} else {
					weaveResolver, err := appclient.NewResolver(appclient.ResolverConfig{
						Targets: weaveTargets,
						Lookup:  weaveDNSLookup,
						Set:     multiClients.Set,
					})
					if err != nil {
						log.Errorf("Failed to create weave resolver: %v", err)
					} else {
						defer weaveResolver.Stop()
					}
				}
			}
		}
		clients = multiClients
	}

	p := probe.New(flags.spyInterval, flags.publishInterval, clients, flags.ticksPerFullReport, flags.noControls)
	p.AddTagger(probe.NewTopologyTagger())
	var processCache *process.CachingWalker

	if flags.kubernetesRole != kubernetesRoleCluster {
		hostReporter := host.NewReporter(hostID, hostName, probeID, version, clients, handlerRegistry)
		defer hostReporter.Stop()
		p.AddReporter(hostReporter)

		p.AddTagger(host.NewTagger(hostID))
		if flags.procEnabled {
			processCache = process.NewCachingWalker(process.NewWalker(flags.procRoot, false))
			p.AddTicker(processCache)
			p.AddReporter(process.NewReporter(processCache, hostID, process.GetDeltaTotalJiffies, flags.noCommandLineArguments))
		}

		dnsSnooper, err := endpoint.NewDNSSnooper()
		if err != nil {
			log.Errorf("Failed to start DNS snooper: nodes for external services will be less accurate: %s", err)
		} else {
			defer dnsSnooper.Stop()
		}

		endpointReporter := endpoint.NewReporter(endpoint.ReporterConfig{
			HostID:       hostID,
			HostName:     hostName,
			SpyProcs:     flags.spyProcs,
			UseConntrack: flags.useConntrack,
			WalkProc:     flags.procEnabled,
			UseEbpfConn:  flags.useEbpfConn,
			ProcRoot:     flags.procRoot,
			BufferSize:   flags.conntrackBufferSize,
			ProcessCache: processCache,
			DNSSnooper:   dnsSnooper,
		})
		defer endpointReporter.Stop()
		p.AddReporter(endpointReporter)
	}
	// containerd
	if flags.containerdEnabled {
		containerdControlActions := make(chan *common_controls.ControlAction, 10)
		controlChan = containerdControlActions
		options := containerd.RegistryOptions{
			Interval:               flags.containerdInterval,
			Pipes:                  clients,
			CollectStats:           true,
			HostID:                 hostID,
			HandlerRegistry:        handlerRegistry,
			NoCommandLineArguments: flags.noCommandLineArguments,
			NoEnvironmentVariables: flags.noEnvironmentVariables,
			ControlActions:         containerdControlActions,
		}
		if registry, err := containerd.NewRegistry(options); err == nil {
			defer registry.Stop()
			if flags.procEnabled {
				p.AddTagger(containerd.NewTagger(registry, processCache))
			}
			p.AddReporter(containerd.NewReporter(registry, hostID, probeID, p))
		} else {
			log.Errorf("Docker: failed to start registry: %v", err)
		}
	}

	if flags.dockerEnabled {
		// Don't add the bridge in Kubernetes since container IPs are global and
		// shouldn't be scoped
		if flags.dockerBridge != "" && !flags.kubernetesEnabled {
			if err := report.AddLocalBridge(flags.dockerBridge); err != nil {
				log.Errorf("Docker: problem with bridge %s: %v", flags.dockerBridge, err)
			}
		}
		dockerControlActions := make(chan *common_controls.ControlAction, 10)
		controlChan = dockerControlActions
		options := docker.RegistryOptions{
			Interval:               flags.dockerInterval,
			Pipes:                  clients,
			CollectStats:           true,
			HostID:                 hostID,
			HandlerRegistry:        handlerRegistry,
			NoCommandLineArguments: flags.noCommandLineArguments,
			NoEnvironmentVariables: flags.noEnvironmentVariables,
			ControlActions:         dockerControlActions,
		}
		if registry, err := docker.NewRegistry(options); err == nil {
			defer registry.Stop()
			if flags.procEnabled {
				p.AddTagger(docker.NewTagger(registry, processCache))
			}
			p.AddReporter(docker.NewReporter(registry, hostID, probeID, p))
		} else {
			log.Errorf("Docker: failed to start registry: %v", err)
		}
	}

	if flags.kubernetesEnabled && flags.kubernetesRole != kubernetesRoleHost {
		hostID = hostname.Get()

		if client, err := kubernetes.NewClient(flags.kubernetesClientConfig); err == nil {
			defer client.Stop()
			reporter := kubernetes.NewReporter(client, clients, probeID, hostID, p, handlerRegistry, flags.kubernetesNodeName)
			defer reporter.Stop()
			p.AddReporter(reporter)
			if flags.kubernetesRole != kubernetesRoleCluster && flags.kubernetesNodeName == "" {
				log.Warnf("No value for --probe.kubernetes.node-name, reporting all pods from every probe (which may impact performance).")
			}
		} else {
			log.Errorf("Kubernetes: failed to start client: %v", err)
			log.Errorf("Kubernetes: make sure to run Scope inside a POD with a service account or provide valid probe.kubernetes.* flags")
		}
	}

	if flags.kubernetesEnabled {
		tagger := kubernetes.NewTagger(hostID)
		p.AddTagger(&tagger)
	}

	if flags.weaveEnabled {
		client := weave.NewClient(sanitize.URL("http://", 6784, "")(flags.weaveAddr))
		weave, err := overlay.NewWeave(hostID, client)
		if err != nil {
			log.Errorf("Weave: failed to start client: %v", err)
		} else {
			defer weave.Stop()
			p.AddTagger(weave)
			p.AddReporter(weave)
		}
	}

	//if flags.pluginsRoot != "" {
	//	pluginRegistry, err := plugins.NewRegistry(
	//		flags.pluginsRoot,
	//		pluginAPIVersion,
	//		map[string]string{
	//			"probe_id":    probeID,
	//			"api_version": pluginAPIVersion,
	//		},
	//		handlerRegistry,
	//		p,
	//	)
	//
	//	if err != nil {
	//		log.Errorf("plugins: problem loading: %v", err)
	//	} else {
	//		defer pluginRegistry.Close()
	//		p.AddReporter(pluginRegistry)
	//	}
	//}

	maybeExportProfileData(flags)
	go httpServer(clients, controlChan)

	p.Start()

	signals.SignalHandlerLoop(
		logging.Logrus(log.StandardLogger()),
		p,
	)
}

func httpServer(client interface {
	probe.ReportPublisher
	controls.PipeClient
}, controlChan chan *common_controls.ControlAction) {
	logger := logging.Logrus(log.StandardLogger())
	router := mux.NewRouter().SkipClean(true)
	probe.RegisterRedirectReportPostHandler(router, client, controlChan)

	server := &graceful.Server{
		// we want to manage the stop condition ourselves below
		NoSignalHandling: true,
		Server: &http.Server{
			Addr:           ":6789",
			Handler:        router,
			ReadTimeout:    httpTimeout,
			WriteTimeout:   httpTimeout,
			MaxHeaderBytes: 1 << 20,
		},
	}
	go func() {
		log.Infof("listening on %s", ":6789")
		if err := server.ListenAndServe(); err != nil {
			log.Error(err)
		}
	}()

	// block until INT/TERM
	signals.SignalHandlerLoop(
		logger,
		stopper{
			Server:      server,
			StopTimeout: 5 * time.Second,
		},
	)
}
