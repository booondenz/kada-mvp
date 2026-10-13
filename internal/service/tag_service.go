package service

import (
	"context"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
	"kada-mvp/internal/todo"
)

// TagService 管标签。标签是"多对多"的那一半：链接和标签之间隔着 link_tags。
//
// 对照原项目：kada/internal/service/tag_service.go
type TagService struct {
	db *gorm.DB
}

// NewTagService 构造服务（已给）。
func NewTagService(db *gorm.DB) *TagService {
	return &TagService{db: db}
}

// Create 新建标签。
//
// TODO(练习 54)：实现它。
//  1. name trim 后不能为空。
//  2. 颜色：req.Color 非空就用它，否则用 "#6366F1"（entity.Tag 的 BeforeCreate 也会兜底，
//     但服务层显式写出来，返回值里才有正确的颜色——BeforeCreate 只影响写库，不影响你手里的结构体）。
//  3. 插入 entity.Tag，返回 domain.Tag。
//
// 验收：本函数没有单独的单测。它的归属条件由 TestOrganizationListsAreScopedToTheUser 覆盖；
// 其余行为请按 README 第 3 节的冒烟流程验证。
func (s *TagService) Create(ctx context.Context, userID int64, req domain.CreateTagRequest) (*domain.Tag, error) {
	return nil, todo.Err("TagService.Create")
}

// List 列出用户的标签。
//
// TODO(练习 55)：实现它。
//  1. `Where("user_id = ?", userID).Order("created_at DESC").Find(&tags)`。
//  2. 映射成 []domain.Tag。
//  3. ⚠ 返回**空切片**而不是 nil：JSON 里 null 和 [] 是两种东西，
//     而"没有标签"应该序列化成 []（原项目在多个 DTO 上有这条约定）。
//     做法：`out := make([]domain.Tag, 0, len(tags))`。
//
// 验收：本函数没有单独的单测。它的归属条件由 TestOrganizationListsAreScopedToTheUser 覆盖；
// 其余行为请按 README 第 3 节的冒烟流程验证。
func (s *TagService) List(ctx context.Context, userID int64) ([]domain.Tag, error) {
	return nil, todo.Err("TagService.List")
}

// Delete 删除标签。
//
// TODO(练习 56)：实现它。
//  1. link_tags 里的关联行会随外键 CASCADE 一起删（见 entity.LinkTag）。
//     所以这里只需要删 tags 这一行，而且 WHERE 里必须有 user_id。
//  2. RowsAffected == 0 → not found。
//
// 验收：本函数没有单独的单测。它的归属条件由 TestOrganizationListsAreScopedToTheUser 覆盖；
// 其余行为请按 README 第 3 节的冒烟流程验证。
func (s *TagService) Delete(ctx context.Context, userID, tagID int64) error {
	return todo.Err("TagService.Delete")
}

// ownsTag 报告这个标签是否属于该用户（已给，给上面几个方法当校验样板用）。
func (s *TagService) ownsTag(ctx context.Context, userID, tagID int64) bool {
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.Tag{}).
		Where("id = ? AND user_id = ?", tagID, userID).
		Count(&n).Error
	return err == nil && n > 0
}
