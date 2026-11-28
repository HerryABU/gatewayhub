package health

import (
	"testing"
	"time"

	"gatewayhub/internal/config"
	"gatewayhub/internal/models"
)

// TestProbeCertDaysNoPort target.Parse 对 https://host 保留的 Host 无端口，
// tls.Dial 必须补 :443，否则证书探测永远失败（cert_days 恒为 0）。
func TestProbeCertDaysNoPort(t *testing.T) {
	c := New(nil, config.HealthConfig{Timeout: 5, Interval: 30})
	days := c.probeCertDays("https://chat.deepseek.com")
	if days <= 0 {
		t.Errorf("无端口 HTTPS host 的证书天数应 > 0，实际 %d", days)
	}
}

// TestProbeCertDaysHTTP HTTP 后端不适用，应返回 0。
func TestProbeCertDaysHTTP(t *testing.T) {
	c := New(nil, config.HealthConfig{Timeout: 2, Interval: 30})
	if d := c.probeCertDays("http://127.0.0.1:8080"); d != 0 {
		t.Errorf("HTTP 后端 cert_days 应为 0，实际 %d", d)
	}
}

// TestInfoIncludesCertDays InfoMap 应把证书天数带出去供 API 返回。
func TestInfoIncludesCertDays(t *testing.T) {
	c := New(nil, config.HealthConfig{Timeout: 2, Interval: 30})
	c.state["x"] = &routeHealth{status: models.HealthHealthy, certDays: 42}
	if got := c.InfoMap()["x"].CertDays; got != 42 {
		t.Errorf("InfoMap 未带出证书天数: %d", got)
	}
	_ = time.Now()
}
