package handlers

import (
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// BackupCreate POST /api/backup —— 手动备份
func (h *Handler) BackupCreate(c *gin.Context) {
	rec, err := h.Backup.Create("manual")
	if err != nil {
		h.fail(c, 2001, "备份失败："+err.Error())
		return
	}
	h.ok(c, rec)
}

// BackupList GET /api/backup/list
func (h *Handler) BackupList(c *gin.Context) {
	recs, err := h.Backup.List()
	if err != nil {
		h.fail(c, 2001, "查询失败")
		return
	}
	h.ok(c, recs)
}

// BackupDownload GET /api/backup/download?file=xxx
func (h *Handler) BackupDownload(c *gin.Context) {
	name := c.Query("file")
	if name == "" {
		h.fail(c, 1001, "缺少文件名")
		return
	}
	// 防止路径穿越
	name = filepath.Base(name)
	full := filepath.Join(h.Cfg.Backup.Dir, name)
	if _, err := os.Stat(full); err != nil {
		h.fail(c, 1002, "文件不存在")
		return
	}
	c.FileAttachment(full, name)
}

// BackupDelete DELETE /api/backup/:id
func (h *Handler) BackupDelete(c *gin.Context) {
	id := c.Param("id")
	var rec struct {
		ID       uint
		Filename string
	}
	if err := h.DB.Table("backup_records").Where("id = ?", id).First(&rec).Error; err != nil {
		h.fail(c, 1002, "记录不存在")
		return
	}
	_ = os.Remove(rec.Filename)
	_ = h.DB.Table("backup_records").Where("id = ?", id).Delete(nil).Error
	h.okMsg(c, "deleted")
}
