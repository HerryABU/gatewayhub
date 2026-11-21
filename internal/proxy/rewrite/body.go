package rewrite

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ---- 响应体读写（gzip 感知） ----

// readBody 读取响应体，返回解压后的内容（decoded）与原始字节（raw）。
// raw 用于「无需重写时原样透传」，避免无谓的解压/重压开销，保证响应速度。
func readBody(resp *http.Response) (decoded, raw []byte, gz bool, err error) {
	raw, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, false, err
	}
	enc := strings.ToLower(resp.Header.Get("Content-Encoding"))
	if strings.Contains(enc, "gzip") {
		gr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, raw, true, err
		}
		defer gr.Close()
		decoded, err = io.ReadAll(gr)
		return decoded, raw, true, err
	}
	return raw, raw, false, nil
}

func writeBody(resp *http.Response, b []byte, gz bool) {
	var buf bytes.Buffer
	if gz {
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write(b)
		_ = zw.Close()
		resp.Header.Set("Content-Encoding", "gzip")
	} else {
		resp.Header.Del("Content-Encoding")
		buf.Write(b)
	}
	resp.Body = io.NopCloser(&buf)
	resp.ContentLength = int64(buf.Len())
	resp.Header.Set("Content-Length", strconv.Itoa(buf.Len()))
}
