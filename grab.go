package grab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

type BatchReq struct {
	Dst string
	Url string
}

// Get sends a HTTP request and downloads the content of the requested URL to
// the given destination file path. The caller is blocked until the download is
// completed, successfully or otherwise.
//
// An error is returned if caused by client policy (such as CheckRedirect), or
// if there was an HTTP protocol or IO error.
//
// For non-blocking calls or control over HTTP client headers, redirect policy,
// and other settings, create a Client instead.
func Get(dst, urlStr string) (*Response, error) {
	req, err := NewRequest(dst, urlStr)
	if err != nil {
		return nil, err
	}
	resp := DefaultClient.Do(req)
	return resp, resp.Err()
}

type DownloadOptions struct {
	workers             int
	retryTimes          int
	partSize            int64
	disablePartDownload bool
	writeHook           func(n int64)
}

type DownloadOptionFunc func(opt *DownloadOptions)

func WithDownloadWorkers(workers int) DownloadOptionFunc {
	return func(opt *DownloadOptions) {
		opt.workers = workers
	}
}

func WithRetryTimes(retryTimes int) DownloadOptionFunc {
	return func(opt *DownloadOptions) {
		opt.retryTimes = retryTimes
	}
}

func WithPartSize(partSize int64) DownloadOptionFunc {
	return func(opt *DownloadOptions) {
		opt.partSize = partSize
	}
}

func WithDisablePartDownload(disablePartDownload bool) DownloadOptionFunc {
	return func(opt *DownloadOptions) {
		opt.disablePartDownload = disablePartDownload
	}
}

func WithWriteHook(hook func(n int64)) DownloadOptionFunc {
	return func(opt *DownloadOptions) {
		opt.writeHook = hook
	}
}

// GetBatch sends multiple HTTP requests and downloads the content of the
// requested URLs to the given destination directory using the given number of
// concurrent worker goroutines.
//
// The Response for each requested URL is sent through the returned Response
// channel, as soon as a worker receives a response from the remote server. The
// Response can then be used to track the progress of the download while it is
// in progress.
//
// The returned Response channel will be closed by Grab, only once all downloads
// have completed or failed.
//
// If an error occurs during any download, it will be available via call to the
// associated Response.Err.
//
// For control over HTTP client headers, redirect policy, and other settings,
// create a Client instead.

type BatchResponse struct {
	ResCh  <-chan *Response
	Cancel context.CancelFunc
}

func GetBatch(reqParams []BatchReq, opts ...DownloadOptionFunc) (*BatchResponse, error) {
	opt := &DownloadOptions{
		workers:    10,
		retryTimes: 3,
		partSize:   32 * 1024 * 1024,
	}
	for _, v := range opts {
		v(opt)
	}

	ctx, cancel := context.WithCancel(context.Background())
	resp := &BatchResponse{
		Cancel: cancel,
	}

	reqs := make([]*Request, len(reqParams))
	for i := 0; i < len(reqs); i++ {
		req, err := NewRequest(reqParams[i].Dst, reqParams[i].Url)
		if err != nil {
			return nil, err
		}
		req.PartInfo = fmt.Sprintf("%s%s.%s.cp", filepath.Dir(reqParams[i].Dst), string(os.PathSeparator), filepath.Base(reqParams[i].Dst))
		req.DownloadOptions = opt
		reqs[i] = req
	}

	ch := DefaultClient.DoBatch(ctx, opt, reqs...)
	resp.ResCh = ch
	return resp, nil
}
