package proxy

import (
	"crypto/tls"
	"net/http"
	"testing"

	"gatewayhub/internal/models"
)

// TestTLSSkipVerifyTransport 验证 buildEntry 产出的两条 ReverseProxy 都按路由开关
// 设置 TLSClientConfig。默认必须严格校验；只有显式开启才跳过（宝塔 / WSL 自签证书）。
func TestTLSSkipVerifyTransport(t *testing.T) {
	m := NewManager(nil, nil, "", SelfInfo{Port: 0})
	for _, tc := range []struct {
		name string
		skip bool
	}{
		{"default strict", false},
		{"explicit skip", true},
	} {
		r := models.Route{Prefix: "bt", Target: "https://127.0.0.1:9203", Timeout: 5, TLSSkipVerify: tc.skip}
		entry, err := m.buildEntry(r)
		if err != nil {
			t.Fatalf("%s: buildEntry failed: %v", tc.name, err)
		}
		for label, tr := range map[string]*http.Transport{
			"path": entry.Proxy.Transport.(*http.Transport),
			"sub":  entry.SubProxy.Transport.(*http.Transport),
		} {
			cfg := tr.TLSClientConfig
			got := cfg != nil && cfg.InsecureSkipVerify
			if got != tc.skip {
				t.Errorf("%s / %s: InsecureSkipVerify=%v want %v", tc.name, label, got, tc.skip)
			}
			if tc.skip && cfg != nil && cfg.MinVersion != tls.VersionTLS12 {
				t.Errorf("%s / %s: 跳过校验时也应保持 TLS1.2+ 下限", tc.name, label)
			}
		}
	}
}

func TestDefaultRouteIsStrict(t *testing.T) {
	r := models.Route{Target: "https://example.com"}
	if r.TLSSkipVerify {
		t.Fatal("TLSSkipVerify 默认必须为 false")
	}
}
