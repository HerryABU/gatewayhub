package engine

import (
	"bytes"
	"io"
	"net/http"
	"strconv"

	"gatewayhub/internal/isolation/config"
	"gatewayhub/internal/isolation/diag"
	"gatewayhub/internal/isolation/forward"
	"gatewayhub/internal/isolation/metrics"
	"gatewayhub/internal/isolation/respond"
)

// modifyResponse 是响应侧的「修正」：Location、Set-Cookie、可选的字面量改写
// （FR-10~FR-13）。只修正一次，绝不对后端发起二次请求，也不产生代理内部回环。
//
// 全部使用「请求发出时的配置快照」（st.cfg）而非当前配置：
// 即使运维在请求飞行途中切换模式，单个请求的处理仍是确定的（NFR-2）。
func (rt *runtime) modifyResponse(resp *http.Response) error {
	cfg := rt.service()
	cnt := rt.counters
	if st, ok := stateFrom(resp); ok {
		if st.cfg != nil {
			cfg = st.cfg
		}
		if st.counters != nil {
			cnt = st.counters
		}
	}

	// 熔断结算：5xx 视为后端失败，其余视为后端仍可用
	if resp.StatusCode >= 500 {
		rt.tripOnFailure()
	} else {
		rt.breaker.Success()
	}

	// 1) Location（重定向不跳出前缀）
	if loc := resp.Header.Get("Location"); loc != "" {
		next, changed := respond.RewriteLocation(loc, respond.LocationOptions{
			Mode:           cfg.Mode,
			Prefix:         cfg.Prefix,
			BackendHost:    cfg.Host,
			FixPassThrough: cfg.PassThroughFix,
		})
		if changed {
			resp.Header.Set("Location", next)
			cnt.LocationRewrites.Add(1)
		}
	}

	// 2) Set-Cookie（加帽 + Path 收紧）
	rt.rewriteCookies(resp, cfg, cnt)

	// 3) 响应体字面量改写（仅 strip + 开关打开；isolated/pass-through 永不改 body）
	if !cfg.BodyRewriteEnabled() || !bodyEligible(resp) {
		return nil
	}
	kind := respond.DetectKind(resp.Header.Get("Content-Type"))
	if kind == respond.KindOther {
		return nil
	}
	enc := resp.Header.Get("Content-Encoding")
	if !respond.Decodable(enc) {
		cnt.BodySkipped.Add(1)
		diag.Logf(diag.RewriteFailed, cfg.Name, "响应编码 %q 不支持改写，已原样透传: %s", enc, requestPath(resp))
		return nil
	}
	if resp.ContentLength > cfg.MaxRewriteBytes {
		cnt.BodySkipped.Add(1)
		diag.Logf(diag.RewriteFailed, cfg.Name, "响应体 %d 字节超过改写上限 %d，已原样透传: %s",
			resp.ContentLength, cfg.MaxRewriteBytes, requestPath(resp))
		return nil
	}
	rt.rewriteBody(resp, cfg, cnt, kind, enc)
	return nil
}

// bodyEligible 判断该响应是否值得读入改写（HEAD/204/304 无实体）。
func bodyEligible(resp *http.Response) bool {
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotModified {
		return false
	}
	return resp.Request == nil || resp.Request.Method != http.MethodHead
}

// rewriteBody 读入（必要时解压）→ 字面量改写 → 明文写出。
// 任何一步失败都恢复原始字节原样透传：代理宁可不改，也不能把页面改坏。
func (rt *runtime) rewriteBody(resp *http.Response, cfg *config.Service, cnt *metrics.Counters, kind respond.Kind, enc string) {
	// 先按上限多读 1 字节判断是否超限；超限时把已读片段与剩余流拼回去，绝不截断响应
	buf, err := io.ReadAll(io.LimitReader(resp.Body, cfg.MaxRewriteBytes+1))
	if err != nil {
		cnt.HTMLFailures.Add(1)
		diag.Logf(diag.RewriteFailed, cfg.Name, "读取响应体失败: %v", err)
		// 已读片段 + 剩余流交回，交给 ReverseProxy 原样输出（不关闭底层连接）
		resp.Body = &multiReadCloser{Reader: io.MultiReader(bytes.NewReader(buf), resp.Body), closer: resp.Body}
		return
	}
	if int64(len(buf)) > cfg.MaxRewriteBytes {
		cnt.BodySkipped.Add(1)
		diag.Logf(diag.RewriteFailed, cfg.Name, "响应体超过改写上限 %d 字节，流式原样透传: %s",
			cfg.MaxRewriteBytes, requestPath(resp))
		// 已读部分 + 剩余流一起交回，保持字节完整
		resp.Body = &multiReadCloser{Reader: io.MultiReader(bytes.NewReader(buf), resp.Body), closer: resp.Body}
		return
	}
	origin := resp.Body
	decoded, _, decErr := respond.Decode(buf, enc)
	if decErr != nil {
		cnt.HTMLFailures.Add(1)
		diag.Logf(diag.RewriteFailed, cfg.Name, "响应体解压失败（%s），原样透传: %v", enc, decErr)
		restoreBody(resp, buf, origin)
		return
	}
	out, hits := respond.RewriteLiterals(decoded, kind, cfg.Prefix)
	if hits == 0 {
		// 无命中：连解压结果都不使用，原样（含压缩）透传，零额外开销
		restoreBody(resp, buf, origin)
		return
	}
	_ = origin.Close()
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	// 改写后输出明文：删掉编码头，避免浏览器按 gzip 解码明文（FR-12）
	resp.Header.Del("Content-Encoding")
	// 体已变，强校验头失效，必须删除否则客户端缓存会永久 304
	resp.Header.Del("ETag")
	resp.Header.Del("Content-MD5")
	cnt.HTMLHits.Add(int64(hits))
	diag.Logf(diag.RewriteOK, cfg.Name, "字面量改写命中 %d 处，输出 %d 字节（原 %d）: %s",
		hits, len(out), len(buf), requestPath(resp))
}

// restoreBody 把已读字节写回响应体（可选地关闭原始流）。
func restoreBody(resp *http.Response, buf []byte, origin io.ReadCloser) {
	if origin != nil {
		_ = origin.Close()
	}
	resp.Body = io.NopCloser(bytes.NewReader(buf))
	resp.ContentLength = int64(len(buf))
	if len(buf) == 0 {
		resp.Header.Del("Content-Length")
		return
	}
	resp.Header.Set("Content-Length", strconv.Itoa(len(buf)))
}

// multiReadCloser 让「已读片段 + 剩余流」在关闭时仍能释放底层连接。
type multiReadCloser struct {
	io.Reader
	closer io.Closer
}

func (m *multiReadCloser) Close() error { return m.closer.Close() }

// stateFrom 从响应关联请求上取回处理快照。
func stateFrom(resp *http.Response) (*requestState, bool) {
	if resp == nil || resp.Request == nil {
		return nil, false
	}
	st, ok := resp.Request.Context().Value(stateKey{}).(*requestState)
	return st, ok
}

// requestPath 返回请求路径（日志用）。
func requestPath(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	return resp.Request.URL.Path
}

// proxyIP 提取请求来源（供重定向循环告警分组）。
// 与 ServeHTTP 保持一致：是否信任转发头由 Engine.trustProxies 决定，
// 这里默认不信任（只在未被 ServeHTTP 复用的场景下使用）。
func proxyIP(r *http.Request) string {
	return forward.ClientIP(r.RemoteAddr, r.Header, false)
}
