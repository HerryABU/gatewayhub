package handlers

import (
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"gatewayhub/internal/auth"
	"gatewayhub/internal/models"
)

type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	Remember bool   `json:"remember"`
}

type passwordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

// 登录失败限流：5 次锁定 15 分钟
type loginLimiter struct {
	mu        sync.Mutex
	fails     map[string]int
	lockedAt  map[string]time.Time
}

var limiter = &loginLimiter{
	fails:    make(map[string]int),
	lockedAt: make(map[string]time.Time),
}

func (l *loginLimiter) locked(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.lockedAt[user]; ok && time.Since(t) < 15*time.Minute {
		return true
	}
	if _, ok := l.lockedAt[user]; ok {
		delete(l.lockedAt, user)
		delete(l.fails, user)
	}
	return false
}

func (l *loginLimiter) fail(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails[user]++
	if l.fails[user] >= 5 {
		l.lockedAt[user] = time.Now()
		l.fails[user] = 0
	}
}

func (l *loginLimiter) clear(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, user)
	delete(l.lockedAt, user)
}

// Login POST /api/auth/login
func (h *Handler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if limiter.locked(req.Username) {
		h.fail(c, 1004, "登录失败次数过多，账号已锁定 15 分钟")
		return
	}

	var user models.User
	if err := h.DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		limiter.fail(req.Username)
		h.fail(c, 1005, "用户名或密码错误")
		return
	}
	if !auth.CheckPassword(user.Password, req.Password) {
		limiter.fail(req.Username)
		h.fail(c, 1005, "用户名或密码错误")
		return
	}
	limiter.clear(req.Username)

	ttl := h.Cfg.JWTExpires()
	if req.Remember {
		ttl = h.Cfg.RememberExpires()
	}
	token, expiresAt, err := auth.GenerateToken(h.Cfg.JWT.Secret, user.Username, user.Role, ttl)
	if err != nil {
		h.fail(c, 2001, "生成 Token 失败")
		return
	}
	h.ok(c, gin.H{
		"token":      token,
		"expires_at": expiresAt,
		"username":   user.Username,
		"role":       user.Role,
	})
}

// Refresh POST /api/auth/refresh
func (h *Handler) Refresh(c *gin.Context) {
	tokenStr := extractToken(c)
	if tokenStr == "" {
		h.fail(c, 1005, "未认证")
		return
	}
	claims, err := auth.ParseToken(h.Cfg.JWT.Secret, tokenStr)
	if err != nil {
		h.fail(c, 1005, "Token 无效或已过期")
		return
	}
	token, expiresAt, err := auth.GenerateToken(h.Cfg.JWT.Secret, claims.Username, claims.Role, h.Cfg.JWTExpires())
	if err != nil {
		h.fail(c, 2001, "生成 Token 失败")
		return
	}
	h.ok(c, gin.H{"token": token, "expires_at": expiresAt})
}

// ChangePassword PUT /api/auth/password
func (h *Handler) ChangePassword(c *gin.Context) {
	var req passwordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		h.fail(c, 1001, "参数校验失败")
		return
	}
	if len(req.NewPassword) < 6 {
		h.fail(c, 1001, "新密码长度至少 6 位")
		return
	}
	username := c.GetString("username")
	var user models.User
	if err := h.DB.Where("username = ?", username).First(&user).Error; err != nil {
		h.fail(c, 1002, "用户不存在")
		return
	}
	if !auth.CheckPassword(user.Password, req.OldPassword) {
		h.fail(c, 1004, "原密码错误")
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		h.fail(c, 2001, "密码加密失败")
		return
	}
	if err := h.DB.Model(&user).Update("password", hash).Error; err != nil {
		h.fail(c, 2001, "更新失败")
		return
	}
	h.okMsg(c, "password updated")
}

func extractToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}
