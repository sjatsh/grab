package grab

import (
	"context"
	"errors"
	"golang.org/x/sync/errgroup"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const (
	StatusEmpty    int64 = 0
	StatusStart    int64 = 1
	StatusStopped  int64 = 2
	StatusStopping int64 = 3
)

type DownloadFile struct {
	Url      string `json:"url" yaml:"url"`
	FileName string `json:"fileName" yaml:"fileName"`
}

type Downloader struct {
	l            sync.RWMutex
	path         string
	files        []DownloadFile
	opts         []DownloadOptionFunc
	lastBps      float64
	cancel       context.CancelFunc
	startLock    sync.Mutex
	status       int64
	progressHook func(current, total int64, err error)

	current           int64
	lastCurrent       int64
	total             int64
	errChClosed       int64
	done              int64
	perSecondHookOnce sync.Once
	errOnce           sync.Once
	err               error
	hasErr            chan struct{}
	resps             []*Response
	progressDone      chan struct{}
}

func NewDownloader(path string, files []DownloadFile, opts ...DownloadOptionFunc) *Downloader {
	return &Downloader{
		opts:         opts,
		path:         path,
		files:        files,
		hasErr:       make(chan struct{}),
		progressDone: make(chan struct{}),
	}
}

func (d *Downloader) StartDownload() error {
	if len(d.files) == 0 {
		return nil
	}
	d.startLock.Lock()
	defer d.startLock.Unlock()

	status := atomic.LoadInt64(&d.status)
	if status == StatusStart {
		return errors.New("already in progress download")
	}
	if status == StatusStopping {
		return errors.New("stopping download, please retry later")
	}

	defer func() {
		if d.progressHook != nil && status != StatusEmpty {
			d.WithProgressHook(nil)
		}
		atomic.StoreInt64(&d.status, StatusStart)
	}()

	d.clean()

	batchReq := make([]BatchReq, 0)
	for _, v := range d.files {
		filePath := d.path + string(os.PathSeparator) + v.FileName
		batchReq = append(batchReq, BatchReq{
			Dst: filePath,
			Url: v.Url,
		})
	}

	opts := append(d.opts, WithWriteHook(func(n int64) {
		status := atomic.LoadInt64(&d.status)
		if status == StatusStopped || status == StatusStopping {
			return
		}
		atomic.AddInt64(&d.current, n)
	}))

	current, total, err := DefaultClient.WithDownloadOptions(opts...).GetProgress(batchReq)
	if err != nil {
		return err
	}
	atomic.StoreInt64(&d.current, current)
	atomic.StoreInt64(&d.total, total)

	resp, err := GetBatch(batchReq, opts...)
	if err != nil {
		return err
	}
	d.cancel = resp.Cancel

	go func() {
		for v := range resp.ResCh {
			d.l.Lock()
			d.resps = append(d.resps, v)
			d.l.Unlock()
		}
		atomic.StoreInt64(&d.done, 1)
	}()

	go func() {
		_ = d.Err()
	}()

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			errWg := &errgroup.Group{}
			d.l.RLock()
			for _, v := range d.resps {
				resp := v
				errWg.Go(func() error {
					return resp.Err()
				})
			}
			d.l.RUnlock()
			if err := errWg.Wait(); err != nil {
				if atomic.CompareAndSwapInt64(&d.errChClosed, 0, 1) {
					d.err = err
					close(d.hasErr)
				}
				d.cancel()
				return
			}

			if d.Done() {
				return
			}
		}
	}()
	return nil
}

func (d *Downloader) PauseDownload() error {
	d.startLock.Lock()
	defer d.startLock.Unlock()

	if atomic.LoadInt64(&d.status) == StatusStopped {
		return errors.New("now is not in progress , please run StartDownload again")
	}
	if atomic.LoadInt64(&d.status) == StatusStopping {
		return nil
	}
	defer atomic.StoreInt64(&d.status, StatusStopped)
	atomic.StoreInt64(&d.status, StatusStopping)

	d.cancel()

	for !d.Done() {
		time.Sleep(time.Second)
	}

	d.l.RLock()
	defer d.l.RUnlock()

	var err error
	for _, v := range d.resps {
		err = v.Cancel()
		err = v.Err()
	}
	time.Sleep(time.Second)
	return err
}

func (d *Downloader) WithProgressHook(hook func(current, total int64, err error)) {
	progressFunc := func() {
		defer close(d.progressDone)

		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for range ticker.C {
			status := atomic.LoadInt64(&d.status)
			if status != StatusStart && status != StatusEmpty {
				return
			}

			current := atomic.LoadInt64(&d.current)
			lastCurrent := atomic.LoadInt64(&d.lastCurrent)
			total := atomic.LoadInt64(&d.total)
			if current > total {
				current = total
			}
			if current == lastCurrent && current < total {
				continue
			}
			atomic.StoreInt64(&d.lastCurrent, current)

			select {
			case <-d.hasErr:
				d.progressHook(current, total, d.err)
				return
			default:
				d.progressHook(current, total, nil)
				if current == total {
					return
				}
			}
		}
	}

	if d.progressHook != nil {
		go progressFunc()
		return
	}
	d.progressHook = hook
	go progressFunc()
}

func (d *Downloader) WithErrHook(hook func(err error)) {
	go func() {
		err := d.Err()
		if err != nil {
			hook(err)
		}
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

func (d *Downloader) Wait() {
	for !d.Done() {
		time.Sleep(time.Second)
	}

	d.l.RLock()
	defer d.l.RUnlock()
	for _, v := range d.resps {
		v.Wait()
	}
	<-d.progressDone
}

func (d *Downloader) Err() error {
	for !d.Done() {
		time.Sleep(time.Second)
	}

	d.errOnce.Do(func() {
		go func() {
			errWg := &errgroup.Group{}
			d.l.RLock()
			for _, v := range d.resps {
				resp := v
				errWg.Go(func() error {
					return resp.Err()
				})
			}
			d.l.RUnlock()
			if atomic.CompareAndSwapInt64(&d.errChClosed, 0, 1) {
				d.err = errWg.Wait()
				close(d.hasErr)
			}
		}()
	})
	<-d.hasErr
	<-d.progressDone
	return d.err
}

func (d *Downloader) BytesPerSecond() float64 {
	var bps float64
	d.l.RLock()
	for _, v := range d.resps {
		bps += v.BytesPerSecond()
	}
	d.l.RUnlock()
	return bps
}

func (d *Downloader) Done() bool {
	return atomic.LoadInt64(&d.done) == 1
}

func (d *Downloader) IsRunning() bool {
	return atomic.LoadInt64(&d.status) == StatusStart
}

func (d *Downloader) clean() {
	atomic.StoreInt64(&d.current, 0)
	atomic.StoreInt64(&d.lastCurrent, 0)
	atomic.StoreInt64(&d.total, 0)
	atomic.StoreInt64(&d.errChClosed, 0)
	atomic.StoreInt64(&d.done, 0)
	d.l = sync.RWMutex{}
	d.lastBps = 0
	d.cancel = nil
	d.perSecondHookOnce = sync.Once{}
	d.errOnce = sync.Once{}
	d.err = nil
	d.hasErr = make(chan struct{})
	d.resps = make([]*Response, 0)
	d.progressDone = make(chan struct{})
}
