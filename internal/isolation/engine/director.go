package engine

import (
	"net/http/httputil"
	"strings"

	"gatewayhub/internal/isolation/config"
	"gatewayhub/internal/isolation/forward"
	"gatewayhub/internal/proxy/rewrite"
)

// rewrite 是请求侧的「拦截—修正」：路径、Host、转发头、Cookie 脱帽一次性完成
// （FR-5~FR-9）。每个请求只做一次，绝不因响应内容二次发起请求（FR-9）。
//
// 用 httputil.ProxyRequest（Rewrite 钩子）而不是旧的 Director：
//   - 标准库会先剥离客户端自带的 Forwarded / X-Forwarded-*，避免伪造归属；
//   - Director 模式下标准库还会再自动追加一次 X-Forwarded-For，与我们自己的追加叠加，
//     导致链上出现重复 IP。Rewrite 模式下转发头完全由这里决定，行为确定。
func (rt *runtime) rewrite(pr *httputil.ProxyRequest) {
	st, ok := pr.Out.Context().Value(stateKey{}).(*requestState)
	if !ok {
		return
	}
	cfg := st.cfg
	out := pr.Out

	// 1) 路径：按模式剥离/保留前缀，再拼上后端根路径
	out.URL.Path = joinRoot(cfg.Root, forward.Path(cfg.Mode, cfg.Prefix, st.rest))
	out.URL.RawPath = ""

	// 2) 目标后端
	out.URL.Scheme = cfg.Scheme
	out.URL.Host = cfg.Host

	// 3) Host 头策略（FR-8）：keep 保留代理域名，rewrite 改为后端地址
	if cfg.HostMode == config.HostModeRewrite {
		out.Host = cfg.Host
	} else {
		out.Host = pr.In.Host
	}

	// 4) 转发头：XFF 在「入站已有值」后追加客户端 IP（不覆盖上游链），
	//    其余归属头覆盖写入，不接受客户端伪造值。
	forward.Apply(out, forward.Headers{
		ClientIP:   st.clientIP,
		OriginHost: st.origin,
		Proto:      st.proto,
		Prefix:     st.prefix,
		PriorXFF:   pr.In.Header.Get("X-Forwarded-For"),
	})

	// 5) Cookie 脱帽（只脱本服务的帽子）
	res := cookieRewrite(out, cfg)
	if res.Unhatted > 0 {
		st.counters.CookieRewrites.Add(int64(res.Unhatted))
	}
	if res.Dropped > 0 {
		st.counters.CookiesDropped.Add(int64(res.Dropped))
	}

	// 6) 嵌套链：把对外前缀逐跳累加（与普通反代共用同一个头）。
	//    最内层据此把 <base>/资源路径写到完整链前缀下，避免多跳后资源 404。
	inbound := rewrite.SanitizeChain(rewrite.TrustedChain(pr.In))
	rewrite.WithInboundChain(pr.Out, inbound)
	pr.Out.Header.Set(rewrite.PrefixChainHeader,
		rewrite.JoinChain(inbound, strings.Trim(cfg.Prefix, "/")))

	// 7) 需要改写响应体时只要 gzip：便于解码改写后以明文输出（避免压缩乱码）
	if cfg.BodyRewriteEnabled() {
		out.Header.Set("Accept-Encoding", "gzip")
	}
}

// joinRoot 拼接后端根路径与转发路径（root 为空时原样返回）。
func joinRoot(root, p string) string {
	if root == "" {
		return p
	}
	if p == "/" {
		return root + "/"
	}
	return root + p
}
