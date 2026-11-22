package proxy

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/hopguard"
	"gatewayhub/internal/models"
	"gatewayhub/internal/proxy/rewrite"
	"gatewayhub/internal/stats"
)

func newTestManager(baseDomain string, routes ...models.Route) *Manager {
	m := NewManager(nil, stats.NewWriter(nil, nil, 1, 100, time.Second, 1.0), baseDomain, SelfInfo{})
	m.mu.Lock()
	for _, r := range routes {
		if e, err := m.buildEntry(r); err == nil {
			m.m[r.Prefix] = e
		}
	}
	m.rebuildMultiLocked()
	m.mu.Unlock()
	return m
}

func activeRoute(prefix, target string) models.Route {
	return models.Route{Prefix: prefix, Target: target, Timeout: 5, Status: models.StatusActive}
}

func TestMatchSubdomain(t *testing.T) {
	m := NewManager(nil, nil, "localhost", SelfInfo{})
	cases := []struct {
		host   string
		prefix string
		ok     bool
	}{
		{"java-order.localhost", "java-order", true},
		{"java-order.localhost:8088", "java-order", true},
		{"JAVA-ORDER.localhost", "java-order", true}, // 大小写不敏感
		{"localhost", "", false},                     // 无子域名
		{"a.b.localhost", "", false},                 // 多级子域名不支持
		{"example.com", "", false},                   // 不匹配 baseDomain
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := m.matchSubdomain(c.host)
		if got != c.prefix || ok != c.ok {
			t.Errorf("matchSubdomain(%q) = (%q,%v), want (%q,%v)", c.host, got, ok, c.prefix, c.ok)
		}
	}

	// 禁用子域名路由：baseDomain 为空
	m2 := NewManager(nil, nil, "", SelfInfo{})
	if _, ok := m2.matchSubdomain("java-order.localhost"); ok {
		t.Error("baseDomain 为空应禁用子域名路由")
	}
}

func TestMatchPathLongestPrefix(t *testing.T) {
	m := newTestManager("localhost",
		activeRoute("java-order", ":8080"),
		activeRoute("v2", ":8081"),
		activeRoute("v2/beta", ":8082"),
	)
	cases := []struct {
		path   string
		prefix string
		ok     bool
	}{
		{"/java-order/user", "java-order", true},
		{"/v2/beta/x", "v2/beta", true}, // 最长前缀优先
		{"/v2/other", "v2", true},       // 未命中多级，回退单段
		{"/v2", "v2", true},
		{"/unknown", "", false},
	}
	for _, c := range cases {
		got, _, ok := m.matchPath(c.path)
		if got != c.prefix || ok != c.ok {
			t.Errorf("matchPath(%q) = (%q,%v), want (%q,%v)", c.path, got, ok, c.prefix, c.ok)
		}
	}
}

func TestMatchesPrefix(t *testing.T) {
	cases := []struct {
		path   string
		prefix string
		want   bool
	}{
		{"/v2/beta/x", "v2/beta", true},
		{"/v2/beta", "v2/beta", true},
		{"/v2/betax", "v2/beta", false}, // 段边界：/v2/betax 不是 /v2/beta
		{"/v2", "v2/beta", false},
		{"/a", "a", true},
		{"/aX", "a", false}, // 段边界：/aX 不是 /a
	}
	for _, c := range cases {
		if got := matchesPrefix(c.path, c.prefix); got != c.want {
			t.Errorf("matchesPrefix(%q,%q) = %v, want %v", c.path, c.prefix, got, c.want)
		}
	}
}

func TestIsUpgrade(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Connection", "upgrade")
	req.Header.Set("Upgrade", "websocket")
	if !isUpgrade(req) {
		t.Error("应识别为 Upgrade 请求")
	}
	req2 := httptest.NewRequest("GET", "/", nil)
	if isUpgrade(req2) {
		t.Error("普通 GET 不应识别为 Upgrade")
	}
}

