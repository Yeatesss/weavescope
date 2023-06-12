package main

import (
	"fmt"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"regexp"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/goji/httpauth"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
	"github.com/tylerb/graceful"

	"github.com/weaveworks/common/logging"
	"github.com/weaveworks/common/middleware"
	"github.com/weaveworks/common/signals"
	"github.com/weaveworks/common/tracing"
	"github.com/weaveworks/scope/app"
	"github.com/weaveworks/scope/common/weave"
	"github.com/weaveworks/scope/common/xfer"
	"github.com/weaveworks/scope/probe/docker"
)

const (
	memcacheUpdateInterval = 1 * time.Minute
	httpTimeout            = 90 * time.Second
)

var registerAppMetricsOnce sync.Once

// Router creates the mux for all the various app components.
func router(collector app.Collector, controlRouter app.ControlRouter, pipeRouter app.PipeRouter, externalUI bool, capabilities map[string]bool, metricsGraphURL string) http.Handler {
	router := mux.NewRouter().SkipClean(true)

	// We pull in the http.DefaultServeMux to get the pprof routes
	router.PathPrefix("/debug/pprof").Handler(http.DefaultServeMux)

	app.RegisterReportPostHandler(collector, router)
	app.RegisterControlRoutes(router, controlRouter)
	app.RegisterPipeRoutes(router, pipeRouter)
	app.RegisterTopologyRoutes(router, app.WebReporter{Reporter: collector, MetricsGraphURL: metricsGraphURL}, capabilities)
	app.RegisterAdminRoutes(router, collector)

	uiHandler := http.FileServer(GetFS(externalUI))
	router.PathPrefix("/ui").Name("static").Handler(
		middleware.PathRewrite(regexp.MustCompile("^/ui"), "").Wrap(
			uiHandler))
	router.PathPrefix("/").Name("static").Handler(uiHandler)

	middlewares := middleware.Merge(
		middleware.Tracer{
			RouteMatcher: router,
		},
	)

	return middlewares.Wrap(router)
}

func collectorFactory(window time.Duration) (app.Collector, error) {
	return app.NewCollector(window), nil

}

func controlRouterFactory() (app.ControlRouter, error) {
	return app.NewLocalControlRouter(), nil

}

func pipeRouterFactory() (app.PipeRouter, error) {
	return app.NewLocalPipeRouter(), nil

}

// Main runs the app
func appMain(flags appFlags) {
	setLogLevel(flags.logLevel)
	setLogFormatter(flags.logPrefix)
	runtime.SetBlockProfileRate(flags.blockProfileRate)

	app.NewAuthorization(app.AppConfig{
		Identity: flags.identity,
		Insecure: flags.insecure,
	})

	traceCloser, err := tracing.NewFromEnv(fmt.Sprintf("scope-%s", flags.serviceName))
	if err != nil {
		log.Infof("Tracing not initialized: %s", err)
	} else {
		defer traceCloser.Close()
	}

	defer log.Info("app exiting")
	rand.Seed(time.Now().UnixNano())
	app.UniqueID = strconv.FormatInt(rand.Int63(), 16)
	app.Version = version
	log.Infof("app starting, version %s, ID %s", app.Version, app.UniqueID)
	logCensoredArgs()

	tickInterval := flags.tickInterval
	if tickInterval == 0 {
		tickInterval = flags.storeInterval
	}
	collector, err := collectorFactory(flags.window)
	if err != nil {
		log.Fatalf("Error creating collector: %v", err)
		return
	}

	defer collector.Close()

	controlRouter, err := controlRouterFactory()
	if err != nil {
		log.Fatalf("Error creating control router: %v", err)
		return
	}

	pipeRouter, err := pipeRouterFactory()
	if err != nil {
		log.Fatalf("Error creating pipe router: %v", err)
		return
	}

	// Periodically try and register our IP address in WeaveDNS.
	if flags.weaveEnabled && flags.weaveHostname != "" {
		weave, err := newWeavePublisher(
			flags.dockerEndpoint, flags.weaveAddr,
			flags.weaveHostname, flags.containerName)
		if err != nil {
			log.Println("Failed to start weave integration:", err)
		} else {
			defer weave.Stop()
		}
	}

	capabilities := map[string]bool{
		xfer.HistoricReportsCapability: collector.HasHistoricReports(),
	}
	logger := logging.Logrus(log.StandardLogger())
	handler := router(collector, controlRouter, pipeRouter, flags.externalUI, capabilities, flags.metricsGraphURL)
	if flags.logHTTP {
		handler = middleware.Log{
			Log:               logger,
			LogRequestHeaders: flags.logHTTPHeaders,
		}.Wrap(handler)
	}

	if flags.basicAuth {
		log.Infof("Basic authentication enabled")
		handler = httpauth.SimpleBasicAuth(flags.username, flags.password)(handler)
	} else {
		log.Infof("Basic authentication disabled")
	}

	server := &graceful.Server{
		// we want to manage the stop condition ourselves below
		NoSignalHandling: true,
		Server: &http.Server{
			Addr:           flags.listen,
			Handler:        handler,
			ReadTimeout:    httpTimeout,
			WriteTimeout:   httpTimeout,
			MaxHeaderBytes: 1 << 20,
		},
	}
	go func() {
		log.Infof("listening on %s", flags.listen)
		if err := server.ListenAndServe(); err != nil {
			log.Error(err)
		}
	}()

	// block until INT/TERM
	signals.SignalHandlerLoop(
		logger,
		stopper{
			Server:      server,
			StopTimeout: flags.stopTimeout,
		},
	)
}

// stopper adapts graceful.Server's interface to signals.SignalReceiver's interface.
type stopper struct {
	Server      *graceful.Server
	StopTimeout time.Duration
}

// Stop implements signals.SignalReceiver's Stop method.
func (c stopper) Stop() error {
	// stop listening, wait for any active connections to finish
	c.Server.Stop(c.StopTimeout)
	<-c.Server.StopChan()
	return nil
}

func newWeavePublisher(dockerEndpoint, weaveAddr, weaveHostname, containerName string) (*app.WeavePublisher, error) {
	dockerClient, err := docker.NewDockerClientStub(dockerEndpoint)
	if err != nil {
		return nil, err
	}
	weaveClient := weave.NewClient(weaveAddr)
	return app.NewWeavePublisher(
		weaveClient,
		dockerClient,
		app.Interfaces,
		weaveHostname,
		containerName,
	), nil
}
