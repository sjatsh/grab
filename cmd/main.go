package main

import (
	"flag"
	"fmt"
	"github.com/go-yaml/yaml"
	"github.com/sjatsh/grab"
	"net/http"
	"os"
	"time"
)

type Config struct {
	Path  string              `yaml:"path"`
	Files []grab.DownloadFile `yaml:"files"`
}

var configPath = flag.String("c", "", "")

func main() {
	flag.Parse()

	data, err := os.ReadFile(*configPath)
	if err != nil {
		panic(err)
	}
	config := &Config{}
	if err := yaml.Unmarshal(data, config); err != nil {
		panic(err)
	}

	downloader := grab.NewDownloader(
		config.Path,
		config.Files,
		grab.WithPartSize(32*1024*1024),
		grab.WithDownloadWorkers(10),
	)

	start := time.Now()
	if err := downloader.StartDownload(); err != nil {
		panic(err)
	}

	go func() {
		_ = http.ListenAndServe("0.0.0.0:6060", nil)
	}()

	downloader.WithProgressHook(func(current, total int64, err error) {
		if err != nil {
			panic(err)
		}
		fmt.Printf("%.2f%%\n", float64(current)/float64(total)*100)
	})

	exist := make(chan struct{})
	time.AfterFunc(time.Second*10, func() {
		if err := downloader.PauseDownload(); err != nil {
			panic(err)
		}
		exist <- struct{}{}
	})
	<-exist
	fmt.Println(time.Now().Sub(start))
}
