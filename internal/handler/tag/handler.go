// Package tag 是标签接口的 HTTP 层（全部留给你填）。
package tag

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/middleware"
)

// TagService 是 handler 需要的服务能力。
type TagService interface {
	Create(ctx context.Context, userID int64, req domain.CreateTagRequest) (*domain.Tag, error)
	List(ctx context.Context, userID int64) ([]domain.Tag, error)
	Delete(ctx context.Context, userID, tagID int64) error
}

// Handler 持有服务。
type Handler struct {
	svc TagService
}

// NewHandler 构造 handler（已给）。
func NewHandler(svc TagService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 挂载路由（已给）。
func (h *Handler) RegisterRoutes(r *gin.RouterGroup, authMW gin.HandlerFunc) {
	r.Use(authMW)
	r.GET("/tags", h.List)
	r.POST("/tags", h.Create)
	r.DELETE("/tags/:id", h.Delete)
}

// List 列出标签。
//
// TODO(练习 81)：实现它。调服务 → 200 {"tags": tags}。
func (h *Handler) List(c *gin.Context) {
	tags, err := h.svc.List(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"tags": tags})
}

// Create 新建标签。
//
// TODO(练习 82)：实现它。绑定 domain.CreateTagRequest（失败 → 400）
// → 调服务 → 201 {"tag": tag}。
// 提示：绑定的错误文案用 "tag name is required"，
// 因为 binding:"required" 失败时 Gin 给的英文信息对使用者没帮助。
func (h *Handler) Create(c *gin.Context) {
	var req domain.CreateTagRequest
	// Gin 在 binding:"required" 失败时给的英文信息对使用者没帮助，这里换成一句人话。
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tag name is required"})
		return
	}

	tag, err := h.svc.Create(c.Request.Context(), middleware.GetUserID(c), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"tag": tag})
}

// Delete 删除标签。
//
// TODO(练习 83)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"message": "tag deleted"}。
func (h *Handler) Delete(c *gin.Context) {
	tagID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tag ID"})
		return
	}

	if err := h.svc.Delete(c.Request.Context(), middleware.GetUserID(c), tagID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "tag deleted"})
}
