package main

import (
	"flag"
	"fmt"
	"github.com/go-yaml/yaml"
	"github.com/sjatsh/grab"
	"net/http"
	"os"
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
	)
	if err := downloader.StartDownload(); err != nil {
		panic(err)
	}

	go func() {
		_ = http.ListenAndServe("0.0.0.0:6060", nil)
	}()

	downloader.WithProgressHook(func(progress float64) bool {
		fmt.Printf("%.1f%%\n", progress*100)
		return true
	})
	if err := downloader.Err(); err != nil {
		panic(err)
	}
}
