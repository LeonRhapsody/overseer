package main

import (
	"compress/gzip"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

var GlobalTransport *http.Transport

//应该定义一个全局的 transport 结构体, 在多个 goroutine 之间共享.否则会占用大量的open files，引发socket: too many open files

func init() { //忽略证书检验
	GlobalTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		//ResponseHeaderTimeout: 2 * time.Second,//限制读取response header的时间
		Dial: (&net.Dialer{
			Timeout: 3 * time.Second, //限制建立tcp连接的时间
			//KeepAlive: 30 * time.Second,
		}).Dial,
	}

}

type HTTP struct {
	//URL to poll for new binaries
	URL          string
	Interval     time.Duration
	CheckHeaders []string
	//internal state
	delay bool
	lasts map[string]string
}

//if any of these change, the binary has been updated
var defaultHTTPCheckHeaders = []string{"ETag", "If-Modified-Since", "Last-Modified", "Content-Length"}

// Init validates the provided config
func (h *HTTP) Init() error {
	//apply defaults
	if h.URL == "" {
		return fmt.Errorf("URL required")
	}
	h.lasts = map[string]string{}
	if h.Interval == 0 {
		h.Interval = 5 * time.Minute
	}
	if h.CheckHeaders == nil {
		h.CheckHeaders = defaultHTTPCheckHeaders
	}
	return nil
}

// Fetch the binary from the provided URL
func (h *HTTP) Fetch() (io.Reader, error) {
	client := http.Client{Transport: GlobalTransport} //忽略证书检验
	//delay fetches after first
	if h.delay {
		time.Sleep(h.Interval)
	}
	h.delay = true
	//status check using HEAD

	resp, err := client.Head(h.URL)
	if err != nil {
		return nil, fmt.Errorf("HEAD request failed (%s)", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HEAD request failed (status code %d)", resp.StatusCode)
	}

	//if all headers match, skip update
	matches, total := 0, 0
	for _, header := range h.CheckHeaders {
		if curr := resp.Header.Get(header); curr != "" { //匹配header中描述的文件信息
			if last, ok := h.lasts[header]; ok && last == curr {
				matches++
			}
			h.lasts[header] = curr
			total++
		}
	}
	if matches == total {
		return nil, nil //skip, file match
	}
	log.Println("发现新文件，触发更新")
	//binary fetch using GET
	resp, err = client.Get(h.URL)

	if err != nil {
		return nil, fmt.Errorf("GET request failed (%s)", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET request failed (status code %d)", resp.StatusCode)
	}
	//extract gz files
	if strings.HasSuffix(h.URL, ".gz") && resp.Header.Get("Content-Encoding") != "gzip" {
		return gzip.NewReader(resp.Body)
	}
	//success!
	return resp.Body, nil
}
