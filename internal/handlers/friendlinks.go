package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// 友链是纯跳转链接，**不经过反向代理**：网关只把它们渲染成 <a href>，
// 既不通往后端服务，也不参与路由匹配。与 routes 表里的转发项是两套东西。
//
// 存储复用 settings 表的 KV（key=friend_links，value=JSON 数组）：
// 不需要为几个链接单独建表，也不会引入新的迁移步骤。
const (
	friendLinkKey  = "friend_links"
	maxFriendLinks = 30
)

// FriendLink 是一条对外展示的友链。
type FriendLink struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// DefaultFriendLinks 是首次访问时的默认友链：GWH 自己的项目主页。
// 用户说「可以是任意网站」，这里选项目 GitHub 仓库——稳定、可点击、不依赖第三方服务存活。
func DefaultFriendLinks() []FriendLink {
	return []FriendLink{{
		ID:          "gatewayhub",
		Name:        "GatewayHub",
		URL:         "https://github.com/HerryABU/gatewayhub",
		Description: "统一接入网关 · 开源项目主页（AGPL-3.0）",
	}}
}

// friendLinks 读取友链。设置缺失或损坏时回落到默认列表，绝不把坏 JSON 抛给前端。
func (h *Handler) friendLinks() []FriendLink {
	links, ok := parseFriendLinks(h.getSetting(friendLinkKey))
	if !ok {
		return DefaultFriendLinks()
	}
	return links
}

// parseFriendLinks 解析存储值；ok=false 表示「未设置/损坏」，调用方应回落到默认值。
// 注意：显式保存 "[]" 是合法的「一条都不想显示」，此时 ok=true、返回空数组。
func parseFriendLinks(raw string) ([]FriendLink, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	var links []FriendLink
	if err := json.Unmarshal([]byte(raw), &links); err != nil {
		return nil, false
	}
	if links == nil {
		return nil, false // JSON null 也是「没配过」
	}
	out := make([]FriendLink, 0, len(links))
	for i, l := range links {
		l.Name = strings.TrimSpace(l.Name)
		l.URL = strings.TrimSpace(l.URL)
		l.Description = strings.TrimSpace(l.Description)
		if !safeLinkURL(l.URL) {
			continue // 历史脏数据（javascript: 等）只在展示侧过滤，不破坏存量配置
		}
		if l.ID == "" {
			l.ID = fmt.Sprintf("link-%d", i)
		}
		out = append(out, l)
	}
	return out, true
}

// safeLinkURL 只接受 http/https 绝对地址，杜绝 javascript:/data: 这类 href 注入。
func safeLinkURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")
}

// validateFriendLink 校验管理端输入；返回空串表示通过。
func validateFriendLink(l FriendLink) string {
	l.Name = strings.TrimSpace(l.Name)
	l.URL = strings.TrimSpace(l.URL)
	if l.Name == "" || len([]rune(l.Name)) > 40 {
		return "名称不能为空且不超过 40 字"
	}
	if !safeLinkURL(l.URL) {
		return "链接必须是完整的 http:// 或 https:// 地址"
	}
	if len([]rune(l.Description)) > 120 {
		return "简介不超过 120 字"
	}
	return ""
}

// saveFriendLinks 回写存储（始终写合法 JSON，空列表写 "[]"）。
func (h *Handler) saveFriendLinks(links []FriendLink) error {
	data, err := json.Marshal(links)
	if err != nil {
		return err
	}
	return h.setSetting(friendLinkKey, string(data))
}

// newLinkID 生成稳定的短 ID（仅用于前端 key 与增删改定位，不用作安全凭据）。
func newLinkID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("link-%d", time.Now().UnixNano())
	}
	return "link-" + hex.EncodeToString(b)
}

// ListFriendLinks GET /api/friend-links（公开，供访客首页展示）
func (h *Handler) ListFriendLinks(c *gin.Context) {
	h.ok(c, h.friendLinks())
}

// CreateFriendLink POST /api/friend-links（管理员）
func (h *Handler) CreateFriendLink(c *gin.Context) {
	var req FriendLink
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	if msg := validateFriendLink(req); msg != "" {
		h.fail(c, 1002, msg)
		return
	}
	links := h.friendLinks()
	if len(links) >= maxFriendLinks {
		h.fail(c, 1003, fmt.Sprintf("最多配置 %d 条友链", maxFriendLinks))
		return
	}
	req.ID = newLinkID()
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	req.Description = strings.TrimSpace(req.Description)
	links = append(links, req)
	if err := h.saveFriendLinks(links); err != nil {
		h.fail(c, 1004, "保存失败")
		return
	}
	h.ok(c, req)
}

// UpdateFriendLink PUT /api/friend-links/:id（管理员）
func (h *Handler) UpdateFriendLink(c *gin.Context) {
	id := c.Param("id")
	var req FriendLink
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	if msg := validateFriendLink(req); msg != "" {
		h.fail(c, 1002, msg)
		return
	}
	links := h.friendLinks()
	found := false
	for i := range links {
		if links[i].ID == id {
			links[i].Name = strings.TrimSpace(req.Name)
			links[i].URL = strings.TrimSpace(req.URL)
			links[i].Description = strings.TrimSpace(req.Description)
			found = true
			break
		}
	}
	if !found {
		h.fail(c, 1005, "友链不存在")
		return
	}
	if err := h.saveFriendLinks(links); err != nil {
		h.fail(c, 1004, "保存失败")
		return
	}
	h.okMsg(c, "friend link updated")
}

// DeleteFriendLink DELETE /api/friend-links/:id（管理员）
func (h *Handler) DeleteFriendLink(c *gin.Context) {
	id := c.Param("id")
	links := h.friendLinks()
	kept := links[:0]
	found := false
	for _, l := range links {
		if l.ID == id {
			found = true
			continue
		}
		kept = append(kept, l)
	}
	if !found {
		h.fail(c, 1005, "友链不存在")
		return
	}
	if err := h.saveFriendLinks(kept); err != nil {
		h.fail(c, 1004, "保存失败")
		return
	}
	h.okMsg(c, "friend link deleted")
}
