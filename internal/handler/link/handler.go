// Package link 是短链 CRUD 的 HTTP 层。
//
// 这个包里的所有方法都是**留给你填的**，但形状和 auth 包一模一样：
// 绑定 JSON → 取 user_id → 调服务 → 把服务错误映射成状态码 → 返回 JSON。
// 填之前先把 internal/handler/auth/handler.go 读一遍，照着它的三段式写。
package link

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/middleware"
)

// LinkService 是 handler 需要的服务能力。
//
// 为什么把方法的参数原样抄一遍而不是直接依赖 *service.LinkService？
// 因为这样 handler 的测试可以塞一个"只记住被调用参数"的假实现，
// 不需要数据库、不需要 Redis、不需要 Kafka。
type LinkService interface {
	Create(ctx context.Context, userID int64, req domain.CreateLinkRequest) (*domain.LinkInfo, error)
	GetByID(ctx context.Context, linkID, userID int64) (*domain.LinkInfo, error)
	List(ctx context.Context, userID int64, page, pageSize int, search string, folderID, tagID, workspaceID int64, sort string) (*domain.PaginatedLinks, error)
	Update(ctx context.Context, linkID, userID int64, req domain.UpdateLinkRequest) (*domain.LinkInfo, error)
	Delete(ctx context.Context, linkID, userID int64) error
	BatchDelete(ctx context.Context, ids []int64, userID int64) (int64, error)
	BatchTag(ctx context.Context, ids []int64, tagID, userID int64) error
}

// Handler 持有服务。
type Handler struct {
	svc LinkService
}

// NewHandler 构造 handler（已给）。
func NewHandler(svc LinkService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 挂载路由（已给）。
//
// 注意顺序：`/links/preview`、`/links/batch-delete` 这类**固定路径**必须写在
// `/links/:id` 之前（如果都在一个组里），否则 "batch-delete" 会被当成一个 id。
// 这里 MVP 不需要 preview，但 batch-* 和 :id 的共存本身就是一个值得注意的坑。
func (h *Handler) RegisterRoutes(r *gin.RouterGroup, authMW gin.HandlerFunc) {
	r.Use(authMW)
	r.POST("/links", h.Create)
	r.GET("/links", h.List)
	r.POST("/links/batch-delete", h.BatchDelete)
	r.POST("/links/batch-tag", h.BatchTag)
	r.GET("/links/:id", h.Get)
	r.PATCH("/links/:id", h.Update)
	r.DELETE("/links/:id", h.Delete)
}

// Create 创建短链。
//
// TODO(练习 64)：实现它。
//  1. c.ShouldBindJSON(&req) 失败 → 400，文案 "valid link details are required: " + err.Error()。
//     （把校验器的原始信息带上：这里唯一能看懂"到底哪一栏错了"的人是调用方。）
//  2. h.svc.Create(c.Request.Context(), middleware.GetUserID(c), req)。
//     失败 → 500 + err.Error()（注意：Create 的错误里既有"你的短码被占了"这种调用方问题，
//     也有数据库问题。MVP 统一 500，想更细就按错误类型分流——那正是 auth 包做的事）。
//  3. 成功 → 201 {"link": link}。
//     为什么是 201 而不是 200？因为这次请求创建了一个新资源，这是 201 的定义。
//
// 验收：go test ./internal/handler/link/ -run TestCreate
func (h *Handler) Create(c *gin.Context) {
	var req domain.CreateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "valid link details are required: " + err.Error()})
		return
	}

	link, err := h.svc.Create(c.Request.Context(), middleware.GetUserID(c), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"link": link})
}

