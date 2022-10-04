package paralleldownload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sync/errgroup"
)

type Worker struct {
	Url       string
	File      *os.File
	Count     int64
	TotalSize int64
}

// filename为文件存储的路径(可省略)+文件名
func downloadFile(url string, filename string) error {
	request, err := http.NewRequest("GET", url, nil)
	request.Header.Set("user-agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/96.0.4664.55 Safari/537.36")
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("访问url失败,err:%w", err)
	}
	defer resp.Body.Close()
	// 创建一个文件用于保存
	out, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer out.Close()
	//io.Copy() 方法将副本从 src 复制到 dst ，直到 src 达到文件末尾 ( EOF ) 或发生错误，
	//然后返回复制的字节数和复制时遇到的第一个错误(如果有)。
	//将响应流和文件流对接起来
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

// url为下载直链，若不支持多线程下载将尝试普通下载,file_path包含文件名
func ParallelDownload(download_url string, file_path string, worker_count int64) (err error) {
	if file_path == "" {
		file_path, err = getFileName(download_url)
		if err != nil {
			return err
		}
	}
	file_size, err := getSizeAndCheckRangeSupport(download_url)
	if err != nil {
		//不支持多线程下载，尝试普通下载
		return downloadFile(download_url, file_path)
	}
	if file_size <= 0 {
		return errors.New("get file size failed")
	}
	f, err := os.OpenFile(file_path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errGroup := new(errgroup.Group)
	// New worker struct to download file
	var worker = Worker{
		Url:       download_url,
		File:      f,
		Count:     worker_count,
		TotalSize: file_size,
	}
	var start, end int64
	var partial_size = int64(file_size / worker_count)
	for num := int64(0); num < worker.Count; num++ {
		if num == worker.Count-1 {
			end = file_size // last part
		} else {
			end = start + partial_size
		}
		tempNum := num
		tempStart := start
		tempEnd := end
		errGroup.Go(func() error {
			return worker.writeRange(ctx, cancel, tempNum, tempStart, tempEnd-1) // end-1
		})
		start = end
	}
	if err := errGroup.Wait(); err != nil {
		// 处理可能出现的错误
		return err
	}
	return nil
}

func (w *Worker) writeRange(ctx context.Context, cancel context.CancelFunc, part_num int64, start int64, end int64) error {
	var written int64
	body, size, err := w.getRangeBody(start, end)
	if err != nil {
		cancel()
		return fmt.Errorf("part %d request error: %w", part_num, err)
	}
	defer body.Close()
	// make a buffer to keep chunks that are read
	buf := make([]byte, 4*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		nr, err2 := body.Read(buf)
		if nr > 0 {
			nw, err := w.File.WriteAt(buf[0:nr], start)
			if err != nil {
				cancel()
				return fmt.Errorf("part %d write error: %w", part_num, err)
			}
			if nr != nw {
				cancel()
				return fmt.Errorf("part %d write error: %s", part_num, "short write")
			}
			start = int64(nw) + start
			if nw > 0 {
				written += int64(nw)
			}
		}
		if err2 != nil {
			if err2 == io.EOF {
				if size == written {
					// Download successfully
					return nil
				} else {
					cancel()
					return fmt.Errorf("part %d download error: %s", part_num, "size not match")
				}
			}
			cancel()
			return fmt.Errorf("part %d download error: %w", part_num, err2)
		}
	}
}

func (w *Worker) getRangeBody(start int64, end int64) (io.ReadCloser, int64, error) {
	var client http.Client
	req, err := http.NewRequest("GET", w.Url, nil)
	// req.Header.Set("cookie", "")
	// log.Printf("Request header: %s\n", req.Header)
	if err != nil {
		return nil, 0, err
	}
	// Set range header
	req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	size, err := strconv.ParseInt(resp.Header["Content-Length"][0], 10, 64)
	return resp.Body, size, err
}

func getSizeAndCheckRangeSupport(url string) (size int64, err error) {
	client := &http.Client{}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	// req.Header.Set("cookie", "")
	// log.Printf("Request header: %s\n", req.Header)
	res, err := client.Do(req)
	if err != nil {
		return
	}
	header := res.Header
	_, have := header["Content-Length"]
	if !have {
		err = errors.New("get file size failed")
		return
	}
	size, err = strconv.ParseInt(header["Content-Length"][0], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("get file size error: %w", err)
	}
	accept_ranges, supported := header["Accept-Ranges"]
	if !supported {
		return size, errors.New("doesn't support header `Accept-Ranges`")
	} else if supported && accept_ranges[0] != "bytes" {
		return size, errors.New("support `Accept-Ranges`, but value is not `bytes`")
	}
	return
}

func getFileName(download_url string) (string, error) {
	url_struct, err := url.Parse(download_url)
	if err != nil {
		return "", err
	}
	return filepath.Base(url_struct.Path), nil
}
