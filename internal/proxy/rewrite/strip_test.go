package rewrite

import (
	"net/http"
	"strings"
	"testing"
)

func TestApplyStripRules(t *testing.T) {
	h := http.Header{}
	h.Add("X-WAF-UUID", "123")
	h.Add("X-Trace-Id", "keep")
	h.Add("Set-Cookie", "x-waf-captcha-referer=; Path=/")
	h.Add("Set-Cookie", "x-waf-sesid=abc; Path=/")
	h.Add("Set-Cookie", "ds_session_id=keep; Path=/")
	resp := &http.Response{Header: h}

	rw := New("qidian", "", "")
	rw.SetStripRules([]string{"X-WAF-UUID"}, []string{"x-waf-"})
	rw.applyStripRules(resp)

	if resp.Header.Get("X-WAF-UUID") != "" {
		t.Error("X-WAF-UUID 头应被删除")
	}
	if resp.Header.Get("X-Trace-Id") != "keep" {
		t.Error("无关头不应被删")
	}
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) != 1 || !strings.Contains(cookies[0], "ds_session_id") {
		t.Errorf("x-waf-* cookie 应被删、其它保留，实际 %v", cookies)
	}
}

func TestApplyStripRulesNoop(t *testing.T) {
	h := http.Header{}
	h.Add("X-WAF-UUID", "123")
	resp := &http.Response{Header: h}
	New("x", "", "").applyStripRules(resp) // 无规则
	if resp.Header.Get("X-WAF-UUID") != "123" {
		t.Error("无规则时不应删任何头")
	}
}
