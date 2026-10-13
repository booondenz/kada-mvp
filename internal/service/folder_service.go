package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
)

// FolderService 管文件夹。它和下面两个服务（Tag、Workspace）一起构成本项目里
// "标准 CRUD + 归属校验"的模板：每个方法都短，但每一条 WHERE 里都必须有 user_id。
//
// 对照原项目：kada/internal/service/folder_service.go
type FolderService struct {
	db *gorm.DB
}

// NewFolderService 构造服务（已给）。
func NewFolderService(db *gorm.DB) *FolderService {
	return &FolderService{db: db}
}

// Create 新建文件夹。
//
// TODO(练习 50)：实现它。
//  1. strings.TrimSpace(req.Name) 为空 → errors.New("folder name is required")。
//  2. 插入 entity.Folder{UserID: userID, Name: name}。
//  3. 返回 domain.Folder{ID, UserID, Name, CreatedAt, UpdatedAt}。
//
// 注意：不要去查"这个用户是不是已经有同名文件夹"。同名文件夹是允许的
// （真正不能重复的是短码这种被引用的标识），而多一次查询就多一个竞态。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *FolderService) Create(ctx context.Context, userID int64, req domain.CreateFolderRequest) (*domain.Folder, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, errors.New("folder name is required")
	}

	folder := entity.Folder{UserID: userID, Name: name}
	if err := s.db.WithContext(ctx).Create(&folder).Error; err != nil {
		return nil, err
	}

	return &domain.Folder{
		ID:        folder.ID,
		UserID:    folder.UserID,
		Name:      folder.Name,
		CreatedAt: folder.CreatedAt,
		UpdatedAt: folder.UpdatedAt,
	}, nil
}

// List 列出用户的文件夹，每个带上链接数量。
//
// TODO(练习 51)：实现它。
//
//	SELECT f.id, f.user_id, f.name, f.created_at, f.updated_at,
//	       (SELECT COUNT(*) FROM links AS l WHERE l.folder_id = f.id) AS link_count
//	  FROM folders AS f
//	 WHERE f.user_id = ?
//	 ORDER BY f.created_at DESC
//
// 用子查询而不是 LEFT JOIN + GROUP BY：一条链接只能属于一个文件夹，
// 但子查询让"计数"这件事和"列表"这件事在 SQL 里保持独立，读起来不用在脑子里做聚合。
//
// 归属条件仍然是第一个 WHERE。
//
// ⚠ 用 Find（或 Rows/Pluck 之外的行扫描方式），**不要用 Scan**：
// DryRun 会话下 GORM 的 Scan/Rows 会返回 ErrDryRunModeUnsupported，而验收测试都跑在 DryRun 上。
//
// 验收：本函数没有单独的单测。它的归属条件由 TestOrganizationListsAreScopedToTheUser 覆盖；
// 其余行为请按 README 第 3 节的冒烟流程验证。
func (s *FolderService) List(ctx context.Context, userID int64) ([]domain.Folder, error) {
	var rows []folderListRow

	// 用 Find 而不是 Scan：DryRun 会话下 Scan 会以 ErrDryRunModeUnsupported 结束，
	// 而 Find 走 Query 回调（真实数据库上两者的 SQL 相同）。
	if err := s.db.WithContext(ctx).
		Table("folders AS f").
		Select(`f.id, f.user_id, f.name, f.created_at, f.updated_at,
		        (SELECT COUNT(*) FROM links AS l WHERE l.folder_id = f.id) AS link_count`).
		Where("f.user_id = ?", userID).
		Order("f.created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, err
	}

	// 空切片而不是 nil：没有文件夹时 JSON 里应该是 []，不是 null。
	out := make([]domain.Folder, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.Folder{
			ID:        r.ID,
			UserID:    r.UserID,
			Name:      r.Name,
			CreatedAt: r.CreatedAt,
			UpdatedAt: r.UpdatedAt,
			LinkCount: r.LinkCount,
		})
	}

	return out, nil
}

// folderListRow 是文件夹列表查询的一行：entity.Folder 的字段 + 链接数量。
type folderListRow struct {
	ID        int64
	UserID    int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
	LinkCount int64
}

// Update 重命名文件夹。
//
// TODO(练习 52)：实现它。
//  1. 名称 trim 后不能为空。
//  2. `Model(&entity.Folder{}).Where("id = ? AND user_id = ?", folderID, userID).
//     Updates(map[string]any{"name": name})`，检查 RowsAffected：
//     0 行说明"没找到"或"不是你的"，两种情况都返回 domain.ErrLinkNotFound
//     之外的通用 not found 错误（这里可以定义 ErrFolderNotFound，
//     也可以复用 errors.New("folder not found")——MVP 选择后者，少一个导出符号）。
//
// 注意这里用 Updates(map) 而不是 Updates(struct)：map 能表达"把字段改成零值"，
// struct 不能（GORM 会跳过零值字段）。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *FolderService) Update(ctx context.Context, userID, folderID int64, name string) (*domain.Folder, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("folder name is required")
	}

	res := s.db.WithContext(ctx).
		Model(&entity.Folder{}).
		Where("id = ? AND user_id = ?", folderID, userID).
		Updates(map[string]any{"name": name})
	if res.Error != nil {
		return nil, res.Error
	}
	// 0 行 = "没这个文件夹" 或 "不是你的"：两种情况对外是同一个答案。
	if res.RowsAffected == 0 {
		return nil, errors.New("folder not found")
	}

	// 返回值要是"库里的样子"，所以再读一次（updated_at 是这一条 UPDATE 改的）。
	var folder entity.Folder
	if err := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", folderID, userID).
		First(&folder).Error; err != nil {
		if isNotFound(err) {
			return nil, errors.New("folder not found")
		}
		return nil, err
	}

	return &domain.Folder{
		ID:        folder.ID,
		UserID:    folder.UserID,
		Name:      folder.Name,
		CreatedAt: folder.CreatedAt,
		UpdatedAt: folder.UpdatedAt,
	}, nil
}

// Delete 删除文件夹。
//
// TODO(练习 53)：实现它。**先摘链接，再删文件夹**，两步都要带 user_id：
//
//  1. UPDATE links SET folder_id = NULL WHERE folder_id = ? AND user_id = ?
//     为什么要先摘？因为数据库的外键是 ON DELETE SET NULL，它会替我们做这件事，
//     但显式写出来有两层好处：一是语义清楚（我们不是"删掉链接"，是"让它们不再属于任何文件夹"），
//     二是它顺便做了一次归属校验——如果这个文件夹不是你的，第一步就影响 0 行。
//  2. DELETE FROM folders WHERE id = ? AND user_id = ?，RowsAffected == 0 → not found。
//
// 验收：本函数没有单独的单测。跑 `go test ./internal/service/` 确认这个包仍能编译；
// 行为请按 README 第 3 节的冒烟流程验证。
func (s *FolderService) Delete(ctx context.Context, userID, folderID int64) error {
	// 第 1 步：先把链接从这个文件夹里摘出来（不是删链接）。
	// 归属校验顺便发生在这里：不是你的文件夹，这一步影响 0 行。
	if err := s.db.WithContext(ctx).
		Model(&entity.Link{}).
		Where("folder_id = ? AND user_id = ?", folderID, userID).
		Update("folder_id", nil).Error; err != nil {
		return err
	}

	// 第 2 步：删文件夹本身，WHERE 里同样必须有 user_id。
	res := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", folderID, userID).
		Delete(&entity.Folder{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("folder not found")
	}

	return nil
}
