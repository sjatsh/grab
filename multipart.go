package grab

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hashicorp/go-cleanhttp"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
)

var ErrForbidden = errors.New("server returned 403 Forbidden")

type Chunk struct {
	sync.RWMutex
	Number    int   `json:"number,omitempty"`
	OffSet    int64 `json:"offSet,omitempty"`
	Size      int64 `json:"size,omitempty"`
	Done      bool  `json:"done,omitempty"`
	Completed int64 `json:"completed,omitempty"`
	transfer  *transfer
}

type RangeOptions struct {
	Start int64 `json:"start,omitempty"`
	End   int64 `json:"end,omitempty"`
}

type MultiCPInfo struct {
	Size             int64                    `json:"s,omitempty"`
	LastModified     string                   `json:"lm,omitempty"`
	DownloadedBlocks map[int]*DownloadedBlock `json:"dbs,omitempty"`
}

type DownloadedBlock struct {
	From      int64 `json:"f,omitempty"`
	To        int64 `json:"t,omitempty"`
	Completed int64 `json:"c,omitempty"`
}

type Jobs struct {
	Url      string
	FilePath string
	Chunk    *Chunk
	Range    string
}

type Results struct {
	PartNumber int
	body       io.ReadCloser
	fd         *os.File
	transfer   *transfer
	err        error
	done       bool
	dump       bool
	completed  int64
}

func FormatRangeOptions(opt *RangeOptions) string {
	if opt == nil {
		return ""
	}
	return fmt.Sprintf("bytes=%v-%v", opt.Start, opt.End)
}

// DividePart 根据文件大小计算分片数量和大小
// fileSize: 文件大小
// last: 分片大小(MB)
func DividePart(fileSize int64, last int) (int64, int64) {
	partSize := int64(last * 1024 * 1024)
	partNum := fileSize / partSize
	for partNum >= 10000 {
		partSize = partSize * 2
		partNum = fileSize / partSize
	}
	return partNum, partSize
}

// SplitSizeIntoChunks 根据文件大小和分片大小计算分片
func SplitSizeIntoChunks(totalBytes int64, partSizes ...int64) ([]*Chunk, int, error) {
	var partSize int64
	var partNum int64
	if len(partSizes) > 0 && partSizes[0] > 0 {
		partSize = partSizes[0]
		if partSize < 1024*1024 {
			return nil, 0, errors.New("partSize>=1048576 is required")
		}
		partNum = totalBytes / partSize
	} else {
		partNum, partSize = DividePart(totalBytes, 16)
	}

	if partNum >= 10000 {
		return SplitSizeIntoChunks(totalBytes, partSize*2)
	}

	var chunks []*Chunk
	for i := int64(0); i < partNum; i++ {
		chunks = append(chunks, &Chunk{
			Number: int(i + 1),
			OffSet: i * partSize,
			Size:   partSize,
		})
	}

	if totalBytes%partSize > 0 {
		chunks = append(chunks, &Chunk{
			Number: len(chunks) + 1,
			OffSet: int64(len(chunks)) * partSize,
			Size:   totalBytes % partSize,
		})
		partNum++
	}
	return chunks, int(partNum), nil
}

// checkDownloadedParts 对已下载分片检查
func checkDownloadedParts(opt *MultiCPInfo, cfFile string, chunks []*Chunk) (bool, error) {
	fd, err := os.Open(cfFile)
	// checkpoint 文件不存在
	if err != nil && os.IsNotExist(err) {
		// 创建 checkpoint 文件
		fd, err := os.OpenFile(cfFile, os.O_RDONLY|os.O_CREATE|os.O_TRUNC, 0660)
		if err != nil {
			return false, err
		}
		_ = fd.Close()
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		_ = fd.Close()
	}()

	// 获取保存的checkpoint信息
	if err := json.NewDecoder(fd).Decode(opt); err != nil {
		return false, nil
	}
	// 判断文件是否发生变化|没有下载完成的分片，如果发生变化重新下载
	if opt.Size != opt.Size || opt.LastModified != opt.LastModified || len(opt.DownloadedBlocks) == 0 {
		return false, nil
	}

	// len(chunks) 大于1，否则为简单下载, chunks[0].Size为partSize
	partSize := chunks[0].Size
	for _, v := range opt.DownloadedBlocks {
		index := v.From / partSize
		chunk := chunks[index]
		chunk.Completed = v.Completed
		to := chunk.OffSet + chunk.Size - 1
		if chunk.OffSet != v.From || to != v.To || chunk.Completed > chunk.Size || chunk.Completed < 0 {
			// 重置chunks，重新在下所有分片
			for i := range chunks {
				chunks[i].Done = false
			}
			return false, nil
		}
		if chunk.Completed == chunk.Size {
			chunks[index].Done = true
		}
	}
	return true, nil
}

