package respond

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func TestDetectKind(t *testing.T) {
	cases := []struct {
		ct   string
		want Kind
	}{
		{"text/html", KindHTML},
		{"text/html; charset=utf-8", KindHTML},
		{"application/xhtml+xml", KindHTML},
		{"text/css; charset=gbk", KindCSS},
		{"application/json", KindOther},
		{"application/javascript", KindOther},
		{"image/png", KindOther},
		{"", KindOther},
	}
	for _, c := range cases {
		if got := DetectKind(c.ct); got != c.want {
			t.Errorf("DetectKind(%q) = %v, want %v", c.ct, got, c.want)
		}
	}
}

func TestRewriteLiteralsHTML(t *testing.T) {
	in := `<html><head>
<link rel="stylesheet" href="/css/app.css">
<script src='/js/app.js'></script>
</head><body>
<a href="/page">p</a>
<form action="/submit"></form>
<img src="/img/logo.png">
<img src="//cdn.example.com/x.png">
<img src="/A/img/already.png">
<a href="mailto:a@b.c">m</a>
<div style="background:url(/img/bg.png)"></div>
<div style="background:url('/img/q.png')"></div>
</body></html>`
	out, hits := RewriteLiterals([]byte(in), KindHTML, "/A")
	s := string(out)
	for _, want := range []string{
		`href="/A/css/app.css"`,
		`src='/A/js/app.js'`,
		`href="/A/page"`,
		`action="/A/submit"`,
		`src="/A/img/logo.png"`,
		`url(/A/img/bg.png)`,
		`url('/A/img/q.png')`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("缺少改写结果 %q\n%s", want, s)
		}
	}
	for _, keep := range []string{`src="//cdn.example.com/x.png"`, `src="/A/img/already.png"`, `href="mailto:a@b.c"`} {
		if !strings.Contains(s, keep) {
			t.Errorf("不应被改写 %q", keep)
		}
	}
	if hits != 7 {
		t.Errorf("hits = %d, want 7", hits)
	}
}

func TestRewriteLiteralsCSS(t *testing.T) {
	in := `@font-face{src:url(/fonts/a.woff2) format("woff2")}
.a{background:url("/img/a.png")}
.b{background:url('/img/b.png')}
.c{background:url(data:image/png;base64,AAA)}
.d{background:url(//cdn/x.png)}
.e{background:url(/A/img/already.png)}`
	out, hits := RewriteLiterals([]byte(in), KindCSS, "/A")
	s := string(out)
	for _, want := range []string{"url(/A/fonts/a.woff2)", `url("/A/img/a.png")`, `url('/A/img/b.png')`} {
		if !strings.Contains(s, want) {
			t.Errorf("缺少改写结果 %q\n%s", want, s)
		}
	}
	for _, keep := range []string{"data:image/png", "//cdn/x.png", "/A/img/already.png"} {
		if !strings.Contains(s, keep) {
			t.Errorf("不应被改写 %q", keep)
		}
	}
	if hits != 3 {
		t.Errorf("hits = %d, want 3", hits)
	}
}

func TestRewriteLiteralsNoHitReturnsSameBytes(t *testing.T) {
	in := []byte(`<a href="/A/x">ok</a>`)
	out, hits := RewriteLiterals(in, KindHTML, "/A")
	if hits != 0 {
		t.Fatalf("hits = %d, want 0", hits)
	}
	if !bytes.Equal(out, in) {
		t.Fatal("无命中时必须原样返回（不得复制/改动字节）")
	}
}

func TestRewriteLiteralsGuards(t *testing.T) {
	in := []byte(`<a href="/x">`)
	if out, hits := RewriteLiterals(in, KindHTML, ""); hits != 0 || !bytes.Equal(out, in) {
		t.Fatal("空前缀不应改写")
	}
	if out, hits := RewriteLiterals(in, KindOther, "/A"); hits != 0 || !bytes.Equal(out, in) {
		t.Fatal("非 HTML/CSS 不应改写")
	}
}

func TestCodecRoundTrip(t *testing.T) {
	plain := []byte("<a href=\"/x\">hello</a>")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(plain); err != nil {
		t.Fatal(err)
	}
	_ = zw.Close()

	decoded, wasGzip, err := Decode(buf.Bytes(), "gzip")
	if err != nil || !wasGzip || !bytes.Equal(decoded, plain) {
		t.Fatalf("Decode 失败: %v %v", err, wasGzip)
	}
	if _, _, err := Decode([]byte("x"), "br"); err == nil {
		t.Fatal("br 应视为不支持")
	}
	if Decodable("br") || !Decodable("") || !Decodable("gzip") || !Decodable("identity") {
		t.Fatal("Decodable 判定异常")
	}
	out, enc := Encode(plain, false)
	if enc != "" || !bytes.Equal(out, plain) {
		t.Fatal("不压缩时编码名应为空且内容不变")
	}
	out2, enc2 := Encode(plain, true)
	if enc2 != "gzip" {
		t.Fatalf("enc = %q", enc2)
	}
	back, _, err := Decode(out2, enc2)
	if err != nil || !bytes.Equal(back, plain) {
		t.Fatalf("重新压缩后解码失败: %v", err)
	}
}

// TestRewriteLiteralsSkipsDataSrc 校验「延迟地址」属性的边界：
// data-src / data-href 的值会被前端再拼一次基准（如 vite legacy 的
// __dynamic_base__ + data-src），提前加前缀会拼成 /A/A/... 的双重前缀。
// 属性名判定必须以空白起始，不能靠 \b（"data-src" 的 src 前是 '-'）。
func TestRewriteLiteralsSkipsDataSrc(t *testing.T) {
	in := `<html><head>
<script nomodule id="vite-legacy-entry" data-src="/assets/index-legacy.js"></script>
<script data-src='/assets/polyfills.js'></script>
<a data-href="/x">x</a>
<img src="/assets/p.png">
</head></html>`
	out, _ := RewriteLiterals([]byte(in), KindHTML, "/A")
	s := string(out)

	// 真正参与解析的 src 仍然要加前缀
	if !strings.Contains(s, `src="/A/assets/p.png"`) {
		t.Errorf("普通 src 应加前缀\n%s", s)
	}
	// 延迟地址保持原样（交给前端与基准拼接）
	for _, keep := range []string{
		`data-src="/assets/index-legacy.js"`,
		`data-src='/assets/polyfills.js'`,
		`data-href="/x"`,
	} {
		if !strings.Contains(s, keep) {
			t.Errorf("延迟地址不应改写，缺少 %q\n%s", keep, s)
		}
	}
	if strings.Contains(s, "/A/A/") {
		t.Errorf("出现双重前缀\n%s", s)
	}
}
