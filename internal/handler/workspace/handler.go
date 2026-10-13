// Package workspace 是工作区接口的 HTTP 层（全部留给你填）。
package workspace

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/handler/httpx"
	"kada-mvp/internal/middleware"
)

// WorkspaceService 是 handler 需要的服务能力。
type WorkspaceService interface {
	Create(ctx context.Context, userID int64, req domain.CreateWorkspaceRequest) (*domain.Workspace, error)
	List(ctx context.Context, userID int64) ([]domain.Workspace, error)
	GetByID(ctx context.Context, workspaceID, userID int64) (*domain.Workspace, error)
	Update(ctx context.Context, workspaceID, userID int64, req domain.UpdateWorkspaceRequest) (*domain.Workspace, error)
	Delete(ctx context.Context, workspaceID, userID int64) error
}

// Handler 持有服务。
type Handler struct {
	svc WorkspaceService
}

// NewHandler 构造 handler（已给）。
func NewHandler(svc WorkspaceService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 挂载路由（已给）。
func (h *Handler) RegisterRoutes(r *gin.RouterGroup, authMW gin.HandlerFunc) {
	r.Use(authMW)
	r.GET("/workspaces", h.List)
	r.POST("/workspaces", h.Create)
	r.GET("/workspaces/:id", h.Get)
	r.PATCH("/workspaces/:id", h.Update)
	r.DELETE("/workspaces/:id", h.Delete)
}

// List 列出工作区。
//
// TODO(练习 84)：实现它。调服务 → 200 {"workspaces": workspaces}。
func (h *Handler) List(c *gin.Context) {
	httpx.NotImplemented(c, "workspace.List")
}

// Create 新建工作区。
//
// TODO(练习 85)：实现它。绑定 domain.CreateWorkspaceRequest（失败 → 400）
// → 调服务 → 201 {"workspace": workspace}。
// 提示：slug 已存在时服务层会返回一个可读错误；MVP 统一按 400 回，
// 想更准确的话可以学 auth 包，用一个哨兵错误映射成 409。
func (h *Handler) Create(c *gin.Context) {
	httpx.NotImplemented(c, "workspace.Create")
}

// Get 取一个工作区。
//
// TODO(练习 86)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"workspace": workspace}。
func (h *Handler) Get(c *gin.Context) {
	httpx.NotImplemented(c, "workspace.Get")
}

// Update 更新工作区。
//
// TODO(练习 87)：实现它。解析 id（失败 400）→ 绑定 domain.UpdateWorkspaceRequest
// （失败 400）→ 调服务 → 200 {"workspace": workspace}。
func (h *Handler) Update(c *gin.Context) {
	httpx.NotImplemented(c, "workspace.Update")
}

// Delete 删除工作区。
//
// TODO(练习 88)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"message": "workspace deleted"}。
func (h *Handler) Delete(c *gin.Context) {
	httpx.NotImplemented(c, "workspace.Delete")
}

// 这些 import 是给你的实现准备的；填空阶段还没有被使用，用空引用保持编译通过，写完删掉。
var (
	_ = http.StatusCreated
	_ = strconv.ParseInt
	_ = middleware.GetUserID
)
