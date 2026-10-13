// Package folder 是文件夹接口的 HTTP 层（全部留给你填）。
package folder

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/middleware"
)

// FolderService 是 handler 需要的服务能力。
type FolderService interface {
	Create(ctx context.Context, userID int64, req domain.CreateFolderRequest) (*domain.Folder, error)
	List(ctx context.Context, userID int64) ([]domain.Folder, error)
	Update(ctx context.Context, userID, folderID int64, name string) (*domain.Folder, error)
	Delete(ctx context.Context, userID, folderID int64) error
}

// Handler 持有服务。
type Handler struct {
	svc FolderService
}

// NewHandler 构造 handler（已给）。
func NewHandler(svc FolderService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 挂载路由（已给）。
func (h *Handler) RegisterRoutes(r *gin.RouterGroup, authMW gin.HandlerFunc) {
	r.Use(authMW)
	r.GET("/folders", h.List)
	r.POST("/folders", h.Create)
	r.PATCH("/folders/:id", h.Update)
	r.DELETE("/folders/:id", h.Delete)
}

// List 列出文件夹。
//
// TODO(练习 77)：实现它。调服务 → 200 {"folders": folders}。
// ⚠ 服务返回空切片而不是 nil，这里直接透传；如果你在服务层用了 append 到 nil 切片，
// 记住那会序列化成 null，前端要把两种情况都处理一遍。
func (h *Handler) List(c *gin.Context) {
	folders, err := h.svc.List(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"folders": folders})
}

// Create 新建文件夹。
//
// TODO(练习 78)：实现它。绑定 domain.CreateFolderRequest（失败 → 400）
// → 调服务 → 201 {"folder": folder}。
func (h *Handler) Create(c *gin.Context) {
	var req domain.CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "folder name is required"})
		return
	}

	folder, err := h.svc.Create(c.Request.Context(), middleware.GetUserID(c), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"folder": folder})
}

// Update 重命名文件夹。
//
// TODO(练习 79)：实现它。
//  1. 解析 id（失败 → 400）。
//  2. 绑定 struct{ Name string `json:"name" binding:"required"` }（失败 → 400）。
//  3. 调服务 → 200 {"folder": folder}；失败 → 404。
func (h *Handler) Update(c *gin.Context) {
	folderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder ID"})
		return
	}

	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}

	folder, err := h.svc.Update(c.Request.Context(), middleware.GetUserID(c), folderID, req.Name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"folder": folder})
}

// Delete 删除文件夹。
//
// TODO(练习 80)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"message": "folder deleted"}。
func (h *Handler) Delete(c *gin.Context) {
	folderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder ID"})
		return
	}

	if err := h.svc.Delete(c.Request.Context(), middleware.GetUserID(c), folderID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "folder deleted"})
}
