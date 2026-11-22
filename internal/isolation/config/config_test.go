package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleYAML = `
version: 1
defaults:
  mode: strip
  timeout: 7
breaker:
  failures: 3
  cooldown: 15
redirect_loop:
  threshold: 5
  window: 6
services:
  - name: A
    target: 127.0.0.1:9001
    cookie_hat: A__
    html_rewrite: true
    known_abs_prefixes: ["/static", "/static", " "]
  - name: B
    target: :9002
    mode: pass-through
    cookie_hat: ""
  - name: C
    target: http://10.0.0.9:8080/api
    mode: isolated
    timeout: 2
    host_mode: rewrite
    enabled: false
`

func buildSample(t *testing.T) (*Table, error) {
	t.Helper()
	return parse("test.yaml", []byte(sampleYAML))
}

func TestParseDefaultsAndResolution(t *testing.T) {
	tbl, err := buildSample(t)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := len(tbl.Services); got != 3 {
		t.Fatalf("services = %d, want 3", got)
	}
	a, b, c := tbl.Services[0], tbl.Services[1], tbl.Services[2]

	if a.Prefix != "/A" || a.Mode != ModeStrip || a.Timeout != 7 {
		t.Fatalf("A 解析异常: %+v", a)
	}
	if !a.HTMLRewrite || !a.BodyRewriteEnabled() {
		t.Fatalf("A 的 HTML 改写应生效")
	}
	// 前缀去重 + 去空白
	if len(a.KnownAbsPrefix) != 1 || a.KnownAbsPrefix[0] != "/static" {
		t.Fatalf("known_abs_prefixes 清洗异常: %v", a.KnownAbsPrefix)
	}
	// 显式空帽 = 不加帽
	if b.CookieHat != "" {
		t.Fatalf("B 显式空 cookie_hat 应保持为空，得到 %q", b.CookieHat)
	}
	if b.Mode != ModePassThrough || b.Mode.StripsPrefix() {
		t.Fatalf("B 应为 pass-through 且不剥离前缀")
	}
	// 后端根路径解析 + host_mode
	if c.Root != "/api" || c.HostMode != HostModeRewrite {
		t.Fatalf("C 解析异常: %+v", c)
	}
	if c.Enabled {
		t.Fatalf("C 显式 enabled:false 应保持禁用")
	}
	if c.Timeout != 2 {
		t.Fatalf("C timeout = %d, want 2", c.Timeout)
	}
	// 未配置 cookie_hat 的服务默认用 name + 后缀（此处 C 未配置）
	if c.CookieHat != "C__" {
		t.Fatalf("C 默认帽 = %q, want C__", c.CookieHat)
	}
	if tbl.Breaker.Failures != 3 || tbl.Breaker.Cooldown != 15 {
		t.Fatalf("breaker 解析异常: %+v", tbl.Breaker)
	}
	if tbl.Loop.Threshold != 5 || tbl.Loop.Window != 6 {
		t.Fatalf("redirect_loop 解析异常: %+v", tbl.Loop)
	}
	if tbl.Hash == "" || tbl.Source != "test.yaml" {
		t.Fatalf("快照元信息缺失: %+v", tbl)
	}
}

func TestBuildAggregatesAllIssues(t *testing.T) {
	yml := `
services:
  - name: A
    target: 127.0.0.1:9001
  - name: A
    target: 127.0.0.1:9002
  - name: api
    target: :9003
  - name: bad/name
    target: :9004
  - name: D
    target: ""
  - name: E
    target: :9005
    mode: nonsense
  - name: F
    target: :9006
    cookie_hat: "F=1"
  - name: G
    target: :9007
    known_abs_prefixes: ["static"]
`
	tbl, err := parse("t.yaml", []byte(yml))
	if err == nil {
		t.Fatalf("应当返回聚合错误，实际通过: %+v", tbl)
	}
	issues, ok := err.(*Issues)
	if !ok {
		t.Fatalf("错误类型应为 *Issues，得到 %T", err)
	}
	// 重复名 / 保留字 / 非法字符 / 缺 target / 未知 mode / 非法帽子 / 前缀格式
	if len(issues.Items) < 6 {
		t.Fatalf("应聚合至少 6 条问题，得到 %d: %v", len(issues.Items), issues.Items)
	}
	joined := strings.Join(issues.Items, "\n")
	for _, want := range []string{"重复", "保留字", "非法字符", "缺少 target", "未知 mode", "cookie_hat", "known_abs_prefixes"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("错误信息缺少关键词 %q:\n%s", want, joined)
		}
	}
}