// List 分页列出链接。
//
// TODO(练习 65)：实现它。
//  1. 从 query 取参数：page（默认 1）、page_size（默认 20）、search、folder_id、
//     tag_id、workspace_id、sort（默认 "created_desc"）。
//     提示：strconv.Atoi 的错误**可以忽略**（坏参数当成 0，也就是"不筛选"），
//     但你要知道这是个取舍：更严格的做法是回 400。
//  2. 调服务，返回 200：
//     {"links": ..., "total_count": ..., "page": ..., "page_size": ...}
//  3. 失败 → 500 + err.Error()。
//
// 验收：go test ./internal/handler/link/ -run TestList
func (h *Handler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	folderID, _ := strconv.ParseInt(c.DefaultQuery("folder_id", "0"), 10, 64)
	tagID, _ := strconv.ParseInt(c.DefaultQuery("tag_id", "0"), 10, 64)
	workspaceID, _ := strconv.ParseInt(c.DefaultQuery("workspace_id", "0"), 10, 64)

	result, err := h.svc.List(
		c.Request.Context(),
		middleware.GetUserID(c),
		page,
		pageSize,
		c.Query("search"),
		folderID,
		tagID,
		workspaceID,
		c.DefaultQuery("sort", "created_desc"),
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"links":       result.Links,
		"total_count": result.TotalCount,
		"page":        result.Page,
		"page_size":   result.PageSize,
	})
}

// Get 取单条链接。
//
// TODO(练习 66)：实现它。
//  1. strconv.ParseInt(c.Param("id"), 10, 64) 失败 → 400 {"error": "invalid link ID"}。
//  2. 调服务；**任何错误都回 404**（"链接不存在"）。
//     为什么不区分 403？因为对调用方来说"这条链接不是你的"和"没有这条链接"
//     应该是同一个答案——区分它们等于提供一个"探测别人有哪些 id"的接口。
//  3. 成功 → 200 {"link": link}。
func (h *Handler) Get(c *gin.Context) {
	linkID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid link ID"})
		return
	}

	link, err := h.svc.GetByID(c.Request.Context(), linkID, middleware.GetUserID(c))
	if err != nil {
		// 「不是你的」和「不存在」共用 404：区分它们等于提供一个探测别人 id 的接口。
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"link": link})
}

// Update 更新链接。
//
// TODO(练习 67)：实现它。
//  1. 解析 id（失败 → 400）。
//  2. 绑定 domain.UpdateLinkRequest（失败 → 400 "invalid request parameters"）。
//  3. 调服务；失败 → 404 + err.Error()；成功 → 200 {"link": link}。
func (h *Handler) Update(c *gin.Context) {
	linkID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid link ID"})
		return
	}

	var req domain.UpdateLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}

	link, err := h.svc.Update(c.Request.Context(), linkID, middleware.GetUserID(c), req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"link": link})
}

// Delete 删除链接。
//
// TODO(练习 68)：实现它。解析 id（失败 400）→ 调服务（失败 404）→ 200 {"message": "link deleted"}。
func (h *Handler) Delete(c *gin.Context) {
	linkID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid link ID"})
		return
	}

	if err := h.svc.Delete(c.Request.Context(), linkID, middleware.GetUserID(c)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "link deleted"})
}

// BatchDelete 批量删除。
//
// TODO(练习 69)：实现它。
//  1. 绑定一个匿名结构体：
//     var req struct{ IDs []int64 `json:"ids" binding:"required"` }
//     失败 → 400 {"error": "link ID list is required"}。
//  2. 调服务，返回 200 {"deleted": count}（count 是**实际删掉的行数**，
//     而不是请求里的 id 个数——两者在"其中一些不是你的"时不一样）。
func (h *Handler) BatchDelete(c *gin.Context) {
	var req struct {
		IDs []int64 `json:"ids" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "link ID list is required"})
		return
	}

	deleted, err := h.svc.BatchDelete(c.Request.Context(), req.IDs, middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}

// BatchTag 批量打标签。
//
// TODO(练习 70)：实现它。
//  1. 绑定 struct{ IDs []int64; TagID int64 }（都 required）→ 失败 400。
//  2. 调服务 → 成功 200 {"message": "tags applied successfully"}。
func (h *Handler) BatchTag(c *gin.Context) {
	var req struct {
		IDs   []int64 `json:"ids" binding:"required"`
		TagID int64   `json:"tag_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request parameters"})
		return
	}

	if err := h.svc.BatchTag(c.Request.Context(), req.IDs, req.TagID, middleware.GetUserID(c)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "tags applied successfully"})
}
