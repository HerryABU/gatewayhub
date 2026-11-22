package rewrite

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ---- 响应体读写（内容编码感知） ----
//
// 嵌套反代里，一个响应可能经过多层网关：每层都可能是上一层的「后端」，
// 而每一层都可能用 gzip/deflate 压缩。规则只有两条：
//
//  1. 只改写**能解开**的编码（identity / gzip / deflate）；
//  2. 解不开的（br / zstd / 多段编码）**一个字节都不碰**、原头原样透传 ——
//     宁可不改写，也绝不能把压缩字节当明文发出去（那会直接白屏）。
//
// 历史上这里会无脑删掉 Content-Encoding 并写入明文改写结果：后端一旦回 br，
// 浏览器就拿着 br 头去解一份明文 HTML，页面直接崩。嵌套链把这种错误放大 N 倍。

// bodyEncoding 是本包能安全解开并重新打包的编码名（空串 = identity）。
type bodyEncoding string

const (
	encIdentity bodyEncoding = ""
	encGzip     bodyEncoding = "gzip"
	encDeflate  bodyEncoding = "deflate"
)

// encodingOf 归一化 Content-Encoding。只接受单一取值；
// "gzip, br" 这类多段编码谁都不能安全解开，按「不支持」处理。
func encodingOf(raw string) (bodyEncoding, bool) {
	e := strings.ToLower(strings.TrimSpace(raw))
	switch e {
	case "", "identity":
		return encIdentity, true
	case "gzip", "x-gzip":
		return encGzip, true
	case "deflate":
		return encDeflate, true
	default:
		return encIdentity, false
	}
}

// readBody 读取响应体。
//
// 成功时返回解压后的 decoded、原始字节 raw 与实际编码 enc；raw 用于「无需改写时原样透传」，
// 避免无谓解压/重压。失败时**必须把已读走的字节塞回去**：调用方的约定是
// 「出错就原样透传」，若这里把 Body 读空却不还原，客户端收到的是空响应（静默截断）。
func readBody(resp *http.Response) (decoded, raw []byte, enc bodyEncoding, err error) {
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, raw, encIdentity, err
	}
	enc, ok := encodingOf(resp.Header.Get("Content-Encoding"))
	if !ok {
		return nil, raw, encIdentity, io.EOF // 调用方据此「原样透传」
	}
	switch enc {
	case encGzip:
		decoded, err = decodeGzip(raw)
	case encDeflate:
		decoded, err = decodeDeflate(raw)
	default:
		return raw, raw, encIdentity, nil
	}
	if err != nil {
		// 关键：解压失败也要把原始字节还回去，不能让下游拿到空 body。
		resp.Body = io.NopCloser(bytes.NewReader(raw))
		return nil, raw, encIdentity, err
	}
	return decoded, raw, enc, nil
}

func decodeGzip(raw []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// decodeDeflate 兼容两种历史实现：zlib 包裹（RFC 1950，浏览器/Go 常用）与
// 裸 deflate（RFC 1951，老 IIS / 手写服务常见）。先试 zlib，失败再试裸流。
func decodeDeflate(raw []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err == nil {
		out, readErr := io.ReadAll(zr)
		_ = zr.Close()
		return out, readErr
	}
	fr := flate.NewReader(bytes.NewReader(raw))
	out, readErr := io.ReadAll(fr)
	_ = fr.Close()
	return out, readErr
}

// writeBody 写入改写后的字节，并按原编码重新打包。
//
// 编码名取自响应头而不是布尔 gz：嵌套链里每一跳都可能换编码，
// 用「是否 gzip」的布尔值会把 deflate 误判成 identity 而删掉压缩头。
func writeBody(resp *http.Response, b []byte, enc bodyEncoding) {
	var buf bytes.Buffer
	switch enc {
	case encGzip:
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(b)
		_ = zw.Close()
		resp.Header.Set("Content-Encoding", "gzip")
	case encDeflate:
		zw, _ := zlib.NewWriterLevel(&buf, flate.DefaultCompression)
		_, _ = zw.Write(b)
		_ = zw.Close()
		resp.Header.Set("Content-Encoding", "deflate")
	default:
		resp.Header.Del("Content-Encoding")
		buf.Write(b)
	}
	resp.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
	resp.ContentLength = int64(buf.Len())
	resp.Header.Set("Content-Length", strconv.Itoa(buf.Len()))
}
