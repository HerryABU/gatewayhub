package proxy

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"gatewayhub/internal/isolation/cookie"
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
	// Self 表示 target 指向网关自身（自指路由）。仅用于告警与日志，
	// 不改变转发行为：合法性由 hopguard 的跳数上限保证。
	Self bool
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
	// 两个代理共享同一 Transport，复用后端连接池。
	//
	// TLSClientConfig 只在路由显式开启 TLSSkipVerify 时设置：宝塔面板 / WSL 内网服务
	// 用自签名证书，默认校验会直接 502（而健康检查的 TCP 兜底还会误报 healthy）。
	// 注意这是「网关→后端」这一段，浏览器→网关的 TLS 不受影响；
	// 默认仍严格校验，避免把正常外站降级成可被中间人攻击。
	transport := &http.Transport{
		TLSClientConfig: func() *tls.Config {
			if !r.TLSSkipVerify {
				return nil
			}
			return &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}
		}(),
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
	}
	entry := &Entry{Route: r, Parsed: p}
	entry.Self = target.IsSelf(p, m.self.Port, m.baseDomain)

	// 路径模式：剥离 /prefix 前缀 + 响应内容重写（加回前缀、注入 <base>）
	// 开启 SDKOriginAuth 时把原站 origin 交给 shim：第三方 OAuth 回调（redirect_uri）
	// 换成原站域名，让微信/Apple 开放平台的白名单通过；登录在原站完成。
	rw := rewrite.New(r.Prefix, p.Root, p.Host)
	if r.SDKOriginAuth && p.Scheme != "" && p.Host != "" {
		rw = rewrite.NewWithOrigin(r.Prefix, p.Root, p.Host, p.Scheme+"://"+p.Host)
	}
	if r.WXAppid != "" {
		rw.SetWXAppid(r.WXAppid)
	}
	// 通用 OAuth 回调域名映射：校验为合法 JSON 对象后才注入 shim。
	if r.OAuthRedirectMap != "" && json.Valid([]byte(r.OAuthRedirectMap)) {
		rw.SetOAuthRedirectMap(r.OAuthRedirectMap)
	}
	// 完全透传：关闭所有改写/注入。
	if r.Passthrough {
		rw.SetPassthrough(true)
	}
	// 通用删除规则：解析 {"headers":[...],"cookies":[...]}。
	if r.ResponseStrip != "" {
		var strip struct {
			Headers []string `json:"headers"`
			Cookies []string `json:"cookies"`
		}
		if json.Unmarshal([]byte(r.ResponseStrip), &strip) == nil {
			rw.SetStripRules(strip.Headers, strip.Cookies)
		}
	}
	entry.Proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// 记录网关侧原始路径（外链 CSS 相对化需要推算基准目录）
			req = rewrite.WithGWPath(req, req.URL.Path)
			// 嵌套链：把对外前缀逐跳累加，最内层据此算出完整链
			// （/a → /b → /c 三层时，后端资源必须落在 /a/b/c/ 下）。
			inbound := rewrite.SanitizeChain(rewrite.TrustedChain(req))
			rewrite.WithInboundChain(req, inbound)
			req.Header.Set(rewrite.PrefixChainHeader, rewrite.JoinChain(inbound, r.Prefix))
			rest := sanitizeRest(req.URL.Path, r.Prefix)
			applyForwardHeaders(req, p)
			req.URL.Path = target.JoinPath(p.Root, rest)
			// 完整 cookie 隔离：请求侧只脱本服务帽，别家帽子按策略丢弃/保留。
			if r.CookieHat != "" {
				cookie.RewriteRequest(req.Header, r.CookieHat, r.DropForeign)
			}
			// 仅接受 gzip，便于在 ModifyResponse 中解压重写后再压缩
			req.Header.Set("Accept-Encoding", "gzip")
		},
		Transport: transport,
		ModifyResponse: func(resp *http.Response) error {
			var cookies []string
			if r.CookieHat != "" {
				cookies = resp.Header.Values("Set-Cookie")
				resp.Header.Del("Set-Cookie")
			}
			if err := rw.Modify(resp); err != nil {
				return err
			}
			if len(cookies) == 0 {
				return nil
			}
			res := cookie.RewriteSetCookies(cookies, cookie.SetCookieOptions{
				Hat: r.CookieHat, Prefix: "/" + r.Prefix, BackendRoot: p.Root,
			})
			for _, v := range res.Values {
				resp.Header.Add("Set-Cookie", v)
			}
			return nil
		},
		FlushInterval: -1,
		BufferPool:    poolBuffer{},
		ErrorHandler:  m.errorHandler,
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
