// Package folder 是文件夹接口的 HTTP 层（全部留给你填）。
package folder

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/handler/httpx"
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
	httpx.NotImplemented(c, "folder.List")
}

// Create 新建文件夹。
//
// TODO(练习 78)：实现它。绑定 domain.CreateFolderRequest（失败 → 400）
// → 调服务 → 201 {"folder": folder}。
func (h *Handler) Create(c *gin.Context) {
	httpx.NotImplemented(c, "folder.Create")
}

// Update 重命名文件夹。
//
// TODO(练习 79)：实现它。
//  1. 解析 id（失败 → 400）。
//  2. 绑定 struct{ Name string `json:"name" binding:"required"` }（失败 → 400）。
//  3. 调服务 → 200 {"folder": folder}；失败 → 404。
func (h *Handler) Update(c *gin.Context) {
	httpx.NotImplemented(c, "folder.Update")
}

// Delete 删除文件夹。
//
// TODO(练习 80)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"message": "folder deleted"}。
func (h *Handler) Delete(c *gin.Context) {
	httpx.NotImplemented(c, "folder.Delete")
}

// 保持 import（写完删掉）。
var (
	_ = http.StatusCreated
	_ = strconv.ParseInt
	_ = middleware.GetUserID
)