// TestSubdomainProxyE2E 子域名模式应零重写（后端收到无前缀路径）；路径模式应剥离前缀。
func TestSubdomainProxyE2E(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("path=" + r.URL.Path + ";host=" + r.Host))
	}))
	defer backend.Close()

	m := newTestManager("localhost", activeRoute("java-order", backend.URL))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(func(c *gin.Context) {
		if m.Matchable(c.Request.URL.Path, c.Request.Host) {
			m.Handle(c)
			return
		}
		c.String(http.StatusNotFound, "nf")
	})

	// 子域名请求：java-order.localhost/user → 后端应收到 /user（无前缀）
	req := httptest.NewRequest("GET", "http://java-order.localhost/user", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Body.String() != "path=/user;host=127.0.0.1:"+portOf(backend.URL) {
		t.Errorf("子域名模式转发错误: %s", w.Body.String())
	}

	// 路径请求：/java-order/user → 后端应收到 /user（剥离前缀）
	req2 := httptest.NewRequest("GET", "http://localhost/java-order/user", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Body.String() != "path=/user;host=127.0.0.1:"+portOf(backend.URL) {
		t.Errorf("路径模式转发错误: %s", w2.Body.String())
	}
}

// TestWebSocketUpgradeE2E 验证 Upgrade 请求被透明转发（后端返回 101），不被超时/重写破坏。
// 注意：必须用真实 httptest.Server 做网关（而非 gin.ServeHTTP + ResponseRecorder），
// 因为 httputil.ReverseProxy 处理 Upgrade 时会调用 http.CloseNotifier，ResponseRecorder 不实现它。
func TestWebSocketUpgradeE2E(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isUpgrade(r) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		conn, _, _ := hj.Hijack()
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"))
		_ = conn.Close()
	}))
	defer backend.Close()

	m := newTestManager("localhost", activeRoute("java-order", backend.URL))
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(func(c *gin.Context) {
		if m.Matchable(c.Request.URL.Path, c.Request.Host) {
			m.Handle(c)
			return
		}
		c.String(http.StatusNotFound, "nf")
	})

	gw := httptest.NewServer(r)
	defer gw.Close()

	req, _ := http.NewRequest("GET", gw.URL+"/java-order/ws", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Upgrade 请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Errorf("Upgrade 应返回 101, got %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Upgrade")), "websocket") {
		t.Errorf("Upgrade 头缺失: %v", resp.Header)
	}
}

func portOf(rawURL string) string {
	i := strings.LastIndex(rawURL, ":")
	return rawURL[i+1:]
}

func TestSanitizeRest(t *testing.T) {
	cases := []struct {
		path   string
		prefix string
		want   string
	}{
		{"/go-nvs", "go-nvs", "/"},
		{"/go-nvs/", "go-nvs", "/"},
		{"/go-nvs/foo", "go-nvs", "/foo"},
		{"/go-nvs/foo/", "go-nvs", "/foo/"},
		{"/go-nvs/foo/bar", "go-nvs", "/foo/bar"},
		// 路径穿透防护
		{"/go-nvs/../../etc/passwd", "go-nvs", "/etc/passwd"},
		{"/go-nvs/../secret", "go-nvs", "/secret"},
		{"/go-nvs/foo/../../../x", "go-nvs", "/x"},
		{"/go-nvs/foo/./bar", "go-nvs", "/foo/bar"},
		// 前缀必须是完整段
		{"/go-nvsX/foo", "go-nvs", "/"},
	}
	for _, c := range cases {
		if got := sanitizeRest(c.path, c.prefix); got != c.want {
			t.Errorf("sanitizeRest(%q,%q) = %q, want %q", c.path, c.prefix, got, c.want)
		}
	}
}

// startSelfGateway 起一个「能转发给自己」的网关：先起 httptest.Server，再把它的
// 地址交给 Manager 建自指路由（target 只有运行后才知道）。
func startSelfGateway(t *testing.T, maxHops int) (*Manager, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var m *Manager
	r := gin.New()
	r.NoRoute(func(c *gin.Context) {
		if m == nil || !m.Matchable(c.Request.URL.Path, c.Request.Host) {
			c.String(http.StatusNotFound, "nf")
			return
		}
		m.Handle(c)
	})
	gw := httptest.NewServer(r)
	t.Cleanup(gw.Close)
	m = NewManager(nil, stats.NewWriter(nil, nil, 1, 100, time.Second, 1.0), "",
		SelfInfo{Port: mustAtoi(t, portOf(gw.URL)), MaxHops: maxHops})
	return m, gw.URL
}

// noDecompressClient 避免 http.Client 自动解开 gzip 并删掉 Content-Encoding，
// 否则测试看到的是「客户端解压后」的产物，验不出网关自己的打包行为。
func noDecompressClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("端口解析失败 %q: %v", s, err)
	}
	return n
}

