// Package hopguard 限制「网关把自己当成后端」时的链式转发跳数。
//
// 自指（target 指向网关自身监听地址）与嵌套（一层自指再转给别的路由）都是合法用法：
// 典型场景是把控制台、静态站点或另一个前缀的站点「挂」到新前缀下，或在迁移期做
// 旧地址 → 新地址的别名。但这类配置一旦配成「绕回自己」，就是一个不设上限的请求
// 生成器 —— 每跳都新起一个 goroutine 与一条连接，几百毫秒内就能把进程拖垮，
// 而且日志里只看到一串一模一样的请求，看不出是谁在绕。
//
// 因此这里给每次转发打一个跳数头（X-GatewayHub-Hops），规则只有三条：
//
//  1. 入站读到的值 +1 后写回，链路上每一跳都递增；
//  2. 超过上限不转发，直接 508 Loop Detected（HTTP 状态码本来就是为这件事准备的）；
//  3. 入站值超过硬上限时一律当成 0 —— 客户端可以伪造这个头，但伪造只能让**自己**
//     这一条请求被拒，影响不到别人，所以不需要密钥；硬上限只是防止一个荒谬的大数
//     让计数器溢出或日志出现天文数字。
package hopguard

import (
	"net/http"
	"strconv"
	"strings"
)

const (
	// Header 是网关标注在转发请求上的跳数头。
	Header = "X-GatewayHub-Hops"
	// DefaultMax 是默认允许的最大跳数（含自指与多层嵌套）。
	DefaultMax = 5
	// ceiling 是解析入站值时的硬上限：超过它就当成 0。
	ceiling = 64
)

// Guard 判断一条请求还能不能继续往下转。
type Guard struct{ max int }

// New 创建守卫；max <= 0 时用 DefaultMax。
func New(max int) *Guard {
	if max <= 0 {
		max = DefaultMax
	}
	return &Guard{max: max}
}

// Max 返回最大允许跳数。
func (g *Guard) Max() int {
	if g == nil || g.max <= 0 {
		return DefaultMax
	}
	return g.max
}

// Next 读取入站跳数并返回递增后的值；ok=false 表示已超上限，调用方应返回 508。
//
// 空值、非数字、负数、超过 ceiling 的值都当作 0：这条请求是链路的起点。
func (g *Guard) Next(h http.Header) (int, bool) {
	n := inbound(h)
	next := n + 1
	return next, next <= g.Max()
}

// inbound 解析入站跳数（非法值一律归零）。
func inbound(h http.Header) int {
	if h == nil {
		return 0
	}
	raw := strings.TrimSpace(h.Get(Header))
	if raw == "" {
		return 0
	}
	// 只取第一个逗号前的值：多层代理可能把它当普通头拼接。
	if i := strings.IndexByte(raw, ','); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > ceiling {
		return 0
	}
	return n
}

// Stamp 把跳数写进请求头（供转发使用）。
func Stamp(h http.Header, n int) {
	if h == nil {
		return
	}
	h.Set(Header, strconv.Itoa(n))
}
