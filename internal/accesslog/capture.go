package accesslog

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	// maxBodySniff 只嗅探请求体前 64KB：写入字段的上限就是 64KB，多读纯属浪费内存。
	// （NVS 用 4MB 缓冲再截断到 64KB；这里读得更少、落盘结果一致。）
	maxBodySniff = 64 << 10

	placeholderMultipart = "[multipart/form-data 文件上传]"
	placeholderBinary    = "[binary body"
)

// HeadersOf 把请求头转成 map（多值用 "; " 连接，与 NVS 的 headers 字段一致）。
func HeadersOf(h http.Header) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, vs := range h {
		out[k] = strings.Join(vs, "; ")
	}
	return out
}

// SniffBody 取请求体片段用于日志，并把请求体完整还原给后续 handler / 反向代理。
//
// 与 NVS 的 AccessLog 中间件同一套规则：multipart 绝不读取（读取再还原会截断大文件，
// 导致上游解析 multipart 报 boundary 缺失）；其余类型读前 64KB 后用 io.MultiReader
// 把「已读部分 + 未读剩余流」拼回去，任意大小的正文都能完整转发。
//
// 幂等性：调用方应在转发之前调用一次；不要重复调用（重复调用会取到空串）。
func SniffBody(r *http.Request) string {
	if r == nil || r.Body == nil || r.Body == http.NoBody {
		return ""
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "multipart/form-data") || strings.HasPrefix(ct, "multipart/mixed") {
		return placeholderMultipart
	}
	// 二进制正文只记类型与长度，避免把图片/字体塞进 JSONL（转义后体积翻数倍）
	if isBinary(ct) {
		return placeholderBinary + " " + strconv.FormatInt(r.ContentLength, 10) + " bytes]"
	}

	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBodySniff))
	// 无论读写是否出错，都要把「已读到的部分 + 剩余流」还原，否则正文直接丢字节
	r.Body = &sniffedBody{
		Reader: io.MultiReader(bytes.NewReader(buf), r.Body),
		closer: r.Body,
	}
	if err != nil {
		return ""
	}
	return string(buf)
}

// sniffedBody 在还原读流的同时保留原始 Body 的 Close 语义，
// 否则 net/http 关闭连接时拿不到原始 Body（连接复用与上传中断处理都会受影响）。
type sniffedBody struct {
	io.Reader
	closer io.Closer
}

func (b *sniffedBody) Close() error { return b.closer.Close() }

// isBinary 判断 Content-Type 是否属于「不该写进日志的二进制正文」。
func isBinary(ct string) bool {
	for _, p := range []string{"image/", "video/", "audio/", "font/",
		"application/octet-stream", "application/pdf", "application/zip",
		"application/gzip", "application/x-protobuf"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	return false
}
