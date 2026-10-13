// Package tag 是标签接口的 HTTP 层（全部留给你填）。
package tag

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/handler/httpx"
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
	httpx.NotImplemented(c, "tag.List")
}

// Create 新建标签。
//
// TODO(练习 82)：实现它。绑定 domain.CreateTagRequest（失败 → 400）
// → 调服务 → 201 {"tag": tag}。
// 提示：绑定的错误文案用 "tag name is required"，
// 因为 binding:"required" 失败时 Gin 给的英文信息对使用者没帮助。
func (h *Handler) Create(c *gin.Context) {
	httpx.NotImplemented(c, "tag.Create")
}

// Delete 删除标签。
//
// TODO(练习 83)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"message": "tag deleted"}。
func (h *Handler) Delete(c *gin.Context) {
	httpx.NotImplemented(c, "tag.Delete")
}

// 这些 import 是给你的实现准备的；填空阶段还没有被使用，用空引用保持编译通过，写完删掉。
var (
	_ = http.StatusCreated
	_ = strconv.ParseInt
	_ = middleware.GetUserID
)
