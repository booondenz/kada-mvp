// Package workspace 是工作区接口的 HTTP 层（全部留给你填）。
package workspace

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
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
	workspaces, err := h.svc.List(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"workspaces": workspaces})
}

// Create 新建工作区。
//
// TODO(练习 85)：实现它。绑定 domain.CreateWorkspaceRequest（失败 → 400）
// → 调服务 → 201 {"workspace": workspace}。
// 提示：slug 已存在时服务层会返回一个可读错误；MVP 统一按 400 回，
// 想更准确的话可以学 auth 包，用一个哨兵错误映射成 409。
func (h *Handler) Create(c *gin.Context) {
	var req domain.CreateWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace name and slug are required"})
		return
	}

	workspace, err := h.svc.Create(c.Request.Context(), middleware.GetUserID(c), req)
	if err != nil {
		// slug 已被占用也走这里：MVP 统一 400，想更准可以学 auth 包用哨兵错误映射成 409。
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"workspace": workspace})
}

// Get 取一个工作区。
//
// TODO(练习 86)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"workspace": workspace}。
func (h *Handler) Get(c *gin.Context) {
	workspaceID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace ID"})
		return
	}

	workspace, err := h.svc.GetByID(c.Request.Context(), workspaceID, middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "workspace not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"workspace": workspace})
}

// Update 更新工作区。
//
// TODO(练习 87)：实现它。解析 id（失败 400）→ 绑定 domain.UpdateWorkspaceRequest
// （失败 400）→ 调服务 → 200 {"workspace": workspace}。
func (h *Handler) Update(c *gin.Context) {
	workspaceID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace ID"})
		return
	}

	var req domain.UpdateWorkspaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}

	workspace, err := h.svc.Update(c.Request.Context(), workspaceID, middleware.GetUserID(c), req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"workspace": workspace})
}

// Delete 删除工作区。
//
// TODO(练习 88)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"message": "workspace deleted"}。
func (h *Handler) Delete(c *gin.Context) {
	workspaceID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid workspace ID"})
		return
	}

	if err := h.svc.Delete(c.Request.Context(), workspaceID, middleware.GetUserID(c)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "workspace deleted"})
}
