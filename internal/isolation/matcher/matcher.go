package matcher

import (
	"sort"
	"strings"

	"gatewayhub/internal/isolation/config"
)

// Table 是「服务名 → 服务」的只读匹配表。
//
// 匹配规则（FR-1/FR-3）：取请求路径第一段，与配置表中的服务名做大小写不敏感精确匹配；
// 不做前缀猜测、不做模糊匹配 —— 未命中就是不命中，由调用方返回 404。
type Table struct {
	byName map[string]*config.Service // key 为小写服务名
	names  []string                   // 排序后的服务名（输出稳定）

	// absPrefix 是「后端已知绝对路径前缀 → 服务」的索引（来自 known_abs_prefixes）。
	// 它不参与路由（绝不把 /assets/x 猜给某服务），只在未命中时用于「越界归因」：
	// 告诉运维这条请求最可能来自哪个服务的后端前端，把责任边界写清楚（FR-18）。
	absPrefix map[string]*config.Service
	absNames  []string // 按前缀长度降序，保证最长前缀优先
}

// New 构建匹配表。同名服务以「启用者优先，其次先出现者」为准（配置层已保证不重复，
// 这里只做防御）。
func New(services []*config.Service) *Table {
	t := &Table{
		byName:    make(map[string]*config.Service, len(services)),
		absPrefix: make(map[string]*config.Service),
	}
	for _, s := range services {
		if s == nil {
			continue
		}
		key := strings.ToLower(s.Name)
		if prev, ok := t.byName[key]; ok {
			if prev.Enabled || !s.Enabled {
				continue
			}
		}
		t.byName[key] = s
	}
	t.names = make([]string, 0, len(t.byName))
	for k := range t.byName {
		t.names = append(t.names, k)
	}
	sort.Strings(t.names)

	// 已知绝对路径前缀索引：配置层已做规范化（单个 / 开头、去尾斜杠），这里再去重。
	for _, s := range t.byName {
		for _, p := range s.KnownAbsPrefix {
			if p == "" || p == "/" {
				continue // "/" 等于「全部路径」，放进来会把一切都归因掉，无意义
			}
			if _, dup := t.absPrefix[p]; dup {
				continue // 同一前缀多处声明时以先出现者为准（配置层已告警）
			}
			t.absPrefix[p] = s
			t.absNames = append(t.absNames, p)
		}
	}
	sort.Slice(t.absNames, func(i, j int) bool { return len(t.absNames[i]) > len(t.absNames[j]) })
	return t
}

// Owner 在「未命中任何服务」的路径上做越界归因：返回该路径最可能属于哪个服务，
// 以及命中的已知绝对路径前缀。
//
// 只做归因与日志，绝不据此路由（FR-3 不猜测归属）：典型场景是后端 SPA 没配 base path，
// 浏览器去请求 /assets/app.js 这种根路径，网关应当明确告诉运维「这是 X 的后端造成的」。
func (t *Table) Owner(path string) (*config.Service, string, bool) {
	if t == nil || len(t.absPrefix) == 0 || !strings.HasPrefix(path, "/") {
		return nil, "", false
	}
	for _, p := range t.absNames {
		// 必须落在段边界上：/assets 命中 /assets 与 /assets/x，但不命中 /assetsx
		if path == p || strings.HasPrefix(path, p+"/") {
			return t.absPrefix[p], p, true
		}
	}
	return nil, "", false
}

// Lookup 按路径第一段匹配服务，返回服务与「未剥离的剩余路径」。
//
//	/A        → ("A", "")
//	/A/       → ("A", "/")
//	/A/admin  → ("A", "/admin")
//
// rest 原样返回（不做路径清理），由转发层按模式决定剥离与规范化。
func (t *Table) Lookup(path string) (*config.Service, string, bool) {
	if t == nil || !strings.HasPrefix(path, "/") {
		return nil, "", false
	}
	trimmed := path[1:]
	if trimmed == "" {
		return nil, "", false
	}
	seg := trimmed
	rest := ""
	if i := strings.Index(trimmed, "/"); i >= 0 {
		seg, rest = trimmed[:i], trimmed[i:]
	}
	if seg == "" {
		return nil, "", false
	}
	svc, ok := t.byName[strings.ToLower(seg)]
	if !ok {
		return nil, "", false
	}
	return svc, rest, true
}

// Get 按服务名（大小写不敏感）精确查找。
func (t *Table) Get(name string) (*config.Service, bool) {
	if t == nil {
		return nil, false
	}
	s, ok := t.byName[strings.ToLower(name)]
	return s, ok
}

// Has 判断首段是否为已注册服务（供部署前缀剥离逻辑使用：
// 路径首段命中隔离服务时，绝不能被当成部署前缀剥掉）。
func (t *Table) Has(first string) bool {
	_, ok := t.Get(first)
	return ok
}

// Names 返回排序后的服务名（小写），用于稳定输出。
func (t *Table) Names() []string {
	if t == nil {
		return nil
	}
	out := make([]string, len(t.names))
	copy(out, t.names)
	return out
}

// Len 返回服务条数。
func (t *Table) Len() int {
	if t == nil {
		return 0
	}
	return len(t.byName)
}
