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
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

type worker struct {
	Url       string
	File      *os.File
	Count     int64
	TotalSize int64
}

// filename为文件名，savePath为文件存储的路径，两者都可省略。
func Download(url string, savePath string, filename string) error {
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
	name := generateDownloadFileName(url, resp.Header)
	if filename == "" {
		filename = name
	}
	filepath := filepath.Join(savePath, filename)
	// 创建一个文件用于保存
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return err
	}
	return nil
}

func generateDownloadFileName(url string, header http.Header) string {
	name := getFileNameByHeader(header)
	if name != "" {
		return name
	}
	name, err := getFileNameFromUrl(url)
	if err != nil || name == "" {
		return time.Now().Format("20060102150405") + "_unknown"
	}
	return name
}

// url为下载直链，若不支持多线程下载将尝试普通下载。
// filename为文件名，savePath为文件存储的路径，两者都可省略。
func ParallelDownload(download_url string, savePath string, filename string, worker_count int64) (err error) {
	file_size, header, err := getInfoAndCheckRangeSupport(download_url)
	if err != nil {
		fmt.Println("get file info failed:", err)
		//不支持多线程下载，尝试普通下载
		return Download(download_url, savePath, filename)
	}
	name := generateDownloadFileName(download_url, header)
	if filename == "" {
		filename = name
	}
	filePath := filepath.Join(savePath, filename)
	if file_size <= 0 {
		return errors.New("get file size failed")
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer f.Close()
	errGroup, ctx := errgroup.WithContext(context.Background())
	// New worker struct to download file
	var worker = worker{
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
			return worker.writeRange(ctx, tempNum, tempStart, tempEnd-1) // end-1
		})
		start = end
	}
	if err := errGroup.Wait(); err != nil {
		// 处理可能出现的错误
		return err
	}
	return nil
}

func (w *worker) writeRange(ctx context.Context, part_num int64, start int64, end int64) error {
	var written int64
	body, size, err := w.getRangeBody(start, end)
	if err != nil {
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
				return fmt.Errorf("part %d write error: %w", part_num, err)
			}
			if nr != nw {
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
					return fmt.Errorf("part %d download error: %s", part_num, "size not match")
				}
			}
			return fmt.Errorf("part %d download error: %w", part_num, err2)
		}
	}
}

func (w *worker) getRangeBody(start int64, end int64) (io.ReadCloser, int64, error) {
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

func getInfoAndCheckRangeSupport(url string) (size int64, header http.Header, err error) {
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
	header = res.Header
	_, have := header["Content-Length"]
	if !have {
		err = errors.New("get file size failed")
		return
	}
	size, err = strconv.ParseInt(header["Content-Length"][0], 10, 64)
	if err != nil {
		return 0, header, fmt.Errorf("get file size error: %w", err)
	}
	accept_ranges, supported := header["Accept-Ranges"]
	if !supported {
		return size, header, errors.New("doesn't support header `Accept-Ranges`")
	} else if supported && accept_ranges[0] != "bytes" {
		return size, header, errors.New("support `Accept-Ranges`, but value is not `bytes`")
	}
	return
}

func getFileNameFromUrl(download_url string) (string, error) {
	url_struct, err := url.Parse(download_url)
	if err != nil {
		return "", err
	}
	return filepath.Base(url_struct.Path), nil
}

func getFileNameByHeader(header http.Header) string {
	for k, v := range header {
		if strings.Contains(strings.ToLower(k), "filename") {
			return v[0]
		}
	}
	for k, v := range header {
		if strings.Contains(strings.ToLower(k), "content-disposition") {
			if strings.Contains(v[0], "filename=") {
				return v[0][strings.Index(v[0], "filename=")+9:]
			} else {
				return v[0]
			}
		}
	}
	for k, v := range header {
		if strings.Contains(strings.ToLower(k), "name") {
			return v[0]
		}
	}
	return ""
}
