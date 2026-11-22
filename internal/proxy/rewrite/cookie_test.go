package rewrite

import (
	"net/http"
	"strings"
	"testing"
)

// setCookieResp 构造一个只带 Set-Cookie 的空响应（收紧逻辑不关心 body）。
func setCookieResp(vals ...string) *http.Response {
	h := http.Header{}
	for _, v := range vals {
		h.Add("Set-Cookie", v)
	}
	return &http.Response{StatusCode: 200, Status: "200 OK", Header: h}
}

func cookiesOf(resp *http.Response) []string {
	return resp.Header.Values("Set-Cookie")
}

// 核心场景：后端把 Path 写死 "/"，必须收到 /go-nvs 下，并补一条根路径删除条，
// 否则浏览器里已有的旧副本（Path=/）继续泄露给兄弟前缀。
func TestTightenSetCookiesRootPath(t *testing.T) {
	resp := setCookieResp("token=abc123; Path=/; Max-Age=900; HttpOnly; SameSite=Lax")
	rw := New("go-nvs", "", "127.0.0.1:8000")
	rw.tightenSetCookies(resp)

	got := cookiesOf(resp)
	if len(got) != 2 {
		t.Fatalf("应产出「收紧后的新 cookie + 根路径删除条」两条，实际 %d 条: %q", len(got), got)
	}
	if !strings.HasPrefix(got[0], "token=abc123;") {
		t.Errorf("第一条应保留原值，实际 %q", got[0])
	}
	if !strings.Contains(got[0], "Path=/go-nvs") {
		t.Errorf("Path 应收紧到 /go-nvs，实际 %q", got[0])
	}
	if strings.Contains(got[0], "Path=/;") || strings.HasSuffix(got[0], "Path=/") {
		t.Errorf("不应残留 Path=/，实际 %q", got[0])
	}
	for _, attr := range []string{"Max-Age=900", "HttpOnly", "SameSite=Lax"} {
		if !strings.Contains(got[0], attr) {
			t.Errorf("属性 %q 必须原样保留，实际 %q", attr, got[0])
		}
	}
	if !strings.Contains(got[1], "token=;") || !strings.Contains(got[1], "Path=/;") ||
		!strings.Contains(got[1], "Max-Age=0") {
		t.Errorf("第二条应是 Path=/ 的过期删除条，实际 %q", got[1])
	}
}

// 后端没写 Path：按 RFC 默认落在请求目录，语义上仍属本服务，补成显式前缀。
func TestTightenSetCookiesMissingPath(t *testing.T) {
	resp := setCookieResp("sid=x1; HttpOnly")
	New("openlist", "", "").tightenSetCookies(resp)

	got := cookiesOf(resp)
	if len(got) != 2 {
		t.Fatalf("缺 Path 时同样要补删除条，实际 %d 条: %q", len(got), got)
	}
	if !strings.Contains(got[0], "Path=/openlist") {
		t.Errorf("缺 Path 应补成 /openlist，实际 %q", got[0])
	}
	if !strings.Contains(got[0], "HttpOnly") {
		t.Errorf("HttpOnly 不能丢，实际 %q", got[0])
	}
}

// 已经在自己前缀下：幂等，绝不能重复加前缀，也不该产生删除条。
func TestTightenSetCookiesAlreadyScoped(t *testing.T) {
	resp := setCookieResp("token=abc; Path=/go-nvs/api")
	New("go-nvs", "", "").tightenSetCookies(resp)

	got := cookiesOf(resp)
	if len(got) != 1 {
		t.Fatalf("已在前缀内不应改动或新增，实际 %d 条: %q", len(got), got)
	}
	if got[0] != "token=abc; Path=/go-nvs/api" {
		t.Errorf("应原样保留，实际 %q", got[0])
	}
}

// 后端挂了 root（target=host:port/api）：先剥 root 再加网关前缀。
func TestTightenSetCookiesBackendRoot(t *testing.T) {
	resp := setCookieResp("a=1; Path=/api/session")
	New("svc", "/api", "").tightenSetCookies(resp)

	got := cookiesOf(resp)
	if !strings.Contains(got[0], "Path=/svc/session") {
		t.Errorf("root=/api 时 /api/session 应归一为 /svc/session，实际 %q", got[0])
	}
	if len(got) != 1 {
		t.Errorf("非根路径不应产生删除条，实际 %d 条: %q", len(got), got)
	}
}

