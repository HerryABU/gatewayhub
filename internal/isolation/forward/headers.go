package forward

import (
	"net"
	"net/http"
	"strings"
)

// Headers 是注入到后端请求的转发头集合（FR-7）。
type Headers struct {
	ClientIP   string // 客户端 IP
	OriginHost string // 浏览器请求的 Host（代理域名）
	Proto      string // 原始协议：http / https
	Prefix     string // /{服务名}
	// PriorXFF 是入站请求携带的上游转发链（Rewrite 模式下标准库已从出站请求剥离，
	// 需要显式传回以便「追加而非覆盖」）。
	PriorXFF string
}

// Apply 把转发头写入请求。语义要点：
//   - X-Forwarded-For 追加而非覆盖（保留上游代理链，便于溯源）；
//   - X-Real-IP / X-Forwarded-Host / X-Forwarded-Proto / X-Forwarded-Prefix 覆盖写入，
//     不接受客户端自带值（防止伪造归属，NFR-5）。
func Apply(req *http.Request, h Headers) {
	if h.ClientIP != "" {
		switch {
		case h.PriorXFF != "":
			req.Header.Set("X-Forwarded-For", h.PriorXFF+", "+h.ClientIP)
		case req.Header.Get("X-Forwarded-For") != "":
			req.Header.Set("X-Forwarded-For", req.Header.Get("X-Forwarded-For")+", "+h.ClientIP)
		default:
			req.Header.Set("X-Forwarded-For", h.ClientIP)
		}
		req.Header.Set("X-Real-IP", h.ClientIP)
	}
	if h.OriginHost != "" {
		req.Header.Set("X-Forwarded-Host", h.OriginHost)
	}
	if h.Proto != "" {
		req.Header.Set("X-Forwarded-Proto", h.Proto)
	}
	if h.Prefix != "" {
		req.Header.Set("X-Forwarded-Prefix", h.Prefix)
	}
}

// ClientIP 提取客户端 IP。
//
// trust=true（前面确实有可信反代）时优先 X-Forwarded-For 第一段 / X-Real-IP；
// trust=false（网关直接对外）时只认 RemoteAddr —— 转发头可由客户端随意伪造，
// 用它做限流/循环检测会被绕过（NFR-5）。
func ClientIP(remoteAddr string, header http.Header, trust bool) string {
	if trust {
		if xff := header.Get("X-Forwarded-For"); xff != "" {
			if ip := strings.TrimSpace(strings.Split(xff, ",")[0]); ip != "" {
				return ip
			}
		}
		if xri := strings.TrimSpace(header.Get("X-Real-IP")); xri != "" {
			return xri
		}
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// Proto 返回原始协议。
func Proto(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	if p := req.Header.Get("X-Forwarded-Proto"); p != "" {
		return p
	}
	return "http"
}

// IsUpgrade 判断是否为协议升级（WebSocket 等），升级连接不受请求超时限制。
func IsUpgrade(req *http.Request) bool {
	return strings.EqualFold(req.Header.Get("Connection"), "Upgrade") && req.Header.Get("Upgrade") != ""
}
