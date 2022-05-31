package grab

import (
	"golang.org/x/sync/errgroup"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

type DownloadFile struct {
	Url      string `json:"url" yaml:"url"`
	FileName string `json:"fileName" yaml:"fileName"`
}

type Downloader struct {
	sync.RWMutex
	path              string
	files             []DownloadFile
	resp              []*Response
	errHookOnce       sync.Once
	progressHookOnce  sync.Once
	perSecondHookOnce sync.Once
	opts              []DownloadOptionFunc
	lastBps           float64
	current           int64
	total             int64
}

func NewDownloader(path string, files []DownloadFile, opts ...DownloadOptionFunc) *Downloader {
	return &Downloader{
		opts:  opts,
		path:  path,
		files: files,
		resp:  []*Response{},
	}
}

func (d *Downloader) WithProgressHook(hook func(current, total int64, err error) bool) {
	d.progressHookOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for range ticker.C {
				d.Progress()
				select {
				case err := <-d.Err():
					hook(atomic.LoadInt64(&d.current), atomic.LoadInt64(&d.total), err)
				default:
					hook(atomic.LoadInt64(&d.current), atomic.LoadInt64(&d.total), nil)
				}
			}
		}()
	})
}

func (d *Downloader) WithErrHook(hook func(err error)) {
	d.errHookOnce.Do(func() {
		go func() {
			hook(<-d.Err())
		}()
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

	errWg := errgroup.Group{}
	d.RLock()
	for _, v := range d.resp {
		resp := v
		errWg.Go(func() error {
			return resp.Cancel()
		})
	}
	d.RUnlock()
	return errWg.Wait()
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

	var bytesComplete int64
	var totalSize int64
	for _, v := range d.resp {
		bytesComplete += v.BytesComplete()
		totalSize += v.Size()
	}
	atomic.StoreInt64(&d.current, bytesComplete)
	atomic.StoreInt64(&d.total, totalSize)
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

	wg := &sync.WaitGroup{}
	d.RLock()
	wg.Add(len(d.resp))
	for _, v := range d.resp {
		resp := v
		go func() {
			defer wg.Done()
			resp.Wait()
		}()
	}
	d.RUnlock()
	wg.Wait()
}

func (d *Downloader) Err() <-chan error {
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

	errCh := make(chan error)
	go func() {
		errCh <- errWg.Wait()
	}()
	return errCh
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

	var bps float64

	d.RLock()
	for _, v := range d.resp {
		bps += v.BytesPerSecond()
	}
	d.RUnlock()
	return bps
}
