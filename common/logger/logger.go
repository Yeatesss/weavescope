package logger

import (
	"github.com/charmbracelet/log"
	"os"
)

var Logger = log.NewWithOptions(os.Stdout, log.Options{
	Level:           log.InfoLevel,
	ReportCaller:    true,
	ReportTimestamp: true,
	TimeFormat:      "2006-01-02 15:04:05",
	Prefix:          "Proxy 🍪 ",
})

//func initLogger(level log.Level) {
//
//	logger = log.NewWithOptions(os.Stdout, log.Options{
//		Level:           level,
//		ReportCaller:    true,
//		ReportTimestamp: true,
//		TimeFormat:      "2006-01-02 15:04:05",
//		Prefix:          "Proxy 🍪 ",
//	})
//}
