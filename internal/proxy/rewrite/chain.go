package rewrite

import (
	"context"
	"net/http"
	"strings"
)

// PrefixChainHeader 在自指/嵌套链的**请求侧逐跳累加**对外前缀，
// 在**响应侧由最内层写回、逐跳向外传递**，让每一跳的改写器都知道完整前缀链。
//
// 为什么必须知道完整链：三跳链 /a → /b → /c → 后端，浏览器地址是 /a/b/c/page，
// 后端 HTML 里的 href="/assets/app.js" 最终必须落到 /a/b/c/assets/app.js。
// 若每跳只知道自己的前缀，最外层只会写 <base href="/a/">，资源就跑到 /a/assets，
// 404 一片 —— 这正是真机验收抓到的现象。
const PrefixChainHeader = "X-GatewayHub-Prefix-Chain"

type inboundChainKey struct{}

// WithInboundChain 把「本跳从客户端收到的前缀链」记进请求 context。
// 空串表示这是链路最外层（客户端直连本网关），响应侧据此决定是否摘掉内部头。
func WithInboundChain(req *http.Request, chain string) {
	if req == nil {
		return
	}
	*req = *req.WithContext(context.WithValue(req.Context(), inboundChainKey{}, chain))
}

// InboundChain 读取本跳收到的前缀链；非本网关写入的值一律忽略。
func InboundChain(req *http.Request) string {
	if req == nil {
		return ""
	}
	s, _ := req.Context().Value(inboundChainKey{}).(string)
	return s
}

// JoinChain 把本跳前缀接到入站链上；入站为空时本跳就是链首。
func JoinChain(inbound, prefix string) string {
	p := strings.Trim(strings.TrimSpace(prefix), "/")
	if p == "" {
		return inbound
	}
	if inbound == "" {
		return p
	}
	return inbound + "/" + p
}

// SanitizeChain 校验来自客户端的链值：只接受用 / 分隔的合法路径段。
//
// 这个头会参与 <base href> / Location / Cookie Path 的生成，绝不能让外部
// 塞进引号、尖括号或 ../ —— 不合法就整条丢弃，从本跳重新开始。
func SanitizeChain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 256 {
		return ""
	}
	parts := strings.Split(raw, "/")
	for i, p := range parts {
		if p == "" || p == "." || p == ".." || len(p) > 64 {
			return ""
		}
		for _, r := range p {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			case r == '-', r == '_', r == '.', r == '~':
			default:
				return ""
			}
		}
		parts[i] = strings.ToLower(p)
	}
	return strings.Join(parts, "/")
}
