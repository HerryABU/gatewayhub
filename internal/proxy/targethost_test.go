package proxy

import (
	"testing"

	"gatewayhub/internal/models"
)

// TestMatchTargetHost 官方域名 Host 精确匹配 target host，是 iframe 透传的基础：
// 浏览器用官方域名访问网关时，网关据此识别归属路由，iframe 内 hostname 天然正确。
func TestMatchTargetHost(t *testing.T) {
	m := &Manager{m: map[string]*Entry{
		"fanq": {Route: models.Route{Prefix: "fanq", Target: "https://fanqienovel.com/", Status: models.StatusActive}},
		"dps":  {Route: models.Route{Prefix: "dps", Target: "https://chat.deepseek.com", Status: models.StatusActive}},
		"off":  {Route: models.Route{Prefix: "off", Target: "https://off.example.com", Status: models.StatusInactive}},
	}}
	cases := []struct {
		host   string
		prefix string
		ok     bool
	}{
		{"fanqienovel.com", "fanq", true},
		{"fanqienovel.com:8088", "fanq", true},
		{"chat.deepseek.com", "dps", true},
		{"off.example.com", "", false}, // 停用路由不匹配
		{"unknown.com", "", false},
		{"localhost", "", false}, // 本机访问走路径模式
		{"127.0.0.1:8088", "", false},
	}
	for _, c := range cases {
		p, _, ok := m.MatchTargetHost(c.host)
		if ok != c.ok || (c.ok && p != c.prefix) {
			t.Errorf("host=%q got (%q,%v) want (%q,%v)", c.host, p, ok, c.prefix, c.ok)
		}
	}
}
