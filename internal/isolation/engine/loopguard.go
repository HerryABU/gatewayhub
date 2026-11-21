package engine

import (
	"sync"
	"time"
)

// selfLoopGuard 识别「网关与后端互相把请求推回去」的确定性死循环。
//
// 判据（仅 strip / isolated 模式）：后端 3xx 的 Location 去掉服务前缀后，正好等于
// 本次转发给后端的路径 —— 浏览器下一步必然请求同一个后端路径，网关又把它剥成同样的
// 后端路径，循环闭合、永远不前进。
//
// 现场案例（openlist）：后端配了 site_url 带 /openlist（它对 / 返回 302 → /openlist），
// 网关却按 strip 把 /openlist 剥成 / → 无限 302，浏览器只能报 ERR_TOO_MANY_REDIRECTS，
// 而日志里除了一条模糊的「疑似重定向循环」外没有任何可执行结论。
//
// 为什么不第一次就拦：存在「302 到同一路径 + Set-Cookie，下次请求才放行」的一次性自跳
// 写法。因此同一 (服务|来源|后端路径) 在窗口内第 2 次命中才判定为死循环并返回 508，
// 一次性自跳仍能正常通过。
type selfLoopGuard struct {
	window time.Duration
	max    int

	mu   sync.Mutex
	hits map[string]*loopEntry
}

type loopEntry struct {
	n     int
	first time.Time
}

func newSelfLoopGuard(window time.Duration) *selfLoopGuard {
	return &selfLoopGuard{
		window: window,
		max:    4096, // 上限：超过就整表清理过期项，防止恶意路径把内存撑爆
		hits:   make(map[string]*loopEntry),
	}
}

// Hit 记录一次命中，返回当前窗口内的命中次数（1 表示首次）。
func (g *selfLoopGuard) Hit(key string, now time.Time) int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.hits[key]; ok {
		if now.Sub(e.first) <= g.window {
			e.n++
			return e.n
		}
		// 窗口已过：重新开始计数
		e.n = 1
		e.first = now
		return 1
	}
	if len(g.hits) >= g.max {
		g.sweep(now)
	}
	g.hits[key] = &loopEntry{n: 1, first: now}
	return 1
}

// sweep 清理过期项（调用方已持锁）。
func (g *selfLoopGuard) sweep(now time.Time) {
	for k, e := range g.hits {
		if now.Sub(e.first) > g.window {
			delete(g.hits, k)
		}
	}
}