// TestSelfReferenceChain 自指 + 嵌套：/chain/a → 自身 /chain/b → 真后端。
// 这是合法用法（把另一个前缀挂到新前缀下），必须能走通，且后端能看出经过了 2 跳。
func TestSelfReferenceChain(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hit-backend:" + r.URL.Path + ";hops=" + r.Header.Get(hopguard.Header)))
	}))
	defer backend.Close()

	m, gwURL := startSelfGateway(t, 4)
	if err := m.Upsert(activeRoute("chain/a", gwURL+"/chain/b")); err != nil {
		t.Fatalf("建自指路由失败: %v", err)
	}
	if err := m.Upsert(activeRoute("chain/b", backend.URL)); err != nil {
		t.Fatalf("建后端路由失败: %v", err)
	}
	if e, _ := m.Get("chain/a"); e == nil || !e.Self {
		t.Fatal("chain/a 应被识别为自指路由")
	}

	resp, err := http.Get(gwURL + "/chain/a/x")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("自指链路应 200，got %d body=%s", resp.StatusCode, body)
	}
	if got := string(body); got != "hit-backend:/x;hops=2" {
		t.Fatalf("自指链结果不符：%q（应为 2 跳到达后端）", got)
	}
}

// TestSelfReferenceLoopDetected 配成绕回自己的自指路由必须被跳数上限拦住：
// 返回 508 而不是把进程拖进无限请求生成。
func TestSelfReferenceLoopDetected(t *testing.T) {
	m, gwURL := startSelfGateway(t, 3)
	if err := m.Upsert(activeRoute("dead", gwURL+"/dead")); err != nil {
		t.Fatalf("建自指路由失败: %v", err)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(gwURL + "/dead/x")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusLoopDetected {
		t.Fatalf("死循环应返回 508，got %d", resp.StatusCode)
	}
}

// gzipChainBackend 返回 gzip 压缩的 HTML（或 br 原样字节），并记录收到的路径。
func gzipChainBackend(t *testing.T, html []byte) (*httptest.Server, *[]string) {
	t.Helper()
	seen := &[]string{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = append(*seen, r.URL.Path)
		enc := r.URL.Query().Get("enc")
		if enc == "br" {
			// 假的 brotli 字节：网关必须一个字节都不碰、原样透传
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Encoding", "br")
			_, _ = w.Write([]byte{0x1b, 0x12, 0x00, 0xff, 'b', 'r'})
			return
		}
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(html)
		_ = zw.Close()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		_, _ = w.Write(buf.Bytes())
	})), seen
}

// TestNestedChainGzipRewrite 多层自指链 /a → /b → /c → gzip 后端：
// 每一跳都要解开 gzip、改写、再打包；最终 base 与资源路径必须反映**最外层**前缀。
func TestNestedChainGzipRewrite(t *testing.T) {
	html := []byte(`<html><head><title>nested</title></head><body><a href="/assets/app.js">a</a><script src="/api/x"></script></body></html>`)
	backend, seen := gzipChainBackend(t, html)
	defer backend.Close()

	m, gwURL := startSelfGateway(t, 6)
	// 目标只写网关根：路径里剩下的 /b/c/... 自然由下一跳前缀匹配。
	for _, pfx := range []string{"a", "b"} {
		if err := m.Upsert(activeRoute(pfx, gwURL)); err != nil {
			t.Fatalf("建自指路由 %s 失败: %v", pfx, err)
		}
	}
	if err := m.Upsert(activeRoute("c", backend.URL)); err != nil {
		t.Fatalf("建后端路由失败: %v", err)
	}

	resp, err := noDecompressClient().Get(gwURL + "/a/b/c/index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("嵌套链应 200，got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("最终响应应保持 gzip，got %q", got)
	}
	if got := resp.Header.Get(rewrite.PrefixChainHeader); got != "" {
		t.Fatalf("内部前缀链头不能漏给浏览器，got %q", got)
	}
	raw, _ := io.ReadAll(resp.Body)
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("响应不是单层合法 gzip（可能被重复压缩）: %v", err)
	}
	defer zr.Close()
	out, _ := io.ReadAll(zr)
	body := string(out)
	if !strings.Contains(body, `<base href="/a/b/c/">`) {
		t.Fatalf("base 必须覆盖完整前缀链 /a/b/c/，实际: %s", body)
	}
	if strings.Contains(body, `<base href="/a/">`) || strings.Contains(body, `<base href="/b/">`) || strings.Contains(body, `<base href="/c/">`) {
		t.Fatalf("不能残留单跳 base: %s", body)
	}
	if !strings.Contains(body, `./assets/app.js`) || !strings.Contains(body, `./api/x`) {
		t.Fatalf("资源/接口路径未改写: %s", body)
	}
	if strings.Contains(body, `href="/assets`) || strings.Contains(body, `src="/api`) {
		t.Fatalf("根绝对路径没被改写: %s", body)
	}
	if len(*seen) != 1 || (*seen)[0] != "/index.html" {
		t.Fatalf("后端应只收到一次 /index.html，实际 %v", *seen)
	}
}

