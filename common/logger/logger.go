package logger

import (
	"fmt"
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

func init() {
	if os.Getenv("DEBUG_SCOPE") != "" {
		fmt.Println("Start Log Debug")
		Logger = log.NewWithOptions(os.Stdout, log.Options{
			Level:           log.DebugLevel,
			ReportCaller:    true,
			ReportTimestamp: true,
			TimeFormat:      "2006-01-02 15:04:05",
			Prefix:          "Proxy 🍪 ",
		})
		return
	}
	fmt.Println("Start Log Info")

}
