package respond

import (
	"testing"

	"gatewayhub/internal/isolation/config"
)

func TestRewriteLocationStrip(t *testing.T) {
	opt := LocationOptions{Mode: config.ModeStrip, Prefix: "/A", BackendHost: "127.0.0.1:9001"}
	cases := []struct {
		in      string
		want    string
		changed bool
	}{
		{"/home", "/A/home", true},
		{"/", "/A/", true},
		{"/A/home", "/A/home", false},
		{"/A", "/A", false},
		{"/abc", "/A/abc", true}, // 段边界：/A 不是 /abc 的前缀
		{"http://127.0.0.1:9001/home?x=1", "/A/home?x=1", true},
		{"http://127.0.0.1:9001/", "/A/", true},
		{"//127.0.0.1:9001/x", "/A/x", true},
		{"https://example.com/home", "https://example.com/home", false},
		{"//cdn.example.com/x", "//cdn.example.com/x", false},
		{"home", "home", false},
		{"../x", "../x", false},
		{"", "", false},
		{"#frag", "#frag", false},
	}
	for _, c := range cases {
		got, changed := RewriteLocation(c.in, opt)
		if got != c.want || changed != c.changed {
			t.Errorf("RewriteLocation(%q) = (%q,%v), want (%q,%v)", c.in, got, changed, c.want, c.changed)
		}
	}
}

func TestRewriteLocationFragmentAndQuery(t *testing.T) {
	opt := LocationOptions{Mode: config.ModeIsolated, Prefix: "/A", BackendHost: "10.0.0.5:8080"}
	got, changed := RewriteLocation("http://10.0.0.5:8080/p#sec", opt)
	if !changed || got != "/A/p#sec" {
		t.Fatalf("片段保留失败: %q (%v)", got, changed)
	}
}

func TestRewriteLocationPassThrough(t *testing.T) {
	opt := LocationOptions{Mode: config.ModePassThrough, Prefix: "/B", BackendHost: "127.0.0.1:9002"}
	if got, changed := RewriteLocation("/home", opt); got != "/home" || changed {
		t.Fatalf("pass-through 默认不应补前缀: %q (%v)", got, changed)
	}
	opt.FixPassThrough = true
	if got, changed := RewriteLocation("/home", opt); got != "/B/home" || !changed {
		t.Fatalf("显式开启修正后应补前缀: %q (%v)", got, changed)
	}
	// 已带前缀永远幂等
	if got, changed := RewriteLocation("/B/home", opt); got != "/B/home" || changed {
		t.Fatalf("已带前缀不应重复加: %q (%v)", got, changed)
	}
}

func TestRewriteLocationIsolated(t *testing.T) {
	opt := LocationOptions{Mode: config.ModeIsolated, Prefix: "/A", BackendHost: "127.0.0.1:9001"}
	if got, changed := RewriteLocation("/home", opt); got != "/A/home" || !changed {
		t.Fatalf("isolated 仍应修正头层 Location: %q (%v)", got, changed)
	}
}
