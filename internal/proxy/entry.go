package proxy

import (
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"gatewayhub/internal/models"
	"gatewayhub/internal/proxy/rewrite"
	"gatewayhub/internal/target"
)

// bufferPool 复用 io.Copy 缓冲区，减少高并发下的内存分配与 GC 压力。
var bufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

type poolBuffer struct{}

func (poolBuffer) Get() []byte  { return *bufferPool.Get().(*[]byte) }
func (poolBuffer) Put(b []byte) { bufferPool.Put(&b) }

// Entry 路由表条目：同一后端地址共享一个 Transport 连接池，
// 路径模式（Proxy，剥离前缀 + 响应重写）与子域名模式（SubProxy，零重写纯透明）各自独立的 ReverseProxy。
type Entry struct {
	Route    models.Route
	Parsed   target.Parsed
	Proxy    *httputil.ReverseProxy // 路径模式
	SubProxy *httputil.ReverseProxy // 子域名模式
}

func (m *Manager) buildEntry(r models.Route) (*Entry, error) {
	p, err := target.Parse(r.Target)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(r.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// 两个代理共享同一 Transport，复用后端连接池
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
	}
	entry := &Entry{Route: r, Parsed: p}

	// 路径模式：剥离 /prefix 前缀 + 响应内容重写（加回前缀、注入 <base>）
	rw := rewrite.New(r.Prefix, p.Root, p.Host)
	entry.Proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// 记录网关侧原始路径（外链 CSS 相对化需要推算基准目录）
			req = rewrite.WithGWPath(req, req.URL.Path)
			rest := sanitizeRest(req.URL.Path, r.Prefix)
			applyForwardHeaders(req, p)
			req.URL.Path = target.JoinPath(p.Root, rest)
			// 仅接受 gzip，便于在 ModifyResponse 中解压重写后再压缩
			req.Header.Set("Accept-Encoding", "gzip")
		},
		Transport:      transport,
		ModifyResponse: rw.Modify,
		FlushInterval:  -1,
		BufferPool:     poolBuffer{},
		ErrorHandler:   m.errorHandler,
	}

	// 子域名模式：前缀在 Host 头里，路径即后端根路径，零重写、零解压，纯透明转发（速度=源站）
	entry.SubProxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			applyForwardHeaders(req, p)
			req.URL.Path = target.JoinPath(p.Root, req.URL.Path)
			// 不设置 Accept-Encoding：保留原始值，让后端按需 gzip/br，网关零解压透传
		},
		Transport:     transport,
		FlushInterval: -1,
		BufferPool:    poolBuffer{},
		ErrorHandler:  m.errorHandler,
	}
	return entry, nil
}
