// Package analytics 是分析接口的 HTTP 层（全部留给你填）。
package analytics

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/middleware"
)

// AnalyticsService 是 handler 需要的分析能力。
type AnalyticsService interface {
	Overview(ctx context.Context, userID int64) (domain.Overview, error)
	DailyTrend(ctx context.Context, userID int64, days int) ([]domain.TrendPoint, error)
	PlatformBreakdown(ctx context.Context, userID int64) ([]domain.PlatformCount, error)
}

// Handler 持有服务。
type Handler struct {
	svc AnalyticsService
}

// NewHandler 构造 handler（已给）。
func NewHandler(svc AnalyticsService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 挂载路由（已给）。
//
// 三个只读接口，路径都用复数名词 + 明确的子资源：/analytics/overview、/analytics/trend。
// 没有 /api/v1 —— 这一版只有一个消费者（自己的前端），版本号只会带来一个需要维护的别名。
func (h *Handler) RegisterRoutes(r *gin.RouterGroup, authMW gin.HandlerFunc) {
	r.Use(authMW)
	r.GET("/analytics/overview", h.Overview)
	r.GET("/analytics/trend", h.Trend)
	r.GET("/analytics/platforms", h.Platforms)
}

// Overview 总览。
//
// TODO(练习 74)：实现它。调服务 → 200 + 结果对象本身（不要再包一层 {"overview": ...}：
// 这个接口只有一个返回物，包一层只会让前端多写一次解构）。失败 → 500。
//
// 验收：go test ./internal/handler/analytics/ -run TestOverview
func (h *Handler) Overview(c *gin.Context) {
	overview, err := h.svc.Overview(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 只有一个返回物，直接透传：再包一层只会让前端多写一次解构。
	c.JSON(http.StatusOK, overview)
}

// Trend 每日趋势。
//
// TODO(练习 75)：实现它。
//  1. days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))（坏参数当 0，由服务层夹紧范围）。
//  2. 调服务 → 200 {"points": points}。
//
// 验收：go test ./internal/handler/analytics/ -run TestTrend
func (h *Handler) Trend(c *gin.Context) {
	// 坏参数退化成 0，由服务层夹紧范围——不该让它变成一次 500。
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))

	points, err := h.svc.DailyTrend(c.Request.Context(), middleware.GetUserID(c), days)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"points": points})
}

// Platforms 平台分布。
//
// TODO(练习 76)：实现它。调服务 → 200 {"platforms": platforms}。
func (h *Handler) Platforms(c *gin.Context) {
	platforms, err := h.svc.PlatformBreakdown(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"platforms": platforms})
}
