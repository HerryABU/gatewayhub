package cookie

import (
	"net/http"
	"strings"
	"testing"
)

func TestHatUnhat(t *testing.T) {
	cases := []struct {
		hat, in, out string
		unhat        bool
	}{
		{"A__", "session", "session", false},
		{"A__", "A__session", "session", true},
		{"A__", "A__", "A__", false},               // 只有帽子，不脱
		{"A__", "B__session", "B__session", false}, // 别人的帽子绝不动
		{"", "session", "session", false},          // 无帽子 = 关闭隔离
		{"A__", "A__x__y", "x__y", true},           // 只脱一次
	}
	for _, c := range cases {
		if got := Hat(c.hat, c.in); c.unhat && got != c.in {
			t.Errorf("Hat(%q,%q) 不应再加帽，得到 %q", c.hat, c.in, got)
		}
		got, ok := Unhat(c.hat, c.in)
		if ok != c.unhat || got != c.out {
			t.Errorf("Unhat(%q,%q) = (%q,%v), want (%q,%v)", c.hat, c.in, got, ok, c.out, c.unhat)
		}
	}
	if got := Hat("A__", "session"); got != "A__session" {
		t.Fatalf("Hat = %q", got)
	}
}

func TestRewriteRequestKeepForeign(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "A__session=abc; B__session=xyz; plain=1")
	res := RewriteRequest(h, "A__", false)
	got := h.Get("Cookie")
	if got != "session=abc; B__session=xyz; plain=1" {
		t.Fatalf("Cookie = %q", got)
	}
	if res.Unhatted != 1 || res.Dropped != 0 || res.Kept != 2 {
		t.Fatalf("结果统计异常: %+v", res)
	}
}

func TestRewriteRequestDropForeign(t *testing.T) {
	h := http.Header{}
	h.Set("Cookie", "A__session=abc; B__session=xyz; plain=1")
	res := RewriteRequest(h, "A__", true)
	if got := h.Get("Cookie"); got != "session=abc" {
		t.Fatalf("Cookie = %q, want session=abc", got)
	}
	if res.Dropped != 2 || res.Unhatted != 1 {
		t.Fatalf("结果统计异常: %+v", res)
	}
}

func TestRewriteRequestMultipleHeadersAndMalformed(t *testing.T) {
	h := http.Header{}
	h.Add("Cookie", "A__a=1; noeq")
	h.Add("Cookie", "A__b=2")
	res := RewriteRequest(h, "A__", false)
	vals := h.Values("Cookie")
	if len(vals) != 2 {
		t.Fatalf("应保留两条 Cookie 头，得到 %v", vals)
	}
	joined := strings.Join(vals, " | ")
	if !strings.Contains(joined, "a=1") || !strings.Contains(joined, "b=2") || !strings.Contains(joined, "noeq") {
		t.Fatalf("修正结果异常: %s", joined)
	}
	if res.Unhatted != 2 {
		t.Fatalf("Unhatted = %d, want 2", res.Unhatted)
	}
}

func TestRewriteRequestNoCookie(t *testing.T) {
	h := http.Header{}
	res := RewriteRequest(h, "A__", true)
	if res != (RequestResult{}) || len(h.Values("Cookie")) != 0 {
		t.Fatalf("无 Cookie 时不应产生副作用: %+v", res)
	}
}

func TestRewriteSetCookiesBasic(t *testing.T) {
	in := []string{
		"session=abc; Path=/; HttpOnly; Secure; SameSite=Lax; Max-Age=3600",
	}
	res := RewriteSetCookies(in, SetCookieOptions{Hat: "A__", Prefix: "/A"})
	if len(res.Values) != 1 || res.Rewritten != 1 {
		t.Fatalf("结果异常: %+v", res)
	}
	got := res.Values[0]
	for _, want := range []string{"A__session=abc", "Path=/A", "HttpOnly", "Secure", "SameSite=Lax", "Max-Age=3600"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q: %s", want, got)
		}
	}
	if strings.Contains(got, "Path=/;") || strings.HasSuffix(got, "Path=/") {
		t.Fatalf("Path 未收紧: %s", got)
	}
}

