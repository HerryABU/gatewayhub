package rewrite

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRewritePath(t *testing.T) {
	cases := []struct {
		prefix string
		root   string
		in     string
		want   string
	}{
		// 根目录后端
		{"app", "", "/assets/index.js", "/app/assets/index.js"},
		{"app", "", "/", "/app/"},
		{"app", "/", "/styles.css", "/app/styles.css"},
		{"app", "", "/api/login", "/app/api/login"},
		{"app", "", "//cdn.x.com/a.js", "//cdn.x.com/a.js"},     // 协议相对不动
		{"app", "", "https://x.com/a.js", "https://x.com/a.js"}, // 绝对不动
		{"app", "", "relative.js", "relative.js"},               // 相对不动
		{"app", "", "/app/api/x", "/app/api/x"},                 // 已带前缀不重复
		// 子路径后端
		{"app", "/api/v1", "/api/v1/user", "/app/user"},
		{"app", "/api/v1", "/api/v1", "/app"},
		{"app", "/api/v1", "/other", "/app/other"},             // 非 root 的根路径同样加前缀（避免 404）
		{"app", "/api/v1", "/assets/x.js", "/app/assets/x.js"}, // 站点根资源也加前缀（修复 bug）
	}
	for _, c := range cases {
		rw := New(c.prefix, c.root, "")
		if got := rw.rewritePath(c.in); got != c.want {
			t.Errorf("rewritePath(%q,%q,%q) = %q, want %q", c.prefix, c.root, c.in, got, c.want)
		}
	}
}

func TestRewriteRelative(t *testing.T) {
	cases := []struct {
		prefix string
		root   string
		in     string
		want   string
	}{
		{"app", "", "/assets/index.js", "./assets/index.js"},
		{"app", "", "/api/login", "./api/login"},
		{"app", "", "/", "./"},
		{"app", "", "//cdn.x.com/a.js", "//cdn.x.com/a.js"},
		{"app", "", "https://x.com/a.js", "https://x.com/a.js"},
		{"app", "", "relative.js", "relative.js"},
		{"app", "", "/app/api/x", "/app/api/x"}, // 已带前缀不重复
		// 子路径后端：/{root}/x → ./x
		{"app", "/api/v1", "/api/v1/user", "./user"},
		{"app", "/api/v1", "/api/v1", "./"},
		{"app", "/api/v1", "/assets/x.js", "./assets/x.js"}, // 站点根资源相对化（修复 root 下 404）
	}
	for _, c := range cases {
		rw := New(c.prefix, c.root, "")
		if got := rw.rewriteRelative(c.in); got != c.want {
			t.Errorf("rewriteRelative(%q,%q,%q) = %q, want %q", c.prefix, c.root, c.in, got, c.want)
		}
	}
}

func TestRelativeToFile(t *testing.T) {
	cases := []struct {
		cssPath string
		raw     string
		want    string
	}{
		{"/app/assets/index.css", "/fonts/x.woff", "../fonts/x.woff"},
		{"/app/assets/index.css", "/assets/x.js", "./x.js"},
		{"/app/css/main.css", "/img/logo.png", "../img/logo.png"},
		{"/app/style.css", "/logo.png", "./logo.png"},
		{"/app/a/b/c.css", "/api/x", "../../api/x"},
	}
	for _, c := range cases {
		rw := New("app", "", "")
		if got := rw.relativeToFile(c.raw, c.cssPath); got != c.want {
			t.Errorf("relativeToFile(%q, %q) = %q, want %q", c.raw, c.cssPath, got, c.want)
		}
	}
}

func TestRewriteHTML(t *testing.T) {
	rw := New("app", "", "")
	in := `<html><head>
<link rel="stylesheet" href="/assets/style.css">
<script src="/assets/main.js"></script>
<script>fetch('/api/inline');const base='/api';</script>
</head><body>
<img src="/logo.png">
<img srcset="/a.png 1x, /b.png 2x">
<div style="background:url('/bg.png')"></div>
<style>.x{background:url(/bg2.png)}</style>
<a href="/login">go</a>
</body></html>`
	out := string(rw.rewriteHTML([]byte(in)))
	checks := []string{
		`<base href="/app/">`,       // 注入 base
		`href="./assets/style.css"`, // 绝对改相对
		`src="./assets/main.js"`,
		`src="./logo.png"`,
		`srcset="./a.png 1x, ./b.png 2x"`,
		`url('./bg.png')`,
		`url(./bg2.png)`,
		`href="./login"`,
		`data-gateway-prefix-shim="1"`, // 运行期前缀兜底脚本
		`fetch('/api/inline')`,         // 内联 <script> 的 API 字面量不动（可能是数据）
		`const base='/api'`,
	}
	for _, c := range checks {
		if !contains(out, c) {
			t.Errorf("rewriteHTML 未包含 %q\n输出:\n%s", c, out)
		}
	}
	// 不应残留未改写的根绝对路径（HTML 属性层面）
	for _, bad := range []string{`href="/assets/style.css"`, `src="/assets/main.js"`, `src="/logo.png"`} {
		if contains(out, bad) {
			t.Errorf("rewriteHTML 残留根绝对路径 %q\n输出:\n%s", bad, out)
		}
	}
}

