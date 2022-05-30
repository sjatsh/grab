package grab

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"testing"
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
				Url:      "https://asset.pbrmaxassets.com/202205301459/e570a5ae5a0dc0299732675853e80614/4741888582829158400/5601859326138826752.zip?response-content-disposition=attachment;filename=Stone.zip",
				FileName: "Stone.zip",
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
	if err := downloader.Err(); err != nil {
		t.Fatal(err)
	}
}
