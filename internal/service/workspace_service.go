package service

import (
	"context"
	"regexp"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
	"kada-mvp/internal/todo"
)

// slugPattern 是工作区 slug 允许的形状（已给）：小写字母开头，之后是小写字母/数字/连字符，
// 长度 3~50。为什么不允许大写？因为 slug 会出现在 URL 路径里，
// "大小写不敏感的系统（比如某些 DNS、某些日志检索）"会让 My-Team 和 my-team 变成两个东西。
var slugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{2,49}$`)

// WorkspaceService 管工作区。
//
// 对照原项目：kada/internal/service/workspace_service.go
type WorkspaceService struct {
	db *gorm.DB
}

// NewWorkspaceService 构造服务（已给）。
func NewWorkspaceService(db *gorm.DB) *WorkspaceService {
	return &WorkspaceService{db: db}
}

// Create 新建工作区。
//
// TODO(练习 57)：实现它。
//  1. name trim 后不能为空；validateSlug(req.Slug) 必须通过。
//  2. slug 必须全局唯一（它是 URL 标识）。用 slugTaken(ctx, slug, 0) 预检一次，
//     但不能只靠它——两个并发请求可以同时通过预检。
//     真正的保证是数据库上的唯一索引，所以插入时的 isDuplicateKey(err)
//     要翻译成一个可读的错误（例如 "this slug is already taken"）。
//     这是"预检为了体验、约束为了正确"的典型分工。
//  3. 插入 entity.Workspace{Name, Slug, UserID}，返回 domain.Workspace。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *WorkspaceService) Create(ctx context.Context, userID int64, req domain.CreateWorkspaceRequest) (*domain.Workspace, error) {
	return nil, todo.Err("WorkspaceService.Create")
}

// List 列出用户的工作区，每个带链接数量。
//
// TODO(练习 58)：实现它。
//
//	SELECT w.id, w.name, w.slug, w.user_id, w.created_at, w.updated_at,
//	       (SELECT COUNT(*) FROM links AS l WHERE l.workspace_id = w.id) AS link_count
//	  FROM workspaces AS w
//	 WHERE w.user_id = ?
//	 ORDER BY w.created_at DESC
//
// 返回空切片而不是 nil（同 TagService.List 的理由）。//
// ⚠ 用 Find，不要用 Scan：DryRun 会话下 GORM 的 Scan/Rows 会返回 ErrDryRunModeUnsupported，
// 而验收测试都跑在 DryRun 上。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *WorkspaceService) List(ctx context.Context, userID int64) ([]domain.Workspace, error) {
	return nil, todo.Err("WorkspaceService.List")
}

// GetByID 取一个工作区。
//
// TODO(练习 59)：实现它。`Where("id = ? AND user_id = ?", workspaceID, userID).First(&w)`，
// 查不到返回错误。归属条件在 WHERE 里，和 GetByID（链接）一样。
func (s *WorkspaceService) GetByID(ctx context.Context, workspaceID, userID int64) (*domain.Workspace, error) {
	return nil, todo.Err("WorkspaceService.GetByID")
}

// Update 更新工作区名字或 slug。
//
// TODO(练习 60)：实现它。
//  1. 先 GetByID（顺便做归属校验）。
//  2. req.Name / req.Slug 非 nil 才改；slug 改了要 validateSlug + slugTaken(..., 排除自己)。
//  3. 用 Updates(map[string]any{...}) 落库，返回更新后的 domain.Workspace。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *WorkspaceService) Update(ctx context.Context, workspaceID, userID int64, req domain.UpdateWorkspaceRequest) (*domain.Workspace, error) {
	return nil, todo.Err("WorkspaceService.Update")
}

// Delete 删除工作区。
//
// TODO(练习 61)：实现它。
//  1. 先把链接摘出去：UPDATE links SET workspace_id = NULL WHERE workspace_id = ? AND user_id = ?
//  2. 再删工作区（WHERE 带 user_id），RowsAffected == 0 → not found。
//
// 注意 entity 里 workspaces 的外键**没有** ON DELETE 子句（保留原项目的 NO ACTION 行为）：
// 如果还有链接引用它，数据库会直接拒绝删除。所以第 1 步不是"顺手做的好事"，
// 而是这个删除能成功的前提。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *WorkspaceService) Delete(ctx context.Context, workspaceID, userID int64) error {
	return todo.Err("WorkspaceService.Delete")
}

// validateSlug 校验 slug 形状。
//
// TODO(练习 62)：实现它。slugPattern.MatchString(slug) 为假时返回一个可读错误，
// 例如：errors.New("slug must be 3-50 characters, start with a lowercase letter, and contain only lowercase letters, digits and hyphens")。
//
// 验收：go test ./internal/service/ -run TestValidateSlug
func validateSlug(slug string) error {
	return todo.Err("validateSlug")
}

// slugTaken 报告 slug 是否已被占用（exceptID 用来在改名时排除自己）。
//
// TODO(练习 63)：实现它。
//
//	SELECT count(*) FROM workspaces WHERE slug = ? AND id <> ?
//	查询出错时返回 true（宁可拒绝一次合法请求，也不要放进一个冲突的 slug）。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *WorkspaceService) slugTaken(ctx context.Context, slug string, exceptID int64) bool {
	return todo.Bool("WorkspaceService.slugTaken")
}

// 这个空引用是给你的实现准备的（entity.Workspace 会用到）：
// 填空阶段它还没有被使用，用它保持编译通过。写完删掉。
var _ = entity.Workspace{}
