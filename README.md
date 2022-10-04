# ParallelDownload


Golang HTTP Downloader using Ranges for parallel downloads.



## Overview



```go
func ParallelDownload(download_url string, file_path string, worker_count int64) (err error)
```



## QuickStart

```go
go get -u github.com/Doraemonkeys/ParallelDownload
```

```go
package main

import (
	"fmt"

	pd "github.com/Doraemonkeys/ParallelDownload"
)

func main() {
	var worker int64 = 5
	err := pd.ParallelDownload("https://XXX/XXX.XX", "path+name", worker)
	if err != nil {
		fmt.Println("download failed:", err)
		return
	}
	fmt.Println("download success")
}
```

