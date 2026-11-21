package matcher

import (
	"testing"

	"gatewayhub/internal/isolation/config"
)

func testTable(t *testing.T) *Table {
	t.Helper()
	tbl, err := (&config.File{Services: []config.Spec{
		{Name: "A", Target: ":9001", Mode: "strip"},
		{Name: "B", Target: ":9002", Mode: "pass-through"},
		{Name: "off", Target: ":9003", Mode: "isolated", Enabled: boolPtr(false)},
	}}).Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return New(tbl)
}

func boolPtr(b bool) *bool { return &b }

func TestLookup(t *testing.T) {
	tb := testTable(t)
	cases := []struct {
		path string
		name string
		rest string
		ok   bool
	}{
		{"/A", "A", "", true},
		{"/A/", "A", "/", true},
		{"/A/admin", "A", "/admin", true},
		{"/A/admin/x?y=1", "A", "/admin/x?y=1", true}, // URL.Path 不含查询串，但不应破坏
		{"/a/admin", "A", "/admin", true},             // 大小写不敏感
		{"/B/x", "B", "/x", true},
		{"/off/x", "off", "/x", true}, // 禁用服务仍能匹配，由引擎返回 503
		{"/unknown/x", "", "", false}, // 未命中不猜测
		{"/AA/x", "", "", false},      // 不做前缀模糊匹配
		{"/", "", "", false},
		{"", "", "", false},
		{"A/admin", "", "", false}, // 非绝对路径
	}
	for _, c := range cases {
		svc, rest, ok := tb.Lookup(c.path)
		if ok != c.ok {
			t.Errorf("Lookup(%q) ok=%v, want %v", c.path, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if svc.Name != c.name || rest != c.rest {
			t.Errorf("Lookup(%q) = (%s,%q), want (%s,%q)", c.path, svc.Name, rest, c.name, c.rest)
		}
	}
}

func TestHasAndGet(t *testing.T) {
	tb := testTable(t)
	if !tb.Has("A") || !tb.Has("a") {
		t.Fatal("Has 应大小写不敏感")
	}
	if tb.Has("unknown") {
		t.Fatal("Has(unknown) 应为 false")
	}
	if s, ok := tb.Get("B"); !ok || s.Mode != config.ModePassThrough {
		t.Fatalf("Get(B) = %+v,%v", s, ok)
	}
	if tb.Len() != 3 {
		t.Fatalf("Len = %d, want 3", tb.Len())
	}
	if names := tb.Names(); len(names) != 3 || names[0] != "a" {
		t.Fatalf("Names 应排序且小写: %v", names)
	}
}

func TestNilTable(t *testing.T) {
	var tb *Table
	if _, _, ok := tb.Lookup("/A/x"); ok {
		t.Fatal("nil 表不应匹配")
	}
	if tb.Has("A") || tb.Len() != 0 {
		t.Fatal("nil 表 Has/Len 应安全")
	}
}

// Owner 只做越界归因，绝不参与路由（FR-3：不猜测归属）。
func TestOwnerKnownAbsPrefix(t *testing.T) {
	svcs := []*config.Service{
		{Name: "SPA", Prefix: "/SPA", Enabled: true, KnownAbsPrefix: []string{"/assets", "/assets/css"}},
		{Name: "AJAX", Prefix: "/AJAX", Enabled: true, KnownAbsPrefix: []string{"/api"}},
	}
	tbl := New(svcs)

	cases := []struct {
		path   string
		owner  string
		prefix string
		ok     bool
	}{
		{"/assets/app.js", "SPA", "/assets", true},
		{"/assets", "SPA", "/assets", true},
		{"/assets/css/main.css", "SPA", "/assets/css", true}, // 最长前缀优先
		{"/api/public/settings", "AJAX", "/api", true},
		{"/assetsx/evil.js", "", "", false}, // 段边界
		{"/other/x", "", "", false},
		{"/", "", "", false},
	}
	for _, c := range cases {
		svc, pfx, ok := tbl.Owner(c.path)
		if ok != c.ok {
			t.Fatalf("%s: ok=%v, want %v", c.path, ok, c.ok)
		}
		if !ok {
			continue
		}
		if svc.Name != c.owner || pfx != c.prefix {
			t.Fatalf("%s: got %s/%s, want %s/%s", c.path, svc.Name, pfx, c.owner, c.prefix)
		}
		// 归因不得让 Lookup 命中（路由必须仍然是 404）
		if _, _, hit := tbl.Lookup(c.path); hit {
			t.Fatalf("%s: 越界归因绝不能变成路由命中", c.path)
		}
	}
}