// 后端写了指向别处的 Path（越界）：一律收回本前缀，绝不让它落到兄弟服务下。
func TestTightenSetCookiesForeignPath(t *testing.T) {
	resp := setCookieResp("leak=1; Path=/openlist")
	New("go-nvs", "", "").tightenSetCookies(resp)

	got := cookiesOf(resp)
	if !strings.Contains(got[0], "Path=/go-nvs/openlist") {
		t.Errorf("越界 Path 应收回本前缀，实际 %q", got[0])
	}
}

// 带 Domain 的旧副本：删除条必须带同一个 Domain，否则浏览器按 (name,domain,path)
// 三元组匹配不到，删不掉。
func TestTightenSetCookiesKeepsDomainInClear(t *testing.T) {
	resp := setCookieResp("token=v; Path=/; Domain=example.com")
	New("go-nvs", "", "").tightenSetCookies(resp)

	got := cookiesOf(resp)
	if len(got) != 2 {
		t.Fatalf("应有两条，实际 %q", got)
	}
	if !strings.Contains(got[1], "Domain=example.com") {
		t.Errorf("删除条必须带相同 Domain，实际 %q", got[1])
	}
}

// 多条 cookie 各自处理，绝不合并（Set-Cookie 不支持逗号合并）。
func TestTightenSetCookiesMultiple(t *testing.T) {
	resp := setCookieResp(
		"token=t1; Path=/; HttpOnly",
		"refresh_token=r1; Path=/; HttpOnly",
		"theme=dark; Path=/go-nvs",
	)
	New("go-nvs", "", "").tightenSetCookies(resp)

	got := cookiesOf(resp)
	// token / refresh_token 各产生 1 新 + 1 删除条；theme 已就位不动
	if len(got) != 5 {
		t.Fatalf("应为 5 条，实际 %d 条: %q", len(got), got)
	}
	joined := strings.Join(got, "\n")
	for _, want := range []string{
		"token=t1", "refresh_token=r1", "theme=dark; Path=/go-nvs",
		"token=; Path=/", "refresh_token=; Path=/",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("缺少 %q\n%s", want, joined)
		}
	}
}

// 没有 Set-Cookie 时不应凭空造头。
func TestTightenSetCookiesNoop(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}
	New("go-nvs", "", "").tightenSetCookies(resp)
	if len(resp.Header.Values("Set-Cookie")) != 0 {
		t.Errorf("无 Set-Cookie 时不应新增头: %q", resp.Header.Values("Set-Cookie"))
	}
}

// 畸形/非法 Set-Cookie 原样透传，不能因为解析失败把后端响应搞坏。
func TestTightenSetCookiesMalformed(t *testing.T) {
	resp := setCookieResp("novalue", "=orphan; Path=/")
	New("go-nvs", "", "").tightenSetCookies(resp)

	got := cookiesOf(resp)
	if len(got) != 2 {
		t.Fatalf("畸形条目应原样透传，实际 %q", got)
	}
	if got[0] != "novalue" || got[1] != "=orphan; Path=/" {
		t.Errorf("畸形条目被改动了: %q", got)
	}
}

// cookiePathFor 的归一化表，防止后续改动把 root/前缀处理搞乱。
func TestCookiePathFor(t *testing.T) {
	cases := []struct {
		old, base, root, want string
	}{
		{"", "/A", "", "/A"},
		{"/", "/A", "", "/A"},
		{"/login", "/A", "", "/A/login"},
		{"/A/x", "/A", "", "/A/x"},
		{"/A", "/A", "", "/A"},
		{"/api/x", "/A", "/api", "/A/x"},
		{"/api", "/A", "/api", "/A"},
		{"/api", "/A", "/api/", "/A"},
		{"login", "/A", "", "/A/login"},
	}
	for _, c := range cases {
		if got := cookiePathFor(c.old, c.base, c.root); got != c.want {
			t.Errorf("cookiePathFor(%q,%q,%q) = %q，期望 %q", c.old, c.base, c.root, got, c.want)
		}
	}
}
