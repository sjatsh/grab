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

func (d *Downloader) WithProgressHook(hook func(current, total int64, err error)) {
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			select {
			case err := <-d.Err():
				hook(atomic.LoadInt64(&d.current), atomic.LoadInt64(&d.total), err)
				return
			default:
				hook(atomic.LoadInt64(&d.current), atomic.LoadInt64(&d.total), nil)
			}
		}
	}()
}

func (d *Downloader) WithErrHook(hook func(err error)) {
	go func() {
		hook(<-d.Err())
	}()
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

	d.opts = append(d.opts, WithWriteHook(func(n int64) {
		atomic.AddInt64(&d.current, n)
	}))
	current, total, resp, err := GetBatch(batchReq, d.opts...)
	if err != nil {
		return err
	}
	atomic.StoreInt64(&d.current, current)
	atomic.StoreInt64(&d.total, total)

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
	wg.Add(len(d.resp))
	for _, v := range d.resp {
		resp := v
		go func() {
			defer wg.Done()
			resp.Wait()
		}()
	}
	wg.Wait()
	time.Sleep(time.Second)
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
	for _, v := range d.resp {
		resp := v
		errWg.Go(func() error {
			return resp.Err()
		})
	}

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
