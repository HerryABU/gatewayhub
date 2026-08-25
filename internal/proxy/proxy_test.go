package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/models"
	"gatewayhub/internal/stats"
)

func newTestManager(baseDomain string, routes ...models.Route) *Manager {
	m := NewManager(nil, stats.NewWriter(nil, nil, 1, 100, time.Second, 1.0), baseDomain)
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
	m := NewManager(nil, nil, "localhost")
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
	m2 := NewManager(nil, nil, "")
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
