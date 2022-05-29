package grab

import "C"
import (
	"golang.org/x/sync/errgroup"
	"os"
	"sync"
	"time"
)

type DownloadFile struct {
	Url      string `json:"url"`
	FileName string `json:"fileName"`
}

type Downloader struct {
	sync.RWMutex
	path              string
	files             []DownloadFile
	resp              []*Response
	errHookOnce       sync.Once
	progressHookOnce  sync.Once
	perSecondHookOnce sync.Once
	lastProgress      float64
	lastBps           float64
	opts              []DownloadOptionFunc
}

func NewDownloader(path string, files []DownloadFile, opts ...DownloadOptionFunc) *Downloader {
	return &Downloader{
		opts:  opts,
		path:  path,
		files: files,
		resp:  []*Response{},
	}
}

func (d *Downloader) WithProgressHook(hook func(progress float64) bool) {
	d.progressHookOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for range ticker.C {
				progress := d.Progress()
				if progress > 0 && (d.lastProgress == 0 || progress-d.lastProgress >= 0.001) {
					d.lastProgress = progress
					if !hook(progress) {
						return
					}
				}
			}
		}()
	})
}

func (d *Downloader) WithErrHook(hook func(err error)) {
	d.errHookOnce.Do(func() {
		go hook(d.Err())
	})
}

func (d *Downloader) WithBytesPerSecondHook(hook func(bps float64) bool) {
	d.perSecondHookOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for range ticker.C {
				bps := d.BytesPerSecond()
				if d.lastBps == bps {
					continue
				}
				d.lastBps = bps
				if !hook(bps) {
					return
				}
			}
		}()
	})
}

func (d *Downloader) StartDownload() error {
	if len(d.files) == 0 {
		return nil
	}
	batchReq := make([]BatchReq, 0)
	for _, v := range d.files {
		batchReq = append(batchReq, BatchReq{
			Dst: d.path + string(os.PathSeparator) + v.FileName,
			Url: v.Url,
		})
	}
	resp, err := GetBatch(batchReq, d.opts...)
	if err != nil {
		return err
	}
	go func() {
		for v := range resp {
			d.Lock()
			d.resp = append(d.resp, v)
			d.Unlock()
		}
	}()
	return nil
}

func (d *Downloader) PauseDownload() error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if len(d.resp) != len(d.files) {
			continue
		}
		break
	}

	d.RLock()
	defer d.RUnlock()
	for _, v := range d.resp {
		if err := v.Cancel(); err != nil {
			return err
		}
	}
	return nil
}

func (d *Downloader) Progress() float64 {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if len(d.resp) != len(d.files) {
			continue
		}
		break
	}

	d.RLock()
	defer d.RUnlock()

	var bytesComplete int64
	var totalSize int64
	for _, v := range d.resp {
		bytesComplete += v.BytesComplete()
		totalSize += v.Size()
	}
	return float64(bytesComplete) / float64(totalSize)
}

func (d *Downloader) Wait() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if len(d.resp) != len(d.files) {
			continue
		}
		break
	}

	wg := sync.WaitGroup{}
	wg.Add(len(d.resp))
	d.RLock()
	for _, v := range d.resp {
		resp := v
		go func() {
			defer wg.Done()
			resp.Wait()
		}()
	}
	defer d.RUnlock()
	wg.Wait()
}

func (d *Downloader) Err() error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if len(d.resp) != len(d.files) {
			continue
		}
		break
	}

	errWg := errgroup.Group{}
	d.RLock()
	for _, v := range d.resp {
		resp := v
		errWg.Go(func() error {
			return resp.Err()
		})
	}
	d.RUnlock()
	return errWg.Wait()
}

func (d *Downloader) BytesPerSecond() float64 {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if len(d.resp) != len(d.files) {
			continue
		}
		break
	}

	d.RLock()
	defer d.RUnlock()

	var bps float64
	for _, v := range d.resp {
		bps += v.BytesPerSecond()
	}
	return bps
}
