package proxy

import (
	"net"
	"strings"
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
