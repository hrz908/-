// Package httpclient 统一上游 HTTP 客户端：一套调好的连接池参数，
// 各模块用 New(timeout) 拿自己的 Client，共享同一份 Transport 语义。
//
// 用 http.DefaultTransport 时 MaxIdleConnsPerHost 仅 2：批量打同一域名
// （腾讯行情、微信、企微）连接复用不足，TIME_WAIT 堆积、尾延迟上升。
// 这里统一修正，并补上默认未显式设置的拨号 / TLS 握手超时。
package httpclient

import (
	"net"
	"net/http"
	"time"
)

// NewTransport 生成调优过的 Transport：
//   - MaxIdleConns 200 / PerHost 64：上游域名很少，批量并发时空闲连接要够复用；
//   - IdleConnTimeout 90s：早于对端（LB/NAT 常见 60~90s）老化前主动回收；
//   - Dialer 5s / TLS 握手 5s：默认 30s/10s 对旁路数据类上游太宽松，
//     超时应尽早失败并由调用方降级，而不是挂着占资源。
func NewTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:          200,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// New 创建客户端；timeout <= 0 表示不设 Client.Timeout，
// 超时交给每次请求的 context（适合流式响应，Client.Timeout 会掐断长流）。
func New(timeout time.Duration) *http.Client {
	c := &http.Client{Transport: NewTransport()}
	if timeout > 0 {
		c.Timeout = timeout
	}
	return c
}
