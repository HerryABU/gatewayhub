package proxy

import (
	"net"
	"net/http"
	"path"
	"strings"

	"gatewayhub/internal/target"
)

// applyForwardHeaders 设置后端目标并透传/注入转发头（两模式共用）
func applyForwardHeaders(req *http.Request, p target.Parsed) {
	req.URL.Scheme = p.Scheme
	req.URL.Host = p.Host
	req.Host = p.Host

	clientIP := clientIPFrom(req.RemoteAddr, req.Header)
	if prior := req.Header.Get("X-Forwarded-For"); prior != "" {
		req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
	} else {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	req.Header.Set("X-Real-IP", clientIP)
	proto := "http"
	if req.TLS != nil {
		proto = "https"
	}
	req.Header.Set("X-Forwarded-Proto", proto)
	// 完全透明：清除前缀痕迹，后端感知不到 /prefix 的存在
	req.Header.Del("X-Forwarded-Prefix")
}

// FirstSegment 提取 URL 第一段路径作为转发名
func FirstSegment(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if i := strings.Index(trimmed, "/"); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

// matchesPrefix 判断 path 是否命中 prefix（完整路径段边界：/prefix 或 /prefix/...）。
func matchesPrefix(path, prefix string) bool {
	full := "/" + prefix
	if !strings.HasPrefix(path, full) {
		return false
	}
	rest := strings.TrimPrefix(path, full)
	return rest == "" || rest[0] == '/'
}

// sanitizeRest 剥离路由前缀并清理路径，防止 `..` 路径穿透。
func sanitizeRest(pathStr, prefix string) string {
	if !matchesPrefix(pathStr, prefix) {
		return "/"
	}
	rest := strings.TrimPrefix(pathStr, "/"+prefix)
	hadTrailing := strings.HasSuffix(rest, "/")
	cleaned := path.Clean("/" + rest)
	if hadTrailing && cleaned != "/" {
		cleaned += "/"
	}
	return cleaned
}

// clientIPFrom 从 RemoteAddr 与请求头提取真实 IP
func clientIPFrom(remoteAddr string, header http.Header) string {
	if xff := header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if xri := header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
