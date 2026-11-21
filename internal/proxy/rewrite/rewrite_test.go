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
	in := `fetch("/api/site-info");axios.get('/api/categories');import("/assets/chunk.js");const x="/";window.location.href='/login';location.assign("/register");`
	out := string(rw.rewriteJS([]byte(in)))
	checks := []string{
		// 非导航字面量一律不动：文本层判不出「URL 还是请求体数据」，由运行期 shim 处理
		`fetch("/api/site-info")`,
		`axios.get('/api/categories')`,
		`import("/assets/chunk.js")`,
		`const x="/"`,
		// 整页导航：保留前缀绝对（最稳，不依赖 document base）
		`location.href='/app/login'`,
		`location.assign("/app/register")`,
	}
	for _, c := range checks {
		if !contains(out, c) {
			t.Errorf("rewriteJS 未包含 %q\n输出:\n%s", c, out)
		}
	}
	if contains(out, "/app/assets/") {
		t.Errorf("JS 资源字面量不应被静态改写:\n%s", out)
	}
}

func TestRewriteJSMidFileImport(t *testing.T) {
	rw := New("app", "", "")
	var sb strings.Builder
	sb.WriteString(`const runtime="` + strings.Repeat("x", 80*1024) + `";`)
	sb.WriteString(`const routes=[{path:"/home",component:()=>import("/assets/AuthorHome-abc.js")}];`)
	sb.WriteString(`import("/assets/core-def.js");`)
	// 大文件中部也不改写：避免「预检窗口漏判」这类历史问题的同时，
	// 彻底消除把业务数据当路径改写的风险（见 TestRewriteJSSkipsAPIPaths）。
	if out := string(rw.rewriteJS([]byte(sb.String()))); out != sb.String() {
		t.Errorf("大文件中的 import 不应被静态改写")
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
	for _, hook := range []string{"window.fetch", "XMLHttpRequest.prototype.open", "navigator.sendBeacon", "history.pushState", "Element.prototype.setAttribute", "HTMLScriptElement", "__dynamic_base__"} {
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

// TestRewriteBasePathConfig 校验「运行期挂载点声明」改写：
// 只改值为 "/" 的声明（SPA 说自己挂在站点根 → 改成网关前缀），带内容的值一律不动。
func TestRewriteBasePathConfig(t *testing.T) {
	rw := New("openlist", "", "")

	cases := []struct{ in, want string }{
		// openlist / alist：单引号、对象字面量
		{`base_path: '/',`, `base_path: '/openlist',`},
		// 压缩后的 JSON 风格：双引号 + 无空格
		{`{"base_path":"/"}`, `{"base_path":"/openlist"}`},
		{`{ "basePath" : "/" }`, `{ "basePath" : "/openlist" }`},
		{`basename: "/"`, `basename: "/openlist"`},
		{`baseURL: '/'`, `baseURL: '/openlist'`},
		{`publicPath: "/"`, `publicPath: "/openlist"`},
		// 已带内容 / 非根：不动
		{`base_path: '/app'`, `base_path: '/app'`},
		{`basename: "//cdn"`, `basename: "//cdn"`},
		{`base_path: '/openlist'`, `base_path: '/openlist'`},
		// 业务字段同名但不是挂载点（值不是 "/"）
		{`path: '/'`, `path: '/'`},
	}
	for _, c := range cases {
		got, _ := rw.rewriteBasePathConfig(c.in)
		if got != c.want {
			t.Errorf("rewriteBasePathConfig(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// 命中计数：只算真正改写的键
	if _, n := rw.rewriteBasePathConfig(`base_path:'/';basename:'/';path:'/'`); n != 2 {
		t.Errorf("命中计数 = %d, want 2", n)
	}
	// 空前缀不改
	if got, _ := New("", "", "").rewriteBasePathConfig(`base_path: '/'`); got != `base_path: '/'` {
		t.Errorf("空前缀不应改写: %q", got)
	}
}

// TestPrepareHTML 校验挂载准备整体：挂载点改写 + shim 注入，且幂等。
func TestPrepareHTML(t *testing.T) {
	html := `<!doctype html><html><head><script>
window.APP = { base_path: '/', cdn: '' }
</script></head><body><div id="root"></div></body></html>`
	out, n := PrepareHTML("openlist", []byte(html))
	if n == 0 {
		t.Fatal("应至少命中挂载点改写")
	}
	s := string(out)
	if !contains(s, `base_path: '/openlist'`) {
		t.Errorf("挂载点未改写:\n%s", s)
	}
	if strings.Count(s, "data-gateway-prefix-shim") != 1 {
		t.Errorf("shim 应注入一次:\n%s", s)
	}
	// 幂等：同一份产物再过一遍不应叠加
	again, n2 := PrepareHTML("openlist", out)
	if n2 != 0 || string(again) == "" {
		t.Errorf("二次处理应无命中且原样返回，n=%d", n2)
	}
	if strings.Count(string(again), "data-gateway-prefix-shim") != 1 {
		t.Errorf("shim 重复注入:\n%s", string(again))
	}
	// 空前缀：原样
	if b, n := PrepareHTML("", []byte(html)); n != 0 || string(b) != html {
		t.Errorf("空前缀应原样返回")
	}
}

// TestShimCollapsesDoublePrefix 校验运行期兜底的幂等收敛：
// 静态层与运行期基准各加一次前缀时，请求不能落到 /prefix/prefix/...。
func TestShimCollapsesDoublePrefix(t *testing.T) {
	shim := New("openlist", "", "").prefixShim()
	if !strings.Contains(shim, "P+P") {
		t.Fatalf("shim 应包含双重前缀收敛逻辑:\n%s", shim)
	}
}

// TestHTMLAttrSkipsDeferred 校验延迟地址（data-src/data-href）不被静态改写。
func TestHTMLAttrSkipsDeferred(t *testing.T) {
	in := `<script nomodule id="vite-legacy-entry" data-src="/assets/index-legacy.js"></script>
<img data-src="/assets/lazy.png" src="/assets/real.png">`
	out := string(New("openlist", "", "").rewriteHTML([]byte(in)))
	for _, keep := range []string{`data-src="/assets/index-legacy.js"`, `data-src="/assets/lazy.png"`} {
		if !strings.Contains(out, keep) {
			t.Errorf("延迟地址不应改写，缺少 %q\n%s", keep, out)
		}
	}
	if !strings.Contains(out, `src="./assets/real.png"`) {
		t.Errorf("普通 src 应改写为文档相对\n%s", out)
	}
}
