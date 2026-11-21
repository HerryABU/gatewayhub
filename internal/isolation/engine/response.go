package engine

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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

		// 1b) 确定性重定向自环：strip/isolated 下后端把浏览器指回它自己的 base path，
		// 剥前缀后又回到同一后端路径 —— 循环闭合、永远不前进。首次只诊断，再次命中才 508。
		if isRedirect(resp.StatusCode) && cfg.Mode != config.ModePassThrough {
			if rt.selfLoopConfirmed(resp, cfg, cnt, resp.Header.Get("Location")) {
				rt.writeLoopDetected(resp, cfg, resp.Header.Get("Location"))
				return nil
			}
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

// selfLoopConfirmed 判断本次 3xx 是否构成「确定性自环」，并决定是否升级为终止。
//
// 判据是纯函数式的（不靠猜测、不靠时间窗口猜）：Location 去掉服务前缀后的路径，正好
// 等于本次转发给后端的路径。此时浏览器下一步请求的后端路径与刚才完全一致，而 strip
// 又会同样地剥掉前缀，循环闭合。
//
// 返回 true 表示「窗口内已确认是死循环，应当拦截」；首次命中只诊断、放行。
func (rt *runtime) selfLoopConfirmed(resp *http.Response, cfg *config.Service, cnt *metrics.Counters, loc string) bool {
	if rt.loopGuard == nil || resp.Request == nil || resp.Request.URL == nil || loc == "" {
		return false
	}
	backendPath := resp.Request.URL.Path
	target, ok := locationPath(loc)
	if !ok || stripServicePrefix(target, cfg.Prefix) != backendPath {
		return false
	}
	key := rt.name + "|" + proxyIP(resp.Request) + "|" + backendPath
	n := rt.loopGuard.Hit(key, time.Now())
	cnt.LoopAlerts.Add(1)
	if n == 1 {
		diag.Logf(diag.LoopWarning, rt.name,
			"检测到确定性重定向自环（首次）：后端 %d → Location=%q，去前缀后仍是后端路径 %q；"+
				"该服务很可能已自行配置 base path（如 openlist 的 site_url 带 /%s），应改为 pass-through",
			resp.StatusCode, loc, backendPath, cfg.Name)
		return false
	}
	diag.Logf(diag.ConfigError, rt.name,
		"重定向自环确认（窗口内第 %d 次），返回 508 终止：后端路径 %q；"+
			"修复方式：把该服务改为 pass-through（后端自己认前缀），或去掉后端的 base path 配置",
		n, backendPath)
	return true
}

// writeLoopDetected 把响应替换为 508 + 可执行的修复建议（不泄露后端 IP:端口）。
func (rt *runtime) writeLoopDetected(resp *http.Response, cfg *config.Service, loc string) {
	msg := fmt.Sprintf("%s 的重定向构成死循环：后端 3xx 指向 %q，而网关按 %s 模式剥掉 /%s 前缀后又回到同一后端路径。请把该服务改为 pass-through（后端自己认前缀），或去掉后端自身的 base path 配置。",
		cfg.Name, loc, cfg.Mode, cfg.Name)
	body := fmt.Sprintf(`{"code":%d,"message":%q}`, http.StatusLoopDetected, msg)
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	resp.StatusCode = http.StatusLoopDetected
	resp.Status = "508 Loop Detected"
	h := resp.Header
	h.Del("Location")
	h.Del("Content-Encoding")
	h.Del("Content-MD5")
	h.Del("ETag")
	h.Del("Transfer-Encoding")
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(strings.NewReader(body))
}

// isRedirect 判断状态码是否为 3xx。
func isRedirect(code int) bool { return code >= 300 && code < 400 }

// locationPath 从 Location 头里取出路径部分（绝对 URL、协议相对、根相对都支持）。
func locationPath(loc string) (string, bool) {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return "", false
	}
	u, err := url.Parse(loc)
	if err != nil {
		return "", false
	}
	if u.Path != "" {
		return u.Path, true
	}
	// 形如 "/foo" 的解析结果已经是 Path；形如 "foo" 的相对值是 Path 相对，
	// 拿不到稳定的后端归属，直接放弃判定（宁可不拦，也不误拦）。
	if strings.HasPrefix(loc, "/") {
		if i := strings.IndexAny(loc, "?#"); i > 0 {
			return loc[:i], true
		}
		return loc, true
	}
	return "", false
}

// stripServicePrefix 把对外路径映射回后端路径（"/A/x" → "/x"，"/A" → "/"）。
func stripServicePrefix(p, prefix string) string {
	if prefix == "" || p == "" {
		return p
	}
	if p == prefix {
		return "/"
	}
	if strings.HasPrefix(p, prefix+"/") {
		return p[len(prefix):]
	}
	return p
}

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
