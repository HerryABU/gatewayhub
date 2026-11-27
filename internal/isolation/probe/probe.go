// Package probe 智能识别后端该用哪种去头模式（pass-through / strip / isolated）。
//
// 为什么需要：同一个人写的 openlist，既可能装在站点根（base_path='/'，网关必须
// 剥前缀并改写根相对资源 → strip），也可能自己配了 site_url 带前缀（后端已认
// 前缀，网关再剥就会 302 自环 → pass-through）。选错模式的症状是白屏或
// ERR_TOO_MANY_REDIRECTS，管理员靠试错非常痛苦。
//
// 探测只读、不改变后端任何状态：请求后端根路径，分析三类信号——
//  1. 3xx Location：带自身服务名前缀 → 后端已配 base path → pass-through；
//  2. 内联 base_path / <base href> 声明非根 → 后端已认前缀 → pass-through；
//  3. HTML 里大量根相对资源（/assets、/static）且声明根 → 需要网关改写 → strip；
//  4. 非 HTML / 信号不足 → isolated（最保守，只改头不动 body）。
package probe

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Result 是一次探测的结论。
type Result struct {
	Mode       string   `json:"mode"`       // pass-through / strip / isolated
	Reason     string   `json:"reason"`     // 给用户看的可执行结论
	Signals    []string `json:"signals"`    // 观测到的证据
	Confidence string   `json:"confidence"` // high / medium / low
}

var (
	basePathRe  = regexp.MustCompile(`(?i)(?:base_?path|basename|base_?url|site_?url)\s*[:=]\s*['"]([^'"]*)['"]`)
	baseHrefRe  = regexp.MustCompile(`(?is)<base\b[^>]*\bhref\s*=\s*['"]([^'"]*)['"]`)
	rootAssetRe = regexp.MustCompile(`(?i)(?:src|href)\s*=\s*['"]/(?:assets|static|dist)/`)
)

// Detect 请求 target 根路径并推断模式。
// service 是对外服务名（如 openlist），用于识别「后端已按该名字配了 base path」。
func Detect(target, service string, timeout time.Duration) (*Result, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{
		Timeout: timeout,
		// 不跟随重定向：3xx 的 Location 本身就是最重要的信号。
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest(http.MethodGet, ensureSlash(target), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "nvs-selfhost-mode-probe/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return analyze(resp, string(body), service), nil
}

func ensureSlash(s string) string {
	if strings.HasSuffix(s, "/") {
		return s
	}
	return s + "/"
}

func analyze(resp *http.Response, body, service string) *Result {
	sig := []string{}
	svc := "/" + strings.Trim(service, "/")

	// 1) 重定向：Location 带服务前缀 → 后端已认前缀，网关不能剥。
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		sig = append(sig, resp.Status+" → "+loc)
		if loc != "" {
			p := locationPath(loc)
			if p == svc || strings.HasPrefix(p, svc+"/") {
				return &Result{Mode: "pass-through", Reason: "后端返回 302 到 " + p + "，说明它已自行配置 base path；网关应原样透传前缀，剥前缀会导致重定向自环。", Signals: sig, Confidence: "high"}
			}
			if p == "/" || p == "" {
				sig = append(sig, "Location 指向站点根，可能是登录跳转而非 base path 声明")
			}
		}
	}

	// 2) 内联声明：base_path / basename / <base href> 非根 → 后端已认前缀。
	for _, m := range basePathRe.FindAllStringSubmatch(body, -1) {
		v := strings.TrimSpace(m[1])
		sig = append(sig, "声明 "+v)
		if v != "" && v != "/" {
			return &Result{Mode: "pass-through", Reason: "后端声明自身挂载点是 " + v + "，已自行认前缀；网关应 pass-through 避免二次剥前缀。", Signals: sig, Confidence: "high"}
		}
	}
	if m := baseHrefRe.FindStringSubmatch(body); len(m) == 2 {
		v := strings.TrimSpace(m[1])
		sig = append(sig, "<base href=\""+v+"\">")
		if v != "" && v != "/" && strings.HasPrefix(v, "/") {
			return &Result{Mode: "pass-through", Reason: "后端页面 <base href=\"" + v + "\"> 已带前缀，网关应 pass-through。", Signals: sig, Confidence: "high"}
		}
	}

	// 3) HTML + 根相对资源 + base_path='/' → 后端挂在根，网关必须剥前缀并改写。
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/html") || strings.Contains(ct, "xhtml") {
		if basePathRe.MatchString(body) {
			sig = append(sig, "base_path 声明为根 /")
		}
		hits := len(rootAssetRe.FindAllString(body, -1))
		if hits > 0 {
			sig = append(sig, "发现 "+itoa(hits)+" 个根相对资源引用")
			return &Result{Mode: "strip", Reason: "后端声明挂在站点根且有 " + itoa(hits) + " 个根相对资源；网关需剥前缀并改写资源路径（base_path 与 shim 会自动补回）。", Signals: sig, Confidence: "high"}
		}
		// HTML 但没有明确资源信号：strip 仍比 isolated 更能保证资源可加载。
		sig = append(sig, "HTML 页面，未发现明确的 base path 或资源信号")
		return &Result{Mode: "strip", Reason: "HTML 页面未声明 base path，按网关挂载场景推断需要剥前缀并改写资源；若实测 302 自环请改用 pass-through。", Signals: sig, Confidence: "medium"}
	}

	// 4) 非 HTML：没有可改写的资源路径，保守只改头。
	sig = append(sig, "Content-Type="+ct)
	return &Result{Mode: "isolated", Reason: "后端未返回 HTML，无资源路径需要改写；用 isolated 只做 Cookie/Location 头层修正最安全。", Signals: sig, Confidence: "medium"}
}

func locationPath(loc string) string {
	if u, err := url.Parse(loc); err == nil && u.IsAbs() {
		return u.Path
	}
	if i := strings.IndexAny(loc, "?#"); i >= 0 {
		loc = loc[:i]
	}
	return loc
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
