package service

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
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
// 验收：本函数没有单独的单测。List 的"返回空切片而不是 nil"由 TestTagListReturnsEmptySliceNotNil 覆盖；
// 其余行为请按 README 第 3 节的冒烟流程验证。
func (s *TagService) Create(ctx context.Context, userID int64, req domain.CreateTagRequest) (*domain.Tag, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("tag name is required")
	}

	// 默认颜色在服务层显式写出来：BeforeCreate 只影响写库，不影响手里的结构体，
	// 于是"库里有颜色、响应里是空字符串"这种不一致会被顺手制造出来。
	color := "#6366F1"
	if req.Color != nil && strings.TrimSpace(*req.Color) != "" {
		color = strings.TrimSpace(*req.Color)
	}

	tag := entity.Tag{UserID: userID, Name: name, Color: color}
	if err := s.db.WithContext(ctx).Create(&tag).Error; err != nil {
		return nil, err
	}

	return &domain.Tag{
		ID:        tag.ID,
		UserID:    tag.UserID,
		Name:      tag.Name,
		Color:     tag.Color,
		CreatedAt: tag.CreatedAt,
	}, nil
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
// 验收：本函数没有单独的单测。List 的"返回空切片而不是 nil"由 TestTagListReturnsEmptySliceNotNil 覆盖；
// 其余行为请按 README 第 3 节的冒烟流程验证。
func (s *TagService) List(ctx context.Context, userID int64) ([]domain.Tag, error) {
	var tags []entity.Tag
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&tags).Error; err != nil {
		return nil, err
	}

	out := make([]domain.Tag, 0, len(tags))
	for _, t := range tags {
		out = append(out, domain.Tag{
			ID:        t.ID,
			UserID:    t.UserID,
			Name:      t.Name,
			Color:     t.Color,
			CreatedAt: t.CreatedAt,
		})
	}

	return out, nil
}

// Delete 删除标签。
//
// TODO(练习 56)：实现它。
//  1. link_tags 里的关联行会随外键 CASCADE 一起删（见 entity.LinkTag）。
//     所以这里只需要删 tags 这一行，而且 WHERE 里必须有 user_id。
//  2. RowsAffected == 0 → not found。
//
// 验收：本函数没有单独的单测。List 的"返回空切片而不是 nil"由 TestTagListReturnsEmptySliceNotNil 覆盖；
// 其余行为请按 README 第 3 节的冒烟流程验证。
func (s *TagService) Delete(ctx context.Context, userID, tagID int64) error {
	// link_tags 里的关联行由外键 CASCADE 负责，这里只删 tags 这一行。
	res := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", tagID, userID).
		Delete(&entity.Tag{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("tag not found")
	}

	return nil
}

// ownsTag 报告这个标签是否属于该用户（已给，给上面几个方法当校验样板用）。
func (s *TagService) ownsTag(ctx context.Context, userID, tagID int64) bool {
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.Tag{}).
		Where("id = ? AND user_id = ?", tagID, userID).
		Count(&n).Error
	return err == nil && n > 0
}