func TestRewriteSetCookiesPathVariants(t *testing.T) {
	cases := []struct {
		in   string
		want string
		root string
	}{
		{"s=1", "Path=/A", ""},                    // 无 Path：补
		{"s=1; Path=/", "Path=/A", ""},            // 根：收紧
		{"s=1; Path=", "Path=/A", ""},             // 空：收紧
		{"s=1; Path=/login", "Path=/A/login", ""}, // 子路径：加前缀
		{"s=1; Path=/A/x", "Path=/A/x", ""},       // 幂等
		{"s=1; Path=/api/x", "Path=/A/x", "/api"}, // 后端根路径归一
		{"s=1; path=/Login", "Path=/A/Login", ""}, // 属性名大小写不敏感（输出统一为 Path）
	}
	for _, c := range cases {
		res := RewriteSetCookies([]string{c.in}, SetCookieOptions{Hat: "A__", Prefix: "/A", BackendRoot: c.root})
		if len(res.Values) != 1 {
			t.Fatalf("%q 结果数量异常: %+v", c.in, res)
		}
		if !strings.Contains(res.Values[0], c.want) {
			t.Errorf("%q → %q，期望包含 %q", c.in, res.Values[0], c.want)
		}
	}
}

func TestRewriteSetCookiesPreservesOrderAndUnknownAttrs(t *testing.T) {
	in := "s=1; Secure; Partitioned; HttpOnly"
	res := RewriteSetCookies([]string{in}, SetCookieOptions{Hat: "A__", Prefix: "/A"})
	got := res.Values[0]
	if got != "A__s=1; Secure; Partitioned; HttpOnly; Path=/A" {
		t.Fatalf("属性顺序/未知属性被破坏: %s", got)
	}
}

func TestRewriteSetCookiesMultiValueAndInvalid(t *testing.T) {
	in := []string{
		"a=1; Path=/",
		"bad/name=1; Path=/",
		"b=2; Path=/",
	}
	res := RewriteSetCookies(in, SetCookieOptions{Hat: "A__", Prefix: "/A"})
	if len(res.Values) != 2 {
		t.Fatalf("非法 Cookie 应被丢弃，得到 %v", res.Values)
	}
	if len(res.Invalid) != 1 || !strings.Contains(res.Invalid[0], "bad/name") {
		t.Fatalf("未记录非法 Cookie: %+v", res.Invalid)
	}
	if !strings.Contains(res.Values[0], "A__a=1") || !strings.Contains(res.Values[1], "A__b=2") {
		t.Fatalf("多值处理异常: %v", res.Values)
	}

	// PassInvalid：原样透传但依旧记录
	res2 := RewriteSetCookies(in, SetCookieOptions{Hat: "A__", Prefix: "/A", PassInvalid: true})
	if len(res2.Values) != 3 || len(res2.Invalid) != 1 {
		t.Fatalf("PassInvalid 行为异常: %+v", res2)
	}
}

func TestRewriteSetCookiesNoHat(t *testing.T) {
	in := []string{"session=1; Path=/"}
	res := RewriteSetCookies(in, SetCookieOptions{Prefix: "/A"})
	if len(res.Values) != 1 || res.Values[0] != in[0] || res.Rewritten != 0 {
		t.Fatalf("未配置帽子时不应改写: %+v", res)
	}
}

func TestRewriteSetCookiesQuotedValueWithSemicolon(t *testing.T) {
	in := `s="a;b"; Path=/`
	res := RewriteSetCookies([]string{in}, SetCookieOptions{Hat: "A__", Prefix: "/A"})
	if len(res.Values) != 1 {
		t.Fatalf("引号内分号被误切: %+v", res)
	}
	got := res.Values[0]
	if !strings.HasPrefix(got, `A__s="a;b"`) || !strings.Contains(got, "Path=/A") {
		t.Fatalf("引号值处理异常: %s", got)
	}
}
