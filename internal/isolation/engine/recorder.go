package engine

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
)

// recorder 是响应写入拦截器：捕获状态码与字节数（用于指标），
// 并透传 Flush/Hijack/Unwrap，保证 SSE 与 WebSocket 升级不被破坏。
type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool
	bytes  int64
}

// newRecorder 包装写入器。
func newRecorder(w http.ResponseWriter) *recorder {
	return &recorder{ResponseWriter: w, status: http.StatusOK}
}

// WriteHeader 记录首个状态码（重复调用只生效一次，与 net/http 语义一致）。
func (r *recorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

// Write 记录响应字节数。
func (r *recorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Flush 透传刷新（SSE / 流式响应）。
func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack 透传连接劫持（WebSocket / Upgrade）。
func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return h.Hijack()
}

// Unwrap 让 http.ResponseController 能拿到底层写入器。
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Status 返回捕获的状态码。
func (r *recorder) Status() int { return r.status }

// Bytes 返回已写出字节数。
func (r *recorder) Bytes() int64 { return r.bytes }
