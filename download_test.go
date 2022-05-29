package grab

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"testing"
	"time"
)

func TestStartDownload(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	downloader := NewDownloader(
		home+string(os.PathSeparator)+"download",
		[]DownloadFile{
			{
				Url:      "http://mirror.nl.leaseweb.net/speedtest/10000mb.bin",
				FileName: "10000mb.bin",
			},
			{
				Url:      "http://lg-sin.fdcservers.net/10GBtest.zip",
				FileName: "10GBtest.zip",
			},
		},
	)
	if err := downloader.StartDownload(); err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = http.ListenAndServe("0.0.0.0:6060", nil)
	}()

	downloader.WithProgressHook(func(progress float64) bool {
		fmt.Printf("%.1f%%\n", progress*100)
		return true
	})

	go func() {
		time.AfterFunc(time.Second*10, func() {
			if err := downloader.PauseDownload(); err != nil {
				t.Error(err)
				return
			}
		})
	}()
	downloader.Wait()
}
