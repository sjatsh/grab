package grab

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"testing"
	"time"
)

func TestStartDownload(t *testing.T) {
	downloader := NewDownloader(
		"/Users/sunjun/download",
		[]DownloadFile{
			{
				Url:      "https://asset.pbrmaxassets.com/202205300158/58dc604e9f0bb28afaeef1deba3d2b97/5602014057703731200/5602098608698380288.zip?response-content-disposition=attachment;filename=Brick.zip",
				FileName: "Brick.zip",
			},
			{
				Url:      "https://asset.pbrmaxassets.com/202205300157/c20191eb7da528aaa7c634a20f6e5121/4741888582829158400/5601859326138826752.zip?response-content-disposition=attachment;filename=Stone.zip",
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