func TestWarnings(t *testing.T) {
	yml := `
defaults:
  mode: isolated
services:
  - name: A
    target: :9001
    html_rewrite: true
    cookie_hat: A__
  - name: B
    target: :9002
    cookie_hat: A__B__
`
	tbl, err := parse("t.yaml", []byte(yml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	joined := strings.Join(tbl.Warnings, "\n")
	if !strings.Contains(joined, "html_rewrite 已开启但 mode=isolated") {
		t.Fatalf("缺少 isolated 下 html_rewrite 无效的告警:\n%s", joined)
	}
	if !strings.Contains(joined, "互为前缀") {
		t.Fatalf("缺少帽子前缀歧义告警:\n%s", joined)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("文件不存在时应报错")
	}
}

func TestWatcherReloadsOnChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "isolation.yaml")
	if err := os.WriteFile(path, []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	tbl, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	w := NewWatcher(path, tbl.Hash, 20*time.Millisecond)
	defer w.Stop()

	got := make(chan *Table, 4)
	errs := make(chan error, 4)
	w.Start(func(t *Table, err error) {
		if err != nil {
			errs <- err
			return
		}
		got <- t
	})

	// 内容不变时不应触发回调
	select {
	case <-got:
		t.Fatal("内容未变化却触发了重载")
	case <-time.After(120 * time.Millisecond):
	}

	changed := strings.Replace(sampleYAML, "name: B", "name: B2", 1)
	if err := os.WriteFile(path, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case t2 := <-got:
		if t2.Services[1].Name != "B2" {
			t.Fatalf("热加载未生效: %+v", t2.Services[1])
		}
	case err := <-errs:
		t.Fatalf("热加载报错: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("热加载超时未触发")
	}
}

// TestBuildNestedName 嵌套服务名：/apps/openlist，Cookie 帽子把 / 换成 .。
func TestBuildNestedName(t *testing.T) {
	svcs, err := (&File{Services: []Spec{
		{Name: "apps/openlist", Target: ":9200", Mode: "strip"},
		{Name: "/apps/minio/", Target: ":9201", Mode: "strip"}, // 前后多余斜杠应被裁掉
	}}).Build()
	if err != nil {
		t.Fatalf("嵌套服务名应被接受: %v", err)
	}
	if svcs[0].Name != "apps/openlist" || svcs[0].Prefix != "/apps/openlist" {
		t.Fatalf("嵌套前缀构建错误: name=%q prefix=%q", svcs[0].Name, svcs[0].Prefix)
	}
	if svcs[0].CookieHat != "apps.openlist__" {
		t.Fatalf("嵌套帽子应把 / 换成 .：got %q", svcs[0].CookieHat)
	}
	if svcs[1].Name != "apps/minio" {
		t.Fatalf("首尾斜杠应被裁掉: got %q", svcs[1].Name)
	}
}

// TestBuildNestedNameIssues 嵌套名的非法形态必须一次性报全。
func TestBuildNestedNameIssues(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"apps//x", "空路径段"},
		{"apps/../x", "路径穿越"},
		{"api/apps", "保留字"},
		{"apps/open list", "非法字符"},
	}
	for _, c := range cases {
		_, err := (&File{Services: []Spec{{Name: c.name, Target: ":9200"}}}).Build()
		if err == nil {
			t.Errorf("%q 应被拒绝", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q 的错误信息应包含 %q，实际: %v", c.name, c.want, err)
		}
	}
}

// TestBuildHatCollision 嵌套名 "." 化后撞帽必须报错（否则两服务互相脱帽）。
func TestBuildHatCollision(t *testing.T) {
	_, err := (&File{Services: []Spec{
		{Name: "apps/openlist", Target: ":9200"},
		{Name: "apps.openlist", Target: ":9201"},
	}}).Build()
	if err == nil || !strings.Contains(err.Error(), "cookie_hat") {
		t.Fatalf("撞帽应报错，实际: %v", err)
	}
}
