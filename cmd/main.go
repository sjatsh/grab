package main

import (
	"flag"
	"fmt"
	"github.com/go-yaml/yaml"
	"github.com/sjatsh/grab"
	"net/http"
	_ "net/http/pprof"
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

	go func() {
		_ = http.ListenAndServe(":6060", nil)
	}()

	downloader := grab.NewDownloader(
		config.Path,
		config.Files,
		grab.WithPartSize(32*1024*1024),
		grab.WithDownloadWorkers(10),
	)
	downloader.WithProgressHook(func(current, total int64, err error) {
		if err != nil {
			panic(err)
		}
		fmt.Printf("%.2f%%\n", float64(current)/float64(total)*100)
	})

	start := time.Now()
	if err := downloader.StartDownload(); err != nil {
		panic(err)
	}
	if err := downloader.Err(); err != nil {
		panic(err)
	}
	fmt.Println(time.Now().Sub(start))
}