func downloadWorker(resp *Response, jobs <-chan *Jobs, results chan<- *Results) {
	for {
		select {
		case <-resp.ctx.Done():
			return
		default:
			j, ok := <-jobs
			if !ok {
				return
			}
			opt := &RangeOptions{
				Start: j.Chunk.OffSet + j.Chunk.Completed,
				End:   j.Chunk.OffSet + j.Chunk.Size - 1,
			}
			j.Range = FormatRangeOptions(opt)

			res := &Results{}
			for i := 1; i <= resp.downloadOptions.retryTimes; i++ {
				if download(resp, res, i == 1, i == resp.downloadOptions.retryTimes, j, results) == nil {
					break
				}
			}
		}
	}
}

func download(resp *Response, res *Results, firstTime, lastTime bool, j *Jobs, results chan<- *Results) error {
	var err error
	var httpResp *http.Response
	res.PartNumber = j.Chunk.Number
	httpResp, err = doHttpReq(http.MethodGet, j.Url, map[string][]string{
		"Range": {j.Range},
	})
	if err != nil {
		return err
	}
	if httpResp.StatusCode == http.StatusForbidden {
		res.err = ErrForbidden
		if firstTime {
			results <- res
		}
		return nil
	}
	if err != nil {
		res.err = err
		return err
	}
	res.body = httpResp.Body

	fd, err := os.OpenFile(j.FilePath, os.O_WRONLY, 0660)
	if err != nil {
		res.err = err
		return err
	}
	if _, err = fd.Seek(j.Chunk.OffSet, io.SeekStart); err != nil {
		res.err = err
		return err
	}
	res.fd = fd

	cleanStatus := func() {
		if res.err != nil && !lastTime {
			res.err = nil
			res.done = false
			if res.body != nil {
				_ = res.body.Close()
				res.body = nil
			}
			if res.fd != nil {
				_ = res.fd.Close()
				res.fd = nil
			}
		}
	}

	j.Chunk.Lock()
	tf := newTransfer(resp.ctx, nil, NewFileWriter(fd, res, resp.downloadOptions.writeHook), LimitReadCloser(httpResp.Body, j.Chunk.Size), nil)
	j.Chunk.transfer = tf
	j.Chunk.Unlock()
	if firstTime {
		cleanStatus()
		results <- res
	}

	n, err := j.Chunk.transfer.copy()
	select {
	case <-resp.ctx.Done():
		return nil
	default:
	}

	needSize := j.Chunk.Size - j.Chunk.Completed
	if n != needSize && err == nil {
		// 说明链接失效了
		res.err = ErrForbidden
		return nil
	}

	copyErr := n != needSize || err != nil
	if lastTime || !copyErr {
		res.done = true
		j.Chunk.Done = true
	}
	if copyErr {
		res.err = fmt.Errorf("io.Copy Failed, nread:%v, want:%v, err:%v", n, j.Chunk.Size, err)
		cleanStatus()
		return res.err
	}
	return nil
}

func LimitReadCloser(r io.Reader, n int64) io.ReadCloser {
	var lc LimitedReadCloser
	lc.R = r
	lc.N = n
	return &lc
}

type FileWriter struct {
	io.WriteCloser
	res       *Results
	writeHook func(n int64)
}

func NewFileWriter(w io.WriteCloser, res *Results, writeHook func(n int64)) *FileWriter {
	return &FileWriter{
		WriteCloser: w,
		res:         res,
		writeHook:   writeHook,
	}
}

func (w *FileWriter) Write(p []byte) (int, error) {
	n, err := w.WriteCloser.Write(p)
	if err != nil {
		return n, err
	}
	atomic.AddInt64(&w.res.completed, int64(n))
	if w.writeHook != nil {
		w.writeHook(int64(n))
	}
	return n, err
}

type LimitedReadCloser struct {
	io.LimitedReader
}

func (lc *LimitedReadCloser) Close() error {
	if r, ok := lc.R.(io.ReadCloser); ok {
		return r.Close()
	}
	return nil
}

// httpClient is the default client to be used by HttpGetters.
var httpClient = cleanhttp.DefaultClient()

func doHttpReq(method, url string, headers ...map[string][]string) (*http.Response, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return nil, err
	}
	if len(headers) > 0 {
		for k, vs := range headers[0] {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	}
	return httpClient.Do(req)
}
