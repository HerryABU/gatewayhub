package cookie

import (
	"net/http"
	"strings"
)

// RequestResult 描述一次请求侧 Cookie 修正的结果（用于指标与日志）。
type RequestResult struct {
	Unhatted int // 脱帽数量
	Dropped  int // 丢弃的外域 Cookie 数量
	Kept     int // 原样转发的数量
}

// RewriteRequest 在转发前修正请求 Cookie（FR-6）：
//   - 仅对「带本服务帽子前缀」的 Cookie 去帽后转发；
//   - 其他服务的 Cookie 按 dropForeign 策略丢弃或原样保留；
//   - 任何情况下都不会误脱别人的帽子（只匹配精确的本服务帽子）。
//
// 该函数直接修改传入的 Header（转发请求的副本），保持 NFR-1 无状态。
func RewriteRequest(h http.Header, hat string, dropForeign bool) RequestResult {
	var res RequestResult
	values := h.Values("Cookie")
	if len(values) == 0 {
		return res
	}
	kept := make([]string, 0, len(values))
	for _, raw := range values {
		pairs := splitCookiePairs(raw)
		if len(pairs) == 0 {
			continue
		}
		out := make([]string, 0, len(pairs))
		for _, pair := range pairs {
			name, _, hasEq := cutPair(pair)
			if !hasEq || name == "" {
				// 畸形片段（无 = 或空名）：不确定归属，原样保留，不做猜测
				out = append(out, pair)
				res.Kept++
				continue
			}
			if newName, ok := Unhat(hat, name); ok {
				out = append(out, newName+pair[len(name):])
				res.Unhatted++
				continue
			}
			if hat != "" && dropForeign {
				res.Dropped++
				continue
			}
			out = append(out, pair)
			res.Kept++
		}
		if len(out) > 0 {
			kept = append(kept, strings.Join(out, "; "))
		}
	}
	h.Del("Cookie")
	for _, v := range kept {
		h.Add("Cookie", v)
	}
	return res
}

// splitCookiePairs 按 ';' 切分 Cookie 头，忽略空片段，保留每段原有内容（仅去掉段首空格）。
func splitCookiePairs(raw string) []string {
	parts := strings.Split(raw, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// cutPair 以第一个 '=' 切出 name/rest，并返回是否包含 '='。
func cutPair(pair string) (name, rest string, hasEq bool) {
	i := strings.Index(pair, "=")
	if i < 0 {
		return strings.TrimSpace(pair), "", false
	}
	return strings.TrimSpace(pair[:i]), pair[i:], true
}
