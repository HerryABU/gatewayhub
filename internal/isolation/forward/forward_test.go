package forward

import (
	"net/http"
	"testing"

	"gatewayhub/internal/isolation/config"
)

func TestPath(t *testing.T) {
	cases := []struct {
		mode   config.Mode
		rest   string
		want   string
		prefix string
	}{
		{config.ModeStrip, "", "/", "/A"},
		{config.ModeStrip, "/", "/", "/A"},
		{config.ModeStrip, "/admin", "/admin", "/A"},
		{config.ModeStrip, "/admin/", "/admin/", "/A"},
		{config.ModeStrip, "/a/../b", "/b", "/A"}, // 路径穿越被吃掉
		{config.ModeStrip, "/../../etc/passwd", "/etc/passwd", "/A"},
		{config.ModeIsolated, "/x", "/x", "/A"},
		{config.ModePassThrough, "", "/A", "/A"},
		{config.ModePassThrough, "/admin", "/A/admin", "/A"},
	}
	for _, c := range cases {
		if got := Path(c.mode, c.prefix, c.rest); got != c.want {
			t.Errorf("Path(%s,%q) = %q, want %q", c.mode, c.rest, got, c.want)
		}
	}
}

func TestApplyHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://proxy.example/A/x", nil)
	req.Host = "proxy.example"
	req.RemoteAddr = "203.0.113.9:5555"
	// 客户端伪造的归属头：不信任时不得采纳
	req.Header.Set("X-Forwarded-Prefix", "/evil")
	req.Header.Set("X-Real-IP", "9.9.9.9")

	clientIP := ClientIP(req.RemoteAddr, req.Header, false)
	Apply(req, Headers{
		ClientIP:   clientIP,
		OriginHost: req.Host,
		Proto:      Proto(req),
		Prefix:     "/A",
		PriorXFF:   "1.1.1.1", // 上游代理链
	})

	if got := req.Header.Get("X-Forwarded-For"); got != "1.1.1.1, 203.0.113.9" {
		t.Errorf("X-Forwarded-For = %q, want 追加而非覆盖", got)
	}
	if got := req.Header.Get("X-Real-IP"); got != "203.0.113.9" {
		t.Errorf("X-Real-IP = %q, want 203.0.113.9", got)
	}
	if got := req.Header.Get("X-Forwarded-Host"); got != "proxy.example" {
		t.Errorf("X-Forwarded-Host = %q", got)
	}
	if got := req.Header.Get("X-Forwarded-Prefix"); got != "/A" {
		t.Errorf("X-Forwarded-Prefix 被伪造值污染: %q", got)
	}
	if got := req.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto = %q", got)
	}
}

func TestClientIPFallback(t *testing.T) {
	if got := ClientIP("192.168.1.9:1234", http.Header{}, false); got != "192.168.1.9" {
		t.Fatalf("ClientIP = %q", got)
	}
	h := http.Header{}
	h.Set("X-Real-IP", "8.8.8.8")
	if got := ClientIP("192.168.1.9:1234", h, true); got != "8.8.8.8" {
		t.Fatalf("信任反代时应优先 X-Real-IP: %q", got)
	}
	// 不信任时忽略客户端伪造的头（NFR-5）
	if got := ClientIP("192.168.1.9:1234", h, false); got != "192.168.1.9" {
		t.Fatalf("不应信任客户端 X-Real-IP: %q", got)
	}
}

func TestIsUpgrade(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://x/", nil)
	if IsUpgrade(req) {
		t.Fatal("普通请求不应判定为升级")
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	if !IsUpgrade(req) {
		t.Fatal("WebSocket 应判定为升级")
	}
}
