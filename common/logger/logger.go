package logger

import (
	"fmt"
	"github.com/charmbracelet/log"
	"os"
	"time"
)

var Logger = log.NewWithOptions(os.Stdout, log.Options{
	Level:           log.InfoLevel,
	ReportCaller:    true,
	ReportTimestamp: true,
	TimeFormat:      "2006-01-02 15:04:05",
	Prefix:          "Proxy 🍪 ",
})

func init() {
	if os.Getenv("DEBUG_SCOPE") != "" || os.Getenv("TRY") == "1" {
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
	if os.Getenv("TRY") == "1" {
		go func() {
			time.Sleep(60 * 10 * time.Second)
			Logger = log.NewWithOptions(os.Stdout, log.Options{
				Level:           log.InfoLevel,
				ReportCaller:    true,
				ReportTimestamp: true,
				TimeFormat:      "2006-01-02 15:04:05",
				Prefix:          "Proxy 🍪 ",
			})
		}()

	}
	fmt.Println("Start Log Info")

}
