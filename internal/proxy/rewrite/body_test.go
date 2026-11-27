package rewrite

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func respWith(body []byte, enc string) *http.Response {
	h := http.Header{}
	if enc != "" {
		h.Set("Content-Encoding", enc)
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestReadWriteGzipRoundTrip(t *testing.T) {
	payload := []byte(`<html><a href="/assets/app.js">x</a></html>`)
	var gzipped bytes.Buffer
	zw := gzip.NewWriter(&gzipped)
	_, _ = zw.Write(payload)
	_ = zw.Close()
	resp := respWith(gzipped.Bytes(), "gzip")
	decoded, raw, enc, err := readBody(resp)
	if err != nil || enc != encGzip || !bytes.Equal(decoded, payload) {
		t.Fatalf("gzip 解码失败: enc=%q err=%v decoded=%q", enc, err, decoded)
	}
	if bytes.Equal(raw, payload) {
		t.Fatal("raw 应是压缩字节，不该等于明文")
	}
	decoded[0] = 'X' // 模拟改写
	writeBody(resp, decoded, enc)
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("重打包后应仍是 gzip，got %q", got)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("响应不是合法 gzip: %v", err)
	}
	defer zr.Close()
	got, _ := io.ReadAll(zr)
	if string(got) != "X"+string(payload[1:]) {
		t.Fatalf("二次解码内容不符: %q", got)
	}
	if resp.ContentLength != int64(len(out)) {
		t.Fatalf("ContentLength 未同步: %d != %d", resp.ContentLength, len(out))
	}
}

func TestReadWriteDeflateRoundTrip(t *testing.T) {
	payload := []byte(`fetch("/api/x")`)
	var buf bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&buf, flate.DefaultCompression)
	_, _ = zw.Write(payload)
	_ = zw.Close()

	resp := respWith(buf.Bytes(), "deflate")
	decoded, _, enc, err := readBody(resp)
	if err != nil || enc != encDeflate || !bytes.Equal(decoded, payload) {
		t.Fatalf("deflate 解码失败: enc=%q err=%v got=%q", enc, err, decoded)
	}
	writeBody(resp, decoded, enc)
	if resp.Header.Get("Content-Encoding") != "deflate" {
		t.Fatal("deflate 重打包后编码头丢了")
	}
	out, _ := io.ReadAll(resp.Body)
	zr, err := zlib.NewReader(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("重打包后不是合法 deflate: %v", err)
	}
	defer zr.Close()
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, payload) {
		t.Fatalf("deflate 往返内容不符: %q", got)
	}
}

func TestReadBodyRawDeflateFallback(t *testing.T) {
	payload := []byte("raw deflate payload")
	var buf bytes.Buffer
	fw, _ := flate.NewWriter(&buf, flate.DefaultCompression)
	_, _ = fw.Write(payload)
	_ = fw.Close()

	decoded, _, enc, err := readBody(respWith(buf.Bytes(), "deflate"))
	if err != nil || enc != encDeflate || string(decoded) != string(payload) {
		t.Fatalf("裸 deflate 应兼容: enc=%q err=%v got=%q", enc, err, decoded)
	}
}

func TestReadBodyRestoresOnBrokenGzip(t *testing.T) {
	broken := []byte("this is not gzip at all")
	resp := respWith(broken, "gzip")
	_, raw, enc, err := readBody(resp)
	if err == nil {
		t.Fatal("坏 gzip 应报错")
	}
	if enc != encIdentity || !bytes.Equal(raw, broken) {
		t.Fatalf("失败时应返回原始字节: enc=%q raw=%q", enc, raw)
	}
	// 关键回归：解压失败后 resp.Body 必须还有内容，否则下游拿到空响应
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, broken) {
		t.Fatalf("Body 未被还原，下游读到 %d 字节（应为 %d）", len(got), len(broken))
	}
}

func TestUnsupportedEncodingIsNotTouched(t *testing.T) {
	for _, enc := range []string{"br", "zstd", "gzip, br"} {
		if _, ok := encodingOf(enc); ok {
			t.Errorf("%q 不应被判定为可改写编码", enc)
		}
		// Modify 对不支持的编码应直接返回，body 与头都原样保留。
		brBody := []byte{0x1b, 0x12, 0x00, 0xff}
		resp := respWith(brBody, enc)
		resp.Header.Set("Content-Type", "text/html")
		rw := New("a/b", "", "backend:80")
		if err := rw.Modify(resp); err != nil {
			t.Fatalf("%s: Modify 不应报错: %v", enc, err)
		}
		got, _ := io.ReadAll(resp.Body)
		if !bytes.Equal(got, brBody) {
			t.Fatalf("%s: body 被改动了: %v", enc, got)
		}
		if resp.Header.Get("Content-Encoding") != enc {
			t.Fatalf("%s: 编码头被删/改写: %q", enc, resp.Header.Get("Content-Encoding"))
		}
	}
}

func TestEncodingOfIdentity(t *testing.T) {
	for _, raw := range []string{"", "identity", "IDENTITY", "x-gzip", "GZip"} {
		enc, ok := encodingOf(raw)
		if !ok {
			t.Errorf("%q 应可识别", raw)
			continue
		}
		if strings.ToLower(raw) == "x-gzip" && enc != encGzip {
			t.Errorf("x-gzip 应归一到 gzip，got %q", enc)
		}
	}
}

func TestReadBodySizeLimit(t *testing.T) {
	old := MaxBodyBytes
	MaxBodyBytes = 64
	defer func() { MaxBodyBytes = old }()

	// Content-Length 已知且超限：不读，body 保持完整
	payload := strings.Repeat("0123456789abcdef", 4) + "X" // 65 字节 > 上限 64
	resp := respWith([]byte(payload), "")
	resp.ContentLength = int64(len(payload))
	_, _, _, err := readBody(resp)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("超限应返回 ErrBodyTooLarge，got %v", err)
	}
	got, _ := io.ReadAll(resp.Body)
	if len(got) != len(payload) {
		t.Fatalf("超限后 body 必须原样保留，got %d 字节", len(got))
	}

	// 未知长度（chunked）：多读的 1 字节要拼回，不能截断
	resp2 := respWith([]byte(strings.Repeat("x", 100)), "")
	resp2.ContentLength = -1
	_, _, _, err = readBody(resp2)
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("chunked 超限应返回 ErrBodyTooLarge，got %v", err)
	}
	got, _ = io.ReadAll(resp2.Body)
	if len(got) != 100 {
		t.Fatalf("chunked 超限后应完整还原 100 字节，got %d", len(got))
	}
}

func TestDecodeGzipBomb(t *testing.T) {
	old := MaxBodyBytes
	MaxBodyBytes = 1024
	defer func() { MaxBodyBytes = old }()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(bytes.Repeat([]byte("A"), 100*1024))
	_ = zw.Close()

	if _, err := decodeGzip(buf.Bytes(), MaxBodyBytes); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("解压炸弹应被拦，got %v", err)
	}
}
