package performance_analyzer

import (
	"context"
	"fmt"
	"github.com/charmbracelet/log"
	"github.com/pyroscope-io/client/pyroscope"
	"github.com/weaveworks/scope/common/logger"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

var pyscore *pyroscope.Profiler

const SwitchPyroscope = syscall.Signal(0x25) //37
const SwitchPprof = syscall.Signal(0x26)     //38
const SwitchLogLevel = syscall.Signal(0x27)  //39

type Analyze struct {
	serverHost string //pyroscope服务host地址
	serverName string //当前服务名
}

var analyzerSvr *http.Server

func NewAnalyze(serverHost string, serverName string) *Analyze {
	return &Analyze{
		serverHost: serverHost,
		serverName: serverName,
	}
}

func (l *Analyze) Start() {
	quit := make(chan os.Signal, 1)

	signal.Notify(quit, SwitchPyroscope, SwitchPprof, SwitchLogLevel)
	for {
		select {
		case sign := <-quit:
			switch sign {
			case SwitchLogLevel:
				switch logger.Logger.GetLevel() {
				case log.InfoLevel:
					fmt.Println("Swith log level to debug")
					logger.Logger = log.NewWithOptions(os.Stdout, log.Options{
						Level:           log.DebugLevel,
						ReportCaller:    true,
						ReportTimestamp: true,
						TimeFormat:      "2006-01-02 15:04:05",
						Prefix:          "Proxy 🍪 ",
					})
				case log.DebugLevel:
					fmt.Println("Swith log level to info")
					logger.Logger = log.NewWithOptions(os.Stdout, log.Options{
						Level:           log.InfoLevel,
						ReportCaller:    true,
						ReportTimestamp: true,
						TimeFormat:      "2006-01-02 15:04:05",
						Prefix:          "Proxy 🍪 ",
					})
				}
			case SwitchPyroscope:
				l.performance()
			case SwitchPprof:
				if analyzerSvr == nil {
					server := &http.Server{Addr: ":9999", Handler: nil}
					go func() {
						server.ListenAndServe()
					}()
				} else {
					analyzerSvr.Shutdown(context.Background())
					analyzerSvr = nil
				}
			}
		}
	}
}

func (l *Analyze) performance() {
	if pyscore != nil {
		logger.Logger.Info("Stop pyroscope")
		pyscore.Stop()
		pyscore = nil
	} else {
		logger.Logger.Info("Start pyroscope")
		runtime.SetMutexProfileFraction(5)
		runtime.SetBlockProfileRate(5)
		pyscore, _ = pyroscope.Start(pyroscope.Config{
			ApplicationName: l.serverName,
			// replace this with the address of pyroscope server
			ServerAddress: l.serverHost,
			// you can disable logging by setting this to nil
			Logger: nil,
			// optionally, if authentication is enabled, specify the API key:
			// AuthToken: os.Getenv("PYROSCOPE_AUTH_TOKEN"),
			ProfileTypes: []pyroscope.ProfileType{
				// these profile types are enabled by default:
				pyroscope.ProfileCPU,
				pyroscope.ProfileAllocObjects,
				pyroscope.ProfileAllocSpace,
				pyroscope.ProfileInuseObjects,
				pyroscope.ProfileInuseSpace,

				// these profile types are optional:
				pyroscope.ProfileGoroutines,
				pyroscope.ProfileMutexCount,
				pyroscope.ProfileMutexDuration,
				pyroscope.ProfileBlockCount,
				pyroscope.ProfileBlockDuration,
			},
		})
	}

}