// TestNestedChainUnsupportedEncodingPassThrough br/zstd 逐跳原样透传：
// 不能被当成明文改写，也不能被删掉 Content-Encoding（那会让浏览器解压缩失败）。
func TestNestedChainUnsupportedEncodingPassThrough(t *testing.T) {
	backend, _ := gzipChainBackend(t, nil)
	defer backend.Close()
	m, gwURL := startSelfGateway(t, 6)
	for _, pfx := range []string{"a", "b"} {
		if err := m.Upsert(activeRoute(pfx, gwURL)); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Upsert(activeRoute("c", backend.URL)); err != nil {
		t.Fatal(err)
	}

	resp, err := noDecompressClient().Get(gwURL + "/a/b/c/br?enc=br")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	want := []byte{0x1b, 0x12, 0x00, 0xff, 'b', 'r'}
	if !bytes.Equal(got, want) {
		t.Fatalf("br 字节被改动: got %v want %v", got, want)
	}
	if h := resp.Header.Get("Content-Encoding"); h != "br" {
		t.Fatalf("br 编码头必须保留，got %q", h)
	}
	if h := resp.Header.Get("ETag"); strings.Contains(h, "gw-") {
		t.Fatalf("未改写的响应不该被打上网关指纹: %q", h)
	}
}

// TestNestedChainETagIsHopLocal 外层的 If-None-Match 不得在内层误命中 304。
func TestNestedChainETagIsHopLocal(t *testing.T) {
	html := []byte(`<html><head></head><body><a href="/assets/app.js">x</a></body></html>`)
	backend, _ := gzipChainBackend(t, html)
	defer backend.Close()
	m, gwURL := startSelfGateway(t, 6)
	if err := m.Upsert(activeRoute("a", gwURL)); err != nil {
		t.Fatal(err)
	}
	if err := m.Upsert(activeRoute("c", backend.URL)); err != nil {
		t.Fatal(err)
	}
	client := noDecompressClient()

	// 先拿外层（scope=a）的 ETag
	r1, err := client.Get(gwURL + "/a/c/index.html")
	if err != nil {
		t.Fatal(err)
	}
	outerTag := r1.Header.Get("ETag")
	_, _ = io.Copy(io.Discard, r1.Body)
	_ = r1.Body.Close()
	if outerTag == "" || !strings.Contains(outerTag, "gw-a.") {
		t.Fatalf("外层 ETag 应带 scope=a，实际 %q", outerTag)
	}

	// 同一个 tag 发给内层 URL：内层 scope=c，不得命中 304
	req, _ := http.NewRequest("GET", gwURL+"/c/index.html", nil)
	req.Header.Set("If-None-Match", outerTag)
	inner, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Body.Close()
	if inner.StatusCode == http.StatusNotModified {
		t.Fatal("外层 ETag 不能让内层返回 304")
	}
	if inner.StatusCode != http.StatusOK {
		t.Fatalf("内层期望 200，got %d", inner.StatusCode)
	}
	innerTag := inner.Header.Get("ETag")
	_, _ = io.Copy(io.Discard, inner.Body)
	if !strings.Contains(innerTag, "gw-c.") || innerTag == outerTag {
		t.Fatalf("内层 ETag 应带 scope=c 且不同于外层: %s", innerTag)
	}

	// 外层自己的 tag 再请求同一 URL，应该正常 304
	req2, _ := http.NewRequest("GET", gwURL+"/a/c/index.html", nil)
	req2.Header.Set("If-None-Match", outerTag)
	resp2, err := client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("同跳同内容应 304，got %d", resp2.StatusCode)
	}
}
