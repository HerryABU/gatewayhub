package engine

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"sync"
	"sync/atomic"
	"time"

	"gatewayhub/internal/isolation/breaker"
	"gatewayhub/internal/isolation/config"
	"gatewayhub/internal/isolation/diag"
	"gatewayhub/internal/isolation/metrics"
)

// bufferPool 复用小缓冲区，降低高并发下的分配与 GC 压力。
var bufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, 32*1024)
		return &b
	},
}

type poolBuffer struct{}

func (poolBuffer) Get() []byte  { return *bufferPool.Get().(*[]byte) }
func (poolBuffer) Put(b []byte) { bufferPool.Put(&b) }

// transportPool 按「后端 + 超时」复用 http.Transport：热更新重建 ReverseProxy 时
// 不会把既有连接池丢掉（否则每次改配置都要重新握手，等于自己打自己）。
type transportPool struct {
	mu sync.Mutex
	m  map[string]*http.Transport
}

func newTransportPool() *transportPool {
	return &transportPool{m: make(map[string]*http.Transport)}
}

func (p *transportPool) get(scheme, host string, timeout time.Duration) *http.Transport {
	key := scheme + "|" + host + "|" + timeout.String()
	p.mu.Lock()
	defer p.mu.Unlock()
	if t, ok := p.m[key]; ok {
		return t
	}
	t := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          512,
		MaxIdleConnsPerHost:   128,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		ForceAttemptHTTP2:     true,
		// 压缩交给后端按 Accept-Encoding 决定，不在 Transport 层自动解压，
		// 避免「透明解压后 Content-Encoding 与 ContentLength 不一致」的二次改写错误
		DisableCompression: true,
	}
	p.m[key] = t
	return t
}

// requestState 是随请求上下文传递的「本请求处理快照」（无全局状态，NFR-1）。
// 由 ServeHTTP 写入，Director / ModifyResponse / ErrorHandler 只读。
type requestState struct {
	cfg      *config.Service
	rest     string
	prefix   string
	clientIP string
	origin   string // 原始 Host（X-Forwarded-Host）
	proto    string // 原始协议
	counters *metrics.Counters
}

type stateKey struct{}

// runtime 是单服务的运行时对象：配置可原子替换（模式热切换），代理实例常驻。
type runtime struct {
	name      string
	cfg       atomic.Pointer[config.Service]
	proxy     *httputil.ReverseProxy
	counters  *metrics.Counters
	breaker   *breaker.Breaker
	loopGuard *selfLoopGuard
}

// service 返回当前生效的配置快照。
func (rt *runtime) service() *config.Service { return rt.cfg.Load() }

// setMode 热切换处理模式（FR-14 一键降级），返回旧模式。
func (rt *runtime) setMode(mode config.Mode) config.Mode {
	for {
		old := rt.cfg.Load()
		if old.Mode == mode {
			return old.Mode
		}
		next := *old // 值拷贝：其余字段（含 target/timeout）保持不变
		next.Mode = mode
		if rt.cfg.CompareAndSwap(old, &next) {
			return old.Mode
		}
	}
}

// newRuntime 构建单服务运行时（含 ReverseProxy、指标、熔断、自环守卫）。
func newRuntime(svc *config.Service, pool *transportPool, reg *metrics.Registry,
	brCfg config.BreakerOptions, guard *selfLoopGuard) *runtime {
	rt := &runtime{name: svc.Name, loopGuard: guard}
	rt.cfg.Store(svc)
	rt.counters = reg.For(svc.Name)
	rt.breaker = breaker.New(brCfg.Failures, time.Duration(brCfg.Cooldown)*time.Second)

	transport := pool.get(svc.Scheme, svc.Host, svc.TimeoutDuration())
	rt.proxy = &httputil.ReverseProxy{
		Rewrite:        rt.rewrite,
		ModifyResponse: rt.modifyResponse,
		ErrorHandler:   rt.errorHandler,
		Transport:      transport,
		FlushInterval:  -1, // 立即 flush：SSE / 流式响应不被缓冲
		BufferPool:     poolBuffer{},
	}
	return rt
}

// errorHandler 统一的后端错误出口：记录指标与熔断，返回不泄露内网信息的 JSON。
func (rt *runtime) errorHandler(w http.ResponseWriter, req *http.Request, err error) {
	code := http.StatusBadGateway
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		code = http.StatusGatewayTimeout
	}
	if st, ok := req.Context().Value(stateKey{}).(*requestState); ok && st.counters != nil {
		st.counters.BackendErrors.Add(1)
	}
	rt.tripOnFailure()
	diag.Logf(diag.BackendBug, rt.name, "后端转发失败（%s %s）：%v", req.Method, req.URL.Path, err)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"code":%d,"message":%q}`, code, http.StatusText(code))
}

// tripOnFailure 记一次失败并在刚刚熔断时打日志。
func (rt *runtime) tripOnFailure() {
	if rt.breaker.Failure() {
		rt.counters.BreakerTrips.Add(1)
		diag.Logf(diag.ProxyDegrade, rt.name, "连续失败达到阈值，服务进入熔断（冷却后可探测）")
	}
}
