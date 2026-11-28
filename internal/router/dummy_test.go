package router

import "gatewayhub/internal/proxy"

// newDummyManager 返回空代理管理器（测试 Referer 解析边界用）。
func newDummyManager() *proxy.Manager { return proxy.NewManager(nil, nil, "", proxy.SelfInfo{}) }
