package grab

import (
	"context"
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
	resps             []*Response
	perSecondHookOnce sync.Once
	opts              []DownloadOptionFunc
	lastBps           float64
	current           int64
	total             int64
	errOnce           sync.Once
	err               chan error
	done              int32
	cancel            context.CancelFunc
}

func NewDownloader(path string, files []DownloadFile, opts ...DownloadOptionFunc) *Downloader {
	return &Downloader{
		opts:  opts,
		path:  path,
		files: files,
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
	resp, err := GetBatch(batchReq, d.opts...)
	if err != nil {
		return err
	}
	d.cancel = resp.Cancel
	atomic.StoreInt64(&d.current, resp.Current)
	atomic.StoreInt64(&d.total, resp.Total)

	go func() {
		for v := range resp.ResCh {
			d.Lock()
			d.resps = append(d.resps, v)
			d.Unlock()
		}
		atomic.StoreInt32(&d.done, 1)
	}()
	return nil
}

func (d *Downloader) PauseDownload() error {
	d.cancel()
	return <-d.Err()
}

func (d *Downloader) Wait() {
	wg := &sync.WaitGroup{}
	wg.Add(len(d.resps))
	for _, v := range d.resps {
		resp := v
		go func() {
			defer wg.Done()
			resp.Wait()
		}()
	}
	wg.Wait()
}

func (d *Downloader) Err() <-chan error {
	d.errOnce.Do(func() {
		errWg := &errgroup.Group{}
		d.RLock()
		for _, v := range d.resps {
			resp := v
			errWg.Go(func() error {
				return resp.Err()
			})
		}
		d.RUnlock()
		go func() {
			d.err <- errWg.Wait()
		}()
	})
	return d.err
}

func (d *Downloader) BytesPerSecond() float64 {
	var bps float64
	d.RLock()
	for _, v := range d.resps {
		bps += v.BytesPerSecond()
	}
	d.RUnlock()
	return bps
}
