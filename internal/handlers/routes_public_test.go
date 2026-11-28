package handlers

import (
	"testing"

	"gatewayhub/internal/models"
)

// TestPublicRoutes 访客视角只保留 active 路由，且 target（后端地址）必须清空，
// 防止停用服务与后端拓扑从 GET /api/routes 泄漏。
func TestPublicRoutes(t *testing.T) {
	in := []models.Route{
		{Prefix: "a", Status: models.StatusActive, Target: "http://backend-a:8000"},
		{Prefix: "b", Status: models.StatusInactive, Target: "http://backend-b:9000"},
		{Prefix: "c", Status: models.StatusActive, Target: "https://backend-c"},
	}
	out := publicRoutes(in)
	if len(out) != 2 {
		t.Fatalf("应只保留 2 条 active，实际 %d", len(out))
	}
	for _, r := range out {
		if r.Status != models.StatusActive {
			t.Errorf("访客结果含非 active: %+v", r)
		}
		if r.Target != "" {
			t.Errorf("访客结果泄漏 target: %s -> %s", r.Prefix, r.Target)
		}
	}
	if out[0].Prefix != "a" || out[1].Prefix != "c" {
		t.Errorf("顺序/内容错: %+v", out)
	}
	// 原切片不应被修改（值拷贝语义）——否则管理员视角也被清空
	if in[0].Target == "" || in[1].Target == "" {
		t.Error("publicRoutes 修改了入参，会污染管理员视角")
	}
}

func TestPublicRoutesEmpty(t *testing.T) {
	if got := publicRoutes(nil); len(got) != 0 {
		t.Errorf("空输入应返回空，实际 %v", got)
	}
}
