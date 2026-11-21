package respond

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
)

// Decodable 判断 Content-Encoding 是否可解码后改写。
// 只支持 identity 与 gzip：br/zstd 等原样透传（宁可不改写，也不冒解压乱码的风险）。
func Decodable(enc string) bool {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "", "identity", "gzip", "x-gzip":
		return true
	}
	return false
}

// Decode 解压响应体，返回明文字节与「原始是否 gzip」。
func Decode(body []byte, enc string) ([]byte, bool, error) {
	e := strings.ToLower(strings.TrimSpace(enc))
	if e != "gzip" && e != "x-gzip" {
		if e == "" || e == "identity" {
			return body, false, nil
		}
		return nil, false, fmt.Errorf("unsupported content-encoding: %s", enc)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, true, fmt.Errorf("gzip 解压失败: %w", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return nil, true, fmt.Errorf("gzip 读取失败: %w", err)
	}
	return out, true, nil
}

// Encode 按需要重新压缩。改写后的响应默认输出明文（并删除 Content-Encoding），
// 只有当调用方明确要求保持压缩时才重新 gzip。
func Encode(body []byte, gzipOut bool) ([]byte, string) {
	if !gzipOut {
		return body, ""
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(body)
	_ = zw.Close()
	return buf.Bytes(), "gzip"
}
