package service

import (
	"context"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
	"kada-mvp/internal/todo"
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
// 验收：go test ./internal/service/ -run TestFolderService
func (s *FolderService) Create(ctx context.Context, userID int64, req domain.CreateFolderRequest) (*domain.Folder, error) {
	return nil, todo.Err("FolderService.Create")
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
// 验收：go test ./internal/service/ -run TestFolderService
func (s *FolderService) List(ctx context.Context, userID int64) ([]domain.Folder, error) {
	return nil, todo.Err("FolderService.List")
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
// 验收：go test ./internal/service/ -run TestFolderService
func (s *FolderService) Update(ctx context.Context, userID, folderID int64, name string) (*domain.Folder, error) {
	return nil, todo.Err("FolderService.Update")
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
// 验收：go test ./internal/service/ -run TestFolderServiceDeleteDetachesLinks
func (s *FolderService) Delete(ctx context.Context, userID, folderID int64) error {
	return todo.Err("FolderService.Delete")
}

// 这个空引用是给你的实现准备的（entity.Folder 会用到）：
// 填空阶段它还没有被使用，用它保持编译通过。写完删掉。
var _ = entity.Folder{}
