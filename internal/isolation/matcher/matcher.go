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
}

// New 构建匹配表。同名服务以「启用者优先，其次先出现者」为准（配置层已保证不重复，
// 这里只做防御）。
func New(services []*config.Service) *Table {
	t := &Table{byName: make(map[string]*config.Service, len(services))}
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
	return t
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
