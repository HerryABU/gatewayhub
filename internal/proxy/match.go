package proxy

import (
	"net"
	"strings"

	"gatewayhub/internal/target"
)

// matchSubdomain 从 Host 头提取单段子域名前缀，如 "java-order.localhost[:8088]" → "java-order"。
func (m *Manager) matchSubdomain(host string) (string, bool) {
	if m.baseDomain == "" {
		return "", false
	}
	h := stripHostPort(host)
	suffix := "." + m.baseDomain
	if !strings.HasSuffix(strings.ToLower(h), strings.ToLower(suffix)) {
		return "", false
	}
	prefix := h[:len(h)-len(suffix)]
	if prefix == "" || strings.Contains(prefix, ".") {
		return "", false // 仅支持单段子域名
	}
	return strings.ToLower(prefix), true
}

// matchPath 最长前缀匹配：多级前缀（含 /）优先，其次第一段精确匹配（O(1) 快速路径）。
func (m *Manager) matchPath(path string) (string, *Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// 多级前缀：长度降序，段边界对齐（最长优先）
	for _, p := range m.multi {
		if matchesPrefix(path, p) {
			if e, ok := m.m[p]; ok {
				return p, e, true
			}
		}
	}
	// 单段前缀：第一段精确匹配
	first := FirstSegment(path)
	if e, ok := m.m[first]; ok {
		return first, e, true
	}
	return "", nil, false
}

// stripHostPort 去掉 Host 头中的端口部分（无端口则原样返回）。
func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// matchTargetHost 用请求 Host 精确匹配 active 路由的 target host。
// iframe 透传的基础：浏览器用官方域名（DNS/Hosts 指向网关）访问时，
// 网关据此识别归属路由，让 iframe 内的 location.hostname 就是官方域名 ——
// 所有按 hostname 判环境的 SDK（微信/QQ/飞书/字节等）天然走生产配置。
func (m *Manager) matchTargetHost(host string) (string, *Entry, bool) {
	h := strings.ToLower(stripHostPort(host))
	if h == "" || h == "localhost" || h == "127.0.0.1" {
		return "", nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for prefix, e := range m.m {
		if e.Route.Status != "active" {
			continue
		}
		p, err := target.Parse(e.Route.Target)
		if err != nil || p.Host == "" {
			continue
		}
		if strings.EqualFold(stripHostPort(p.Host), h) {
			return prefix, e, true
		}
	}
	return "", nil, false
}

// MatchTargetHost 是 matchTargetHost 的导出入口，供 router NoRoute 使用。
func (m *Manager) MatchTargetHost(host string) (string, *Entry, bool) {
	return m.matchTargetHost(host)
}
