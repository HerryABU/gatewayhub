package accesslog

import (
	"net/url"
	"regexp"
	"strings"
)

// 敏感信息脱敏（可选，默认关闭；生产环境建议 access_log.redact_sensitive: true）。
//
// 与 NVS 的 Redactor 同一目的：Cookie / Authorization 里的令牌、body 与 query 里的
// 口令类字段在**落盘之前**替换掉，日志文件外泄时不会连带泄露凭据。

// sensitiveHeaders 这些请求头整体替换为 [redacted]。
var sensitiveHeaders = map[string]bool{
	"Cookie":              true,
	"Set-Cookie":          true,
	"Authorization":       true,
	"Proxy-Authorization": true,
	"X-Api-Key":           true,
	"X-Auth-Token":        true,
}

// sensitiveParams query / JSON 字段名（小写比较）。
var sensitiveParams = map[string]bool{
	"password":      true,
	"passwd":        true,
	"pwd":           true,
	"token":         true,
	"access_token":  true,
	"refresh_token": true,
	"id_token":      true,
	"secret":        true,
	"api_key":       true,
	"apikey":        true,
	"authorization": true,
}

const redacted = "[redacted]"

// jsonSecretRe 匹配 "key": "value" 形式的敏感字段（保留键名，只抹值）。
var jsonSecretRe = regexp.MustCompile(
	`(?i)("(?:password|passwd|pwd|token|access_token|refresh_token|id_token|secret|api_key|apikey|authorization)"\s*:\s*")(?:[^"\\]|\\.)*(")`)

// RedactEntry 就地脱敏一条记录。
func RedactEntry(e *Entry) {
	if e == nil {
		return
	}
	for k, v := range e.Headers {
		if sensitiveHeaders[k] || sensitiveHeaders[canonicalHeader(k)] {
			if v != "" {
				e.Headers[k] = redacted
			}
		}
	}
	e.Query = RedactQuery(e.Query)
	e.Body = RedactBody(e.Body)
}

// RedactQuery 抹掉 query 里的敏感参数值（保留参数名，便于看出「传了 token」）。
func RedactQuery(q string) string {
	if q == "" {
		return ""
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return q
	}
	changed := false
	for k := range vals {
		if sensitiveParams[strings.ToLower(k)] {
			vals.Set(k, redacted)
			changed = true
		}
	}
	if !changed {
		return q
	}
	return vals.Encode()
}

// RedactBody 抹掉 JSON 正文里敏感字段的值。
// 非 JSON（表单、纯文本）不做解析——宁可保留原文，也不猜错位置把正文改坏。
func RedactBody(body string) string {
	if body == "" || (!strings.HasPrefix(strings.TrimSpace(body), "{") &&
		!strings.HasPrefix(strings.TrimSpace(body), "[")) {
		return body
	}
	return jsonSecretRe.ReplaceAllString(body, "${1}"+redacted+"${2}")
}

// canonicalHeader 把 Header 名规范成 map 里的形式（cookie → Cookie）。
func canonicalHeader(k string) string {
	switch strings.ToLower(k) {
	case "cookie":
		return "Cookie"
	case "set-cookie":
		return "Set-Cookie"
	case "authorization":
		return "Authorization"
	case "proxy-authorization":
		return "Proxy-Authorization"
	case "x-api-key":
		return "X-Api-Key"
	case "x-auth-token":
		return "X-Auth-Token"
	}
	return k
}