func TestRewriteCSS(t *testing.T) {
	rw := New("app", "", "")
	out := string(rw.rewriteCSS([]byte(`body{background:url(/bg.png)} .a{background-image:url("/x/y.png")} @import "/theme.css"; @import url("/other.css");`)))
	if !contains(out, `url(./bg.png)`) {
		t.Errorf("CSS 未相对化: %s", out)
	}
	if !contains(out, `url("./x/y.png")`) {
		t.Errorf("CSS 未相对化引号内路径: %s", out)
	}
	if !contains(out, `@import "./theme.css"`) {
		t.Errorf("CSS 未相对化 @import 引号路径: %s", out)
	}
	if !contains(out, `url("./other.css")`) {
		t.Errorf("CSS 未相对化 @import url() 路径: %s", out)
	}
}

func TestRewriteCSSFile(t *testing.T) {
	rw := New("app", "", "")
	out := string(rw.rewriteCSSFile([]byte(`body{background:url(/bg.png)} @import "/theme.css";`), "/app/assets/index.css"))
	if !contains(out, `url(../bg.png)`) {
		t.Errorf("外链 CSS 未相对文件位置: %s", out)
	}
	if !contains(out, `@import "../theme.css"`) {
		t.Errorf("外链 CSS @import 未相对文件位置: %s", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRewriteJS(t *testing.T) {
	rw := New("app", "", "")
	in := `fetch("/api/site-info");axios.get('/api/categories');import("/assets/chunk.js");const base="/api";api.get('/setup/env-check');api.get("/auth/me");const x="/";window.location.href='/login';location.assign("/register");`
	out := string(rw.rewriteJS([]byte(in)))
	checks := []string{
		`fetch("/api/site-info")`,          // API 字面量不动（可能被当请求体数据发给后端）
		`axios.get('/api/categories')`,     // 同上：由运行期 shim 补前缀
		`"/app/assets/chunk.js"`,           // 动态 import 资源保留前缀绝对（module 基准）
		`const base="/api"`,                // baseURL 不再相对化（避免污染数据/双重前缀）
		`api.get('/setup/env-check')`,      // 相对端点不改写
		`api.get("/auth/me")`,              // 相对端点不改写
		`const x="/"`,                      // 单个 / 不重写
		`location.href='/app/login'`,       // 整页导航保留前缀绝对
		`location.assign("/app/register")`, // assign 保留前缀绝对
	}
	for _, c := range checks {
		if !contains(out, c) {
			t.Errorf("rewriteJS 未包含 %q\n输出:\n%s", c, out)
		}
	}
	// 不能产生双重前缀 /app/api/app/...（baseURL 与相对端点叠加）
	if contains(out, "/app/api/app") {
		t.Errorf("rewriteJS 出现双重前缀:\n%s", out)
	}
}

func TestIsRootPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// 资源前缀：重写
		{"/assets/app.js", true},
		{"/static/logo.png", true},
		{"/uploads/a.jpg", true},
		{"/media/x.mp4", true},
		{"/images/a.png", true},
		{"/img/a.png", true},
		{"/fonts/f.woff2", true},
		{"/css/a.css", true},
		{"/js/a.js", true},
		{"/public/a.js", true},
		{"/dist/a.js", true},
		{"/files/a.pdf", true},
		{"/favicon.ico", true},
		{"/logo.png", true},
		{"/icon.svg", true},
		{"/manifest.json", true},
		{"/robots.txt", true},
		// API 前缀 / axios 相对端点 / 业务路由：都不在 JS 字面量层重写
		// （"/api" 既可能是 URL 也可能是请求体数据，静态改写会污染业务数据，
		//  改由运行期 shim 在发请求时补前缀）
		{"/api", false},
		{"/api/user", false},
		{"/setup/env-check", false},
		{"/auth/me", false},
		{"/login", false},
		{"/register", false},
		{"/user/info", false},
		// 边界
		{"/", false},
		{"//cdn.x.com/a.js", false},
		{"/*", false},
		{"relative", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isRootPath(c.in); got != c.want {
			t.Errorf("isRootPath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestNeedsRewrite(t *testing.T) {
	if needsJSRewrite([]byte(`fetch("/api/x")`)) {
		t.Error("只有 /api 不应命中（API 由运行期 shim 处理）")
	}
	if !needsJSRewrite([]byte(`import("/assets/x.js")`)) {
		t.Error("含 /assets 应命中")
	}
	if !needsJSRewrite([]byte(`img.src="/static/a.png"`)) {
		t.Error("含 /static 应命中")
	}
	if !needsJSRewrite([]byte(`location.href='/login'`)) {
		t.Error("含 location.href 应命中")
	}
	if needsJSRewrite([]byte(`const x = 1 + 2; console.log("hello world");`)) {
		t.Error("纯逻辑 JS 不应命中")
	}
	if !needsCSSRewrite([]byte(`.a{background:url(/bg.png)}`)) {
		t.Error("含 url( 应命中")
	}
	if needsCSSRewrite([]byte(`.a{color:red}`)) {
		t.Error("无 url/@import 不应命中")
	}
}

func TestRewriteJSResources(t *testing.T) {
	rw := New("app", "", "")
	in := `const img="/static/logo.png";const up="/uploads/a.jpg";const ico="/favicon.ico";const font="/fonts/f.woff2";`
	out := string(rw.rewriteJS([]byte(in)))
	checks := []string{
		`"/app/static/logo.png"`,
		`"/app/uploads/a.jpg"`,
		`"/app/favicon.ico"`,
		`"/app/fonts/f.woff2"`,
	}
	for _, c := range checks {
		if !contains(out, c) {
			t.Errorf("rewriteJS 资源路径未改写 %q\n输出:\n%s", c, out)
		}
	}
}

// TestRewriteJSMidFileImport 验证「动态 import 位于大文件中部」也能被重写，
// 回归修复：曾因 Peek 64KB 预检漏判导致 SPA 路由懒加载 chunk 404。
func TestRewriteJSMidFileImport(t *testing.T) {
	rw := New("app", "", "")
	// 前 80KB 为无路径的 runtime 代码，动态 import 声明在 80KB 之后（文件中部）
	var sb strings.Builder
	sb.WriteString(`const runtime="` + strings.Repeat("x", 80*1024) + `";`)
	sb.WriteString(`const routes=[{path:"/home",component:()=>import("/assets/AuthorHome-abc.js")}];`)
	sb.WriteString(`import("/assets/core-def.js");`)
	out := string(rw.rewriteJS([]byte(sb.String())))
	checks := []string{
		`"/app/assets/AuthorHome-abc.js"`,
		`"/app/assets/core-def.js"`,
	}
	for _, c := range checks {
		if !contains(out, c) {
			t.Errorf("大文件中部的 import 未重写，缺失 %q", c)
		}
	}
	// 需要重写判断应命中（含 /assets）
	if !needsJSRewrite([]byte(sb.String())) {
		t.Errorf("needsJSRewrite 应命中含 /assets 的大文件")
	}
}

func TestInjectBase(t *testing.T) {
	rw := New("app", "", "")

	// 无 <base>：注入
	in := `<html><head><title>x</title></head><body></body></html>`
	out := rw.injectBase(in)
	if !contains(out, `<base href="/app/">`) {
		t.Errorf("未注入 <base>:\n%s", out)
	}

	// 已有 <base>：改写 href
	in2 := `<html><head><base href="/"><title>x</title></head></html>`
	out2 := rw.injectBase(in2)
	if !contains(out2, `<base href="/app/">`) {
		t.Errorf("未改写 <base>:\n%s", out2)
	}

	// 带子路径后端：同样注入（相对路径统一依赖 base）
	rw2 := New("app", "/api/v1", "")
	if out3 := rw2.injectBase(in); !contains(out3, `<base href="/app/">`) {
		t.Errorf("带子路径后端也应注入 <base>:\n%s", out3)
	}
}

func gzipBytes(t *testing.T, b []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()
	return buf.Bytes()
}

// TestModify 覆盖 ModifyResponse 的 gzip 解压/重压、Location 重写与 Content-Length 更新。
func TestModify(t *testing.T) {
	body := []byte(`<html><head><script src="/main.js"></script></head></html>`)

	// 1) 非 gzip HTML：绝对改相对 + 注入 base
	resp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
		Body:   io.NopCloser(bytes.NewReader(body)),
	}
	rw := New("app", "", "127.0.0.1:8080")
	if err := rw.Modify(resp); err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	if !contains(string(out), `src="./main.js"`) {
		t.Errorf("普通 HTML 未相对化: %s", out)
	}
	if !contains(string(out), `<base href="/app/">`) {
		t.Errorf("普通 HTML 未注入 base: %s", out)
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("非 gzip 响应不应有 Content-Encoding")
	}

	// 2) gzip HTML
	resp2 := &http.Response{
		Header: http.Header{
			"Content-Type":     []string{"text/html"},
			"Content-Encoding": []string{"gzip"},
		},
		Body: io.NopCloser(bytes.NewReader(gzipBytes(t, body))),
	}
	if err := rw.Modify(resp2); err != nil {
		t.Fatal(err)
	}
	gr, err := gzip.NewReader(resp2.Body)
	if err != nil {
		t.Fatalf("重压后无法解压: %v", err)
	}
	decoded, _ := io.ReadAll(gr)
	if !contains(string(decoded), `src="./main.js"`) {
		t.Errorf("gzip HTML 未相对化: %s", decoded)
	}
	if resp2.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("gzip 响应应保留 Content-Encoding")
	}

	// 3) Location 重写（根相对 + 指向后端自身的绝对地址）→ 保留前缀绝对
	resp3 := &http.Response{
		Header: http.Header{
			"Location":     []string{"/login"},
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader([]byte(`{}`))),
	}
	_ = rw.Modify(resp3)
	if got := resp3.Header.Get("Location"); got != "/app/login" {
		t.Errorf("Location 根相对未重写: %s", got)
	}

	resp4 := &http.Response{
		Header: http.Header{"Location": []string{"http://127.0.0.1:8080/foo"}},
		Body:   io.NopCloser(bytes.NewReader([]byte(`{}`))),
	}
	_ = rw.Modify(resp4)
	if got := resp4.Header.Get("Location"); got != "/app/foo" {
		t.Errorf("Location 绝对(后端自身)未重写: %s", got)
	}

	// 4) 外链 CSS 按「CSS 文件位置」相对化（网关侧路径经 context 透传）
	cssBody := []byte(`body{background:url(/bg.png)}`)
	resp5 := &http.Response{
		Header:  http.Header{"Content-Type": []string{"text/css"}},
		Body:    io.NopCloser(bytes.NewReader(cssBody)),
		Request: mustReqWithGWPath("/app/assets/index.css"),
	}
	if err := rw.Modify(resp5); err != nil {
		t.Fatal(err)
	}
	cssOut, _ := io.ReadAll(resp5.Body)
	if !contains(string(cssOut), `url(../bg.png)`) {
		t.Errorf("外链 CSS 未按文件位置相对化: %s", cssOut)
	}
}

func mustReqWithGWPath(p string) *http.Request {
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:8080/"+p, nil)
	return WithGWPath(req, p)
}

// TestPrefixShim 校验运行期前缀兜底脚本：注入位置、幂等、前缀编码与关键钩子齐全。
func TestPrefixShim(t *testing.T) {
	rw := New("go-nvs", "", "")
	html := `<html><head><script type="module" src="./assets/index.js"></script></head><body></body></html>`
	out := string(rw.rewriteHTML([]byte(html)))

	if strings.Count(out, "data-gateway-prefix-shim") != 1 {
		t.Fatalf("shim 应恰好注入一次:\n%s", out)
	}
	if !contains(out, `var P="/go-nvs";`) {
		t.Errorf("shim 前缀未以 JSON 字符串形式注入:\n%s", out)
	}
	// 必须早于应用脚本执行（否则首屏请求已经漏出去）
	if strings.Index(out, "data-gateway-prefix-shim") > strings.Index(out, `type="module"`) {
		t.Errorf("shim 必须在应用脚本之前:\n%s", out)
	}
	for _, hook := range []string{"window.fetch", "XMLHttpRequest.prototype.open", "navigator.sendBeacon", "history.pushState", "Element.prototype.setAttribute", "HTMLScriptElement"} {
		if !contains(out, hook) {
			t.Errorf("shim 缺少钩子 %q", hook)
		}
	}
	// 二次注入幂等（同一个响应被重复处理不应叠两份脚本）
	if again := string(rw.rewriteHTML([]byte(out))); strings.Count(again, "data-gateway-prefix-shim") != 1 {
		t.Errorf("shim 重复注入:\n%s", again)
	}
	// 空前缀不注入
	empty := string(New("", "", "").rewriteHTML([]byte(html)))
	if contains(empty, "data-gateway-prefix-shim") {
		t.Errorf("空前缀不应注入 shim:/n%s", empty)
	}
}
