package service

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
	"kada-mvp/internal/mq"
)

// LinkService 是短链的核心：创建、查询、跳转解析、更新、删除，以及记录点击。
//
// 对照原项目：kada/internal/service/link_service.go（786 行，是仓库里最大的一个文件）。
// MVP 保留了它的四条主线，每一章都对应一个"原项目踩过坑"的地方：
//
//  1. cache-aside：跳转先查 Redis，未命中才查库，查完回填；改删都要让相关短码失效。
//  2. 否定缓存：查不到的短码也记住 60 秒，且**只记"确实不存在"**。
//  3. 降级与幂等：点击走 Kafka；Kafka 不可用就直写库；event_id 保证不重复计数。
//  4. 归属校验：user_id 永远出现在 WHERE 里，而不是"查出来之后再判断"。
type LinkService struct {
	db          *gorm.DB
	baseURL     string
	cache       LinkCache
	kafka       mq.ClickPublisher
	clickWriter ClickWriter
	// group 把"同一个短码的并发未命中"合并成一次数据库读。
	//
	// 为什么需要它？因为缓存穿透最典型的形状就是"一堆请求同时问一个不存在的短码"：
	// 否定缓存是在**读完之后**才写上的，在写入之前，问这个短码的每个请求都会打一次库。
	// 原项目为此写过一条专门的测试（TestGetByCodeCollapsesConcurrentMissesIntoOneRead），
	// 这里也保留了同一个验收测试。
	group singleflight.Group
}

// NewLinkService 构造服务。已给（样板）。
//
// 三个可选依赖（cache / kafka / clickWriter）都允许为 nil-接口值：
//   - cache 为 nil：不缓存（Redis 没起来时的行为）。
//   - kafka 为 nil：点击直接写库（降级模式）。
//   - clickWriter 一般不为 nil（直写库的那条路要用它）。
func NewLinkService(db *gorm.DB, baseURL string, cache LinkCache, kafka mq.ClickPublisher, clickWriter ClickWriter) *LinkService {
	return &LinkService{
		db:          db,
		baseURL:     strings.TrimRight(baseURL, "/"),
		cache:       cache,
		kafka:       kafka,
		clickWriter: clickWriter,
	}
}

// shortCodePattern 是短码允许的形状（已给）。
//
// 3~20 位、只允许字母数字和 - _。为什么不允许别的？
// 因为短码会出现在 URL 路径里，也会被拼进跳转响应；让它"只能是一小段无害字符"，
// 比之后在每一个输出点转义它便宜得多。
var shortCodePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,20}$`)

// shortCodeAlphabet / shortCodeLength 是自动生成短码用的字符表和长度。
//
// 62 个字符（0-9a-zA-Z）大小写敏感，7 位 → 62^7 ≈ 3.5 万亿种，碰撞概率可忽略，
// 但仍然要 shortCodeTaken 检查（"可忽略"不等于"不会发生"）。
const (
	shortCodeAlphabet   = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	shortCodeLength     = 7
	shortCodeMaxRetries = 5
)

// linkListRow 是列表查询的一行：链接本体 + 连接出来的文件夹名（已给）。
//
// 注意这里是**嵌入** entity.Link，而不是把字段抄一遍：GORM 会把嵌入结构体的字段
// 当成同一张表的列来扫描，于是"一行里既有 links.* 又有 folders.name"可以直接落进来。
// 抄字段的写法会在加一列时忘记同步两处。
type linkListRow struct {
	entity.Link
	FolderName *string
}

// linkTagRow 是 tagsForLinks 的一行（链接 id + 标签本体）。
//
// 单独定义一个本文件内的小结构体，而不是把字段塞进 domain.LinkTagInfo：
// LinkTagInfo 是**对外**的形状，不该为了 SQL 方便多一个 link_id 字段。
type linkTagRow struct {
	LinkID int64
	ID     int64
	Name   string
	Color  string
}

// ==================== 公开方法 ====================

// Create 创建一条短链。
func (s *LinkService) Create(ctx context.Context, userID int64, req domain.CreateLinkRequest) (*domain.LinkInfo, error) {
	// 1. 只允许 http / https：`javascript:` 和 `data:` 的目标会在跳转页上执行脚本。
	if !isSafeTarget(req.OriginalURL) {
		return nil, errors.New("original_url must be an absolute http or https URL")
	}

	// 2. 短码：没传就生成；传了就先校验形状、再确认没被占用。
	var code string
	if req.ShortCode != nil && strings.TrimSpace(*req.ShortCode) != "" {
		code = strings.TrimSpace(*req.ShortCode)
		if !shortCodePattern.MatchString(code) {
			return nil, errors.New("short code must be 3-20 characters of letters, digits, hyphen or underscore")
		}
		if s.shortCodeTaken(ctx, code, 0) {
			return nil, errors.New("this short code is already taken")
		}
	} else {
		generated, err := s.generateShortCode()
		if err != nil {
			return nil, err
		}
		code = generated
	}

	// 3. 密码：明文进来，只把 bcrypt 结果带进数据库。
	var passwordHash *string
	if req.Password != nil && *req.Password != "" {
		h, err := hashPassword(*req.Password)
		if err != nil {
			return nil, fmt.Errorf("hash link password: %w", err)
		}
		passwordHash = &h
	}

	// 4. 过期时间：客户端给的是 RFC3339 字符串。
	var expiresAt *time.Time
	if req.ExpiresAt != nil && strings.TrimSpace(*req.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ExpiresAt))
		if err != nil {
			return nil, fmt.Errorf("expires_at must be an RFC3339 timestamp: %w", err)
		}
		expiresAt = &parsed
	}

	// 5. folder_id / workspace_id 是客户端传来的整数：不校验就等于允许
	// "把我的链接放进你的文件夹"。这是它们和数据库里归属之间唯一的桥梁。
	if err := s.validateOwnedRefs(ctx, userID, req.FolderID, req.WorkspaceID); err != nil {
		return nil, err
	}

	// 6. 落库。Domain 为空时用 baseURL 的 host 兜底。
	domainName := ""
	if req.Domain != nil {
		domainName = strings.TrimSpace(*req.Domain)
	}
	if domainName == "" {
		// 假设：baseURL 形如 "https://host[:port]"，去掉 scheme 之后剩下的部分就是对外域名。
		// MVP 不解析端口和路径——BuildShortURL 会原样使用它，想更严格时在这里换成 url.Parse。
		domainName = strings.TrimRight(strings.TrimPrefix(strings.TrimPrefix(s.baseURL, "https://"), "http://"), "/")
	}

	link := entity.Link{
		ShortCode:    code,
		OriginalURL:  req.OriginalURL,
		Title:        req.Title,
		Domain:       domainName,
		PasswordHash: passwordHash,
		ExpiresAt:    expiresAt,
		IsActive:     true,
		UserID:       userID,
		FolderID:     req.FolderID,
		WorkspaceID:  req.WorkspaceID,
	}
	if err := s.db.WithContext(ctx).Create(&link).Error; err != nil {
		if isDuplicateKey(err) {
			// 唯一约束撞了：说明另一个请求在这一瞬间抢到了同一个短码。
			return nil, errors.New("this short code is already taken")
		}
		return nil, fmt.Errorf("create link: %w", err)
	}

	// 7. 标签（只挂这个用户自己的标签；不属于他的 id 会被安静地忽略）。
	s.attachOwnedTags(ctx, userID, link.ID, req.TagIDs)

	// 8.
	return s.linkInfoFromEntity(&link), nil
}

// GetByID 按 id 取一条链接（只允许取自己的）。
func (s *LinkService) GetByID(ctx context.Context, linkID, userID int64) (*domain.LinkInfo, error) {
	var link entity.Link
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", linkID, userID).First(&link).Error
	if err != nil {
		if isNotFound(err) {
			// 不把 gorm 的错误抛出去：它会泄露"这行存在但不是你的"。
			return nil, domain.ErrLinkNotFound
		}
		return nil, fmt.Errorf("get link %d: %w", linkID, err)
	}
	return s.toLinkInfo(ctx, linkID, userID)
}

// GetByCode 是跳转路径的核心：按短码解析链接。
func (s *LinkService) GetByCode(ctx context.Context, shortCode string) (*domain.LinkInfo, error) {
	if s.cache != nil {
		// 1. 正向缓存命中。缓存里的那一行可能是 10 分钟前写进去的，
		//    期间链接可能刚好过期/停用，所以"从缓存返回"不能绕过可用性判断。
		if info, ok := s.cache.GetLink(ctx, shortCode); ok {
			if !isLinkUsable(info, nowUTC()) {
				s.invalidate(ctx, shortCode) // 顺手清掉这条脏缓存
				return nil, domain.ErrLinkNotFound
			}
			return info, nil
		}

		// 2. 否定缓存命中：直接给答案，不查库。
		//    这就是"扫描器把每个请求都变成一次查询"的解法。
		if s.cache.IsMissing(ctx, shortCode) {
			return nil, domain.ErrLinkNotFound
		}
	}

	// 3. 都没有：把"同一个短码的并发未命中"合并成一次数据库读。
	value, err, _ := s.group.Do(shortCode, func() (any, error) {
		return s.loadLink(ctx, shortCode)
	})
	if err != nil {
		// 5. ⚠ 只有"确实不存在"才写否定缓存。数据库连不上时绝不能写：
		//    否则一次数据库故障会把"所有短码都不存在"写进 Redis，而且它会活过故障本身。
		if errors.Is(err, domain.ErrLinkNotFound) {
			s.markMissing(ctx, shortCode)
			return nil, domain.ErrLinkNotFound
		}
		return nil, err
	}

	info, ok := value.(*domain.LinkInfo)
	if !ok || info == nil {
		return nil, errors.New("internal error: unexpected value type from singleflight group")
	}

	// 4.
	s.cacheLink(ctx, info)
	return info, nil
}

// CheckPassword 校验访问密码。
func (s *LinkService) CheckPassword(ctx context.Context, shortCode, password string) (bool, *domain.LinkInfo, error) {
	// 走库、不走缓存：这条路径本来就不热，而且 LinkInfo 里压根没有能装哈希的字段
	// （那是设计好的），所以哈希只能从数据库行里拿。
	// loadLinkEntity 就是 loadLink 里"读一行"的那一半，错误分类完全一样。
	link, err := s.loadLinkEntity(ctx, shortCode)
	if err != nil {
		return false, nil, err
	}

	info := s.linkInfoFromEntity(link)
	if !isLinkUsable(info, nowUTC()) {
		return false, nil, domain.ErrLinkNotFound
	}

	// 没设密码 = 谁都可以访问。
	if link.PasswordHash == nil {
		return true, info, nil
	}
	return checkPassword(*link.PasswordHash, password), info, nil
}

// List 返回分页后的链接列表，支持搜索和筛选。
func (s *LinkService) List(ctx context.Context, userID int64, page, pageSize int, search string, folderID, tagID, workspaceID int64, sort string) (*domain.PaginatedLinks, error) {
	page, pageSize = normalizePage(page, pageSize)

	// 归属是这个列表的**第一个**条件，不是查完再过滤。
	filters := []string{"l.user_id = ?"}
	args := []any{userID}

	if search = strings.TrimSpace(search); search != "" {
		filters = append(filters, "(l.title ILIKE ? OR l.original_url ILIKE ? OR l.short_code ILIKE ?)")
		like := "%" + search + "%"
		args = append(args, like, like, like)
	}
	if folderID > 0 {
		filters = append(filters, "l.folder_id = ?")
		args = append(args, folderID)
	}
	if tagID > 0 {
		// 子查询而不是 JOIN：JOIN 会让"一条链接有多个标签"变成多行，把分页数搞乱。
		filters = append(filters, "l.id IN (SELECT link_id FROM link_tags WHERE tag_id = ?)")
		args = append(args, tagID)
	}
	if workspaceID > 0 {
		filters = append(filters, "l.workspace_id = ?")
		args = append(args, workspaceID)
	}

	where := strings.Join(filters, " AND ")

	// 总数：同样的条件数一遍，返回给前端算页数。
	var total int64
	if err := s.db.WithContext(ctx).Table("links AS l").Where(where, args...).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count links: %w", err)
	}

	var rows []linkListRow
	err := s.db.WithContext(ctx).
		Table("links AS l").
		Select("l.*, f.name AS folder_name").
		Joins("LEFT JOIN folders AS f ON f.id = l.folder_id AND f.user_id = l.user_id").
		Where(where, args...).
		Order(linkListOrderClause(sort)).
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}

	// 标签一次性取回（每行查一次就是 N+1）。
	ids := make([]int64, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].ID)
	}
	tags, err := s.tagsForLinks(ctx, userID, ids)
	if err != nil {
		return nil, err
	}

	links := make([]domain.LinkInfo, 0, len(rows))
	for i := range rows {
		info := s.linkInfoFromEntity(&rows[i].Link)
		info.FolderName = rows[i].FolderName
		if t, ok := tags[info.ID]; ok {
			info.Tags = t
		}
		links = append(links, *info)
	}

	return &domain.PaginatedLinks{
		Links:      links,
		TotalCount: total,
		Page:       page,
		PageSize:   pageSize,
	}, nil
}

// Update 更新一条链接（只允许改自己的）。
func (s *LinkService) Update(ctx context.Context, linkID, userID int64, req domain.UpdateLinkRequest) (*domain.LinkInfo, error) {
	var link entity.Link
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", linkID, userID).First(&link).Error
	if err != nil {
		if isNotFound(err) {
			return nil, domain.ErrLinkNotFound
		}
		return nil, fmt.Errorf("load link %d: %w", linkID, err)
	}
	oldCode := link.ShortCode
	newCode := ""

	// 可空字段要被**清空**时，必须写 gorm.Expr("NULL")：GORM 的 Updates(struct) 会忽略
	// 零值，所以 `Updates(Link{FolderID: nil})` 永远不会把 folder_id 改回 NULL。
	// 这里统一用 Updates(map)：map 能表达"把字段改成零值/NULL"。
	updates := map[string]any{}

	if req.OriginalURL != nil {
		if !isSafeTarget(*req.OriginalURL) {
			return nil, errors.New("original_url must be an absolute http or https URL")
		}
		updates["original_url"] = *req.OriginalURL
	}
	if req.ShortCode != nil {
		code := strings.TrimSpace(*req.ShortCode)
		if !shortCodePattern.MatchString(code) {
			return nil, errors.New("short code must be 3-20 characters of letters, digits, hyphen or underscore")
		}
		// 排除自己：把 a 改成 a（等于没改）不该被判成冲突。
		if s.shortCodeTaken(ctx, code, linkID) {
			return nil, errors.New("this short code is already taken")
		}
		newCode = code
		updates["short_code"] = code
	}
	if req.Title != nil {
		updates["title"] = *req.Title
	}
	if req.Domain != nil {
		updates["domain"] = strings.TrimSpace(*req.Domain)
	}
	if req.Password != nil {
		if *req.Password == "" {
			// 空字符串 = 取消密码。
			updates["password_hash"] = gorm.Expr("NULL")
		} else {
			h, err := hashPassword(*req.Password)
			if err != nil {
				return nil, fmt.Errorf("hash link password: %w", err)
			}
			updates["password_hash"] = h
		}
	}
	if req.ExpiresAt != nil {
		if strings.TrimSpace(*req.ExpiresAt) == "" {
			updates["expires_at"] = gorm.Expr("NULL")
		} else {
			parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*req.ExpiresAt))
			if err != nil {
				return nil, fmt.Errorf("expires_at must be an RFC3339 timestamp: %w", err)
			}
			updates["expires_at"] = parsed
		}
	}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	if req.FolderID != nil {
		if *req.FolderID > 0 {
			updates["folder_id"] = *req.FolderID
		} else {
			updates["folder_id"] = gorm.Expr("NULL")
		}
	}
	if req.WorkspaceID != nil {
		if *req.WorkspaceID > 0 {
			updates["workspace_id"] = *req.WorkspaceID
		} else {
			updates["workspace_id"] = gorm.Expr("NULL")
		}
	}

	// 和 Create 一样：客户端传来的 folder_id / workspace_id 必须证明是自己的。
	if err := s.validateOwnedRefs(ctx, userID, req.FolderID, req.WorkspaceID); err != nil {
		return nil, err
	}

	if len(updates) > 0 {
		err := s.db.WithContext(ctx).Model(&entity.Link{}).
			Where("id = ? AND user_id = ?", linkID, userID).
			Updates(updates).Error
		if err != nil {
			if isDuplicateKey(err) {
				return nil, errors.New("this short code is already taken")
			}
			return nil, fmt.Errorf("update link %d: %w", linkID, err)
		}
	}

	// 标签是"整体替换"：先删旧关联，再挂新的。nil 表示"这次不动标签"，
	// 空切片才表示"清空标签"——把 nil 也当成清空会让一次改标题顺手删掉所有标签。
	if req.TagIDs != nil {
		if err := s.db.WithContext(ctx).
			Where("link_id = ?", linkID).
			Delete(&entity.LinkTag{}).Error; err != nil {
			return nil, fmt.Errorf("replace tags of link %d: %w", linkID, err)
		}
		s.attachOwnedTags(ctx, userID, linkID, req.TagIDs)
	}

	// 改名前后的入口都不能留着旧数据，所以两个短码都要失效。
	s.invalidate(ctx, oldCode)
	if newCode != "" && newCode != oldCode {
		s.invalidate(ctx, newCode)
	}

	return s.toLinkInfo(ctx, linkID, userID)
}

// Delete 删除一条链接。
func (s *LinkService) Delete(ctx context.Context, linkID, userID int64) error {
	// 先取短码：删完就查不到它了，而缓存必须失效。
	var link entity.Link
	err := s.db.WithContext(ctx).Select("short_code").
		Where("id = ? AND user_id = ?", linkID, userID).First(&link).Error
	if err != nil {
		if isNotFound(err) {
			return domain.ErrLinkNotFound
		}
		return fmt.Errorf("load link %d: %w", linkID, err)
	}

	// user_id 同样在 WHERE 里，而不是"先查出来再判断"。
	if err := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", linkID, userID).
		Delete(&entity.Link{}).Error; err != nil {
		return fmt.Errorf("delete link %d: %w", linkID, err)
	}

	// click_logs 会随外键 CASCADE 一起走（见 entity 里的约束）。
	s.invalidate(ctx, link.ShortCode)
	return nil
}

// BatchDelete 批量删除，返回实际删掉的行数。
func (s *LinkService) BatchDelete(ctx context.Context, ids []int64, userID int64) (int64, error) {
	if len(ids) == 0 {
		// 不要生成 `id IN ()` 这种非法 SQL。
		return 0, nil
	}

	// 先查 short_code 再删：删完就查不到短码了，而缓存必须失效。
	var codes []string
	if err := s.db.WithContext(ctx).Model(&entity.Link{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		Pluck("short_code", &codes).Error; err != nil {
		return 0, fmt.Errorf("load short codes: %w", err)
	}

	// 注意 GORM 的 `IN ?` 写法（问号前不加括号）：它会正确展开成 ($1,$2,$3)。
	res := s.db.WithContext(ctx).
		Where("id IN ? AND user_id = ?", ids, userID).
		Delete(&entity.Link{})
	if res.Error != nil {
		return 0, fmt.Errorf("batch delete links: %w", res.Error)
	}

	for _, code := range codes {
		s.invalidate(ctx, code)
	}
	return res.RowsAffected, nil
}

// BatchTag 给多条链接批量打同一个标签。
func (s *LinkService) BatchTag(ctx context.Context, ids []int64, tagID int64, userID int64) error {
	if len(ids) == 0 {
		return nil
	}

	// 1. 标签必须是自己的：否则就是把别人的标签挂到自己的链接上。
	var tagCount int64
	if err := s.db.WithContext(ctx).Model(&entity.Tag{}).
		Where("id = ? AND user_id = ?", tagID, userID).
		Count(&tagCount).Error; err != nil {
		return fmt.Errorf("load tag %d: %w", tagID, err)
	}
	if tagCount == 0 {
		return errors.New("tag not found")
	}

	// 2. 链接也必须都是自己的：数量相等即全部命中。
	var linkCount int64
	if err := s.db.WithContext(ctx).Model(&entity.Link{}).
		Where("id IN ? AND user_id = ?", ids, userID).
		Count(&linkCount).Error; err != nil {
		return fmt.Errorf("load links: %w", err)
	}
	if linkCount != int64(len(ids)) {
		return errors.New("some links were not found")
	}

	// 3. link_tags 的主键是 (link_id, tag_id)，所以"重复打标签"天然被拒：
	//    ON CONFLICT DO NOTHING 让这个操作变成幂等的。
	for _, id := range ids {
		err := s.db.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&entity.LinkTag{LinkID: id, TagID: tagID}).Error
		if err != nil {
			return fmt.Errorf("tag link %d: %w", id, err)
		}
	}
	return nil
}

// LogClick 记录一次点击。
func (s *LinkService) LogClick(ctx context.Context, e domain.ClickEvent) {
	// 1. 幂等键。随机数失败时退化成一个时间戳兜底值：
	//    "取不到随机数"绝不能变成"丢掉这次点击"。
	eventID, err := randomID()
	if err != nil || eventID == "" {
		eventID = fmt.Sprintf("fallback-%d-%d", e.LinkID, nowUTC().UnixNano())
	}

	// 2. 同一个 event 从造出来到最后落库都不再改：Kafka 和降级直写必须是同一个 event_id，
	//    否则 worker 和直写会各写一行（event_id 的 ON CONFLICT 是唯一的保证）。
	event := mq.ClickEvent{
		EventID:   eventID,
		LinkID:    e.LinkID,
		IP:        e.IP,
		UserAgent: e.UserAgent,
		Platform:  string(e.Platform),
		Kind:      string(e.Kind),
		Referer:   e.Referer,
		CreatedAt: nowUTC(),
	}

	// 3./4. Kafka 可用就发消息。这个函数在 handler 起的 goroutine 里跑，
	// Gin 的 recovery 覆盖不到它，所以 publish 必须自己兜住 panic。
	if s.kafka != nil {
		pubErr := s.publishClickSafely(ctx, event)
		if pubErr == nil {
			return
		}
		log.Printf("publish click event %s for link %d failed, falling back to direct write: %v",
			event.EventID, event.LinkID, pubErr)
	}

	// 5. 降级路径：直接写库，失败只记日志（跳转响应不能因为点击没记上而失败）。
	if s.clickWriter == nil {
		log.Printf("click event %s for link %d was dropped: no click writer configured",
			event.EventID, event.LinkID)
		return
	}
	if err := s.clickWriter.WriteClick(ctx, event); err != nil {
		log.Printf("direct write of click event %s for link %d failed: %v",
			event.EventID, event.LinkID, err)
	}
}

// ==================== 内部方法 ====================

// loadLinkEntity 从数据库按短码读一行，并把"查不到"和"查不动"分成两种错误。
//
// 这是 loadLink 和 CheckPassword 共用的那一步（CheckPassword 需要 PasswordHash，
// 而 domain.LinkInfo 里根本没有这个字段，所以它必须拿到 entity.Link）。
//   - 查不到（isNotFound）→ domain.ErrLinkNotFound
//   - 其他错误 → 用 %w 包一层（**绝不能**返回 ErrLinkNotFound：把两者混在一起，
//     就等于让一次数据库故障变成"所有短码都不存在"，而那个结论会按 TTL 活过故障本身）
func (s *LinkService) loadLinkEntity(ctx context.Context, shortCode string) (*entity.Link, error) {
	var link entity.Link
	if err := s.db.WithContext(ctx).Where("short_code = ?", shortCode).First(&link).Error; err != nil {
		if isNotFound(err) {
			return nil, domain.ErrLinkNotFound
		}
		return nil, fmt.Errorf("load link by short code %q: %w", shortCode, err)
	}
	return &link, nil
}

// loadLink 从数据库按短码读一条链接。
func (s *LinkService) loadLink(ctx context.Context, shortCode string) (*domain.LinkInfo, error) {
	link, err := s.loadLinkEntity(ctx, shortCode)
	if err != nil {
		return nil, err
	}

	info := s.linkInfoFromEntity(link)
	// 查到但停用/过期 → 对调用方来说，"停用了"和"不存在"是同一个答案。
	if !isLinkUsable(info, nowUTC()) {
		return nil, domain.ErrLinkNotFound
	}
	return info, nil
}

// cacheLink 把链接写进缓存，并清掉它的否定缓存。
func (s *LinkService) cacheLink(ctx context.Context, info *domain.LinkInfo) {
	if s.cache == nil || info == nil {
		return
	}
	// 顺序有意义：先清否定缓存，再写正向缓存。反过来的话，
	// ClearMissing 会把刚写进去的正向缓存一起删掉（它们是两个键，但顺序错了就白写）。
	s.cache.ClearMissing(ctx, info.ShortCode)
	s.cache.SetLink(ctx, info)
}

// markMissing 记住"这个短码不存在"。
func (s *LinkService) markMissing(ctx context.Context, shortCode string) {
	if s.cache == nil {
		return
	}
	s.cache.MarkMissing(ctx, shortCode)
}

// invalidate 让一个短码的缓存失效（已给）。
//
// 这个 nil 判断值得读一遍：**它检查的是接口本身**。只要 cache 是用
// `var cache LinkCache; if redis != nil { cache = NewCacheService(redis) }` 这样
// 赋值的，nil 判断就是对的；而如果写成 `cache := NewCacheService(maybeNilRedis)`，
// 那么 cache 永远不是 nil，这个判断就永远不会生效（见 CacheService 的注释）。
func (s *LinkService) invalidate(ctx context.Context, shortCode string) {
	if shortCode == "" || s.cache == nil {
		return
	}
	s.cache.InvalidateLink(ctx, shortCode)
}

// toLinkInfo 补齐文件夹名和标签。
func (s *LinkService) toLinkInfo(ctx context.Context, linkID, userID int64) (*domain.LinkInfo, error) {
	var row linkListRow
	err := s.db.WithContext(ctx).
		Table("links AS l").
		Select("l.*, f.name AS folder_name").
		Joins("LEFT JOIN folders AS f ON f.id = l.folder_id AND f.user_id = l.user_id").
		Where("l.id = ? AND l.user_id = ?", linkID, userID).
		Limit(1).
		Find(&row).Error
	if err != nil {
		return nil, fmt.Errorf("load link %d: %w", linkID, err)
	}

	info := s.linkInfoFromEntity(&row.Link)
	info.FolderName = row.FolderName

	tags, err := s.tagsForLinks(ctx, userID, []int64{linkID})
	if err != nil {
		return nil, err
	}
	if t, ok := tags[linkID]; ok {
		info.Tags = t
	}
	return info, nil
}

// tagsForLinks 一次取回多条链接的标签，避免 N+1。
func (s *LinkService) tagsForLinks(ctx context.Context, userID int64, linkIDs []int64) (map[int64][]domain.LinkTagInfo, error) {
	out := map[int64][]domain.LinkTagInfo{}
	if len(linkIDs) == 0 {
		return out, nil
	}

	const query = `
		SELECT lt.link_id, t.id, t.name, t.color
		  FROM link_tags AS lt
		  JOIN tags AS t ON t.id = lt.tag_id
		 WHERE lt.link_id IN ? AND t.user_id = ?`

	var rows []linkTagRow
	// t.user_id 这个条件也要有：标签是用户的，不是链接的。
	// 用 Find 而不是 Scan：两者在真实数据库上生成的 SQL 完全相同，
	// 但 Scan 走的是 sql.Rows 那条路，DryRun 会话会以 ErrDryRunModeUnsupported 结束
	// （GORM 的 Scan/Rows 不支持 DryRun），而 Find 走 Query 回调。
	if err := s.db.WithContext(ctx).Raw(query, linkIDs, userID).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load tags of links: %w", err)
	}

	for _, row := range rows {
		out[row.LinkID] = append(out[row.LinkID], domain.LinkTagInfo{
			ID:    row.ID,
			Name:  row.Name,
			Color: row.Color,
		})
	}
	return out, nil
}

// generateShortCode 生成一个没被占用的短码。
func (s *LinkService) generateShortCode() (string, error) {
	// 用 crypto/rand（**不是 math/rand**）：短码可预测意味着别人能猜到还没人用的短码，
	// 从而"抢占"将来某个人的短码。
	alphabetLen := big.NewInt(int64(len(shortCodeAlphabet)))

	for attempt := 0; attempt < shortCodeMaxRetries; attempt++ {
		var sb strings.Builder
		sb.Grow(shortCodeLength)
		for i := 0; i < shortCodeLength; i++ {
			n, err := rand.Int(rand.Reader, alphabetLen)
			if err != nil {
				return "", fmt.Errorf("generate short code: %w", err)
			}
			sb.WriteByte(shortCodeAlphabet[n.Int64()])
		}
		code := sb.String()

		// 假设：这个函数没有 ctx 参数（签名是已给的），所以这里用 context.Background()。
		// 它只做一次 count 查询，不参与调用方的取消/超时。
		if !s.shortCodeTaken(context.Background(), code, 0) {
			return code, nil
		}
	}
	// 5 次都撞上就报错，而不是死循环。
	return "", errors.New("could not generate an unused short code, please try again")
}

// shortCodeTaken 报告短码是否已被占用。
func (s *LinkService) shortCodeTaken(ctx context.Context, shortCode string, exceptID int64) bool {
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.Link{}).
		Where("short_code = ? AND id <> ?", shortCode, exceptID).
		Count(&n).Error
	if err != nil {
		// 宁可误判"被占用"而报一个可读的错，也不要因为查询失败而放行一个冲突的短码。
		return true
	}
	return n > 0
}

// validateOwnedRefs 确认 folder_id / workspace_id 属于这个用户。
func (s *LinkService) validateOwnedRefs(ctx context.Context, userID int64, folderID, workspaceID *int64) error {
	// 两个指针都可能为 nil（表示"不设置"）；<= 0 在 Update 里表示"清空"。
	if folderID != nil && *folderID > 0 {
		var n int64
		err := s.db.WithContext(ctx).Model(&entity.Folder{}).
			Where("id = ? AND user_id = ?", *folderID, userID).
			Count(&n).Error
		if err != nil {
			return fmt.Errorf("check folder %d: %w", *folderID, err)
		}
		if n == 0 {
			return errors.New("folder not found")
		}
	}
	if workspaceID != nil && *workspaceID > 0 {
		var n int64
		err := s.db.WithContext(ctx).Model(&entity.Workspace{}).
			Where("id = ? AND user_id = ?", *workspaceID, userID).
			Count(&n).Error
		if err != nil {
			return fmt.Errorf("check workspace %d: %w", *workspaceID, err)
		}
		if n == 0 {
			return errors.New("workspace not found")
		}
	}
	return nil
}

// attachOwnedTags 把标签挂到链接上（只挂这个用户自己的标签）。
func (s *LinkService) attachOwnedTags(ctx context.Context, userID, linkID int64, tagIDs []int64) {
	if len(tagIDs) == 0 {
		return
	}

	// 先查出这些 id 里**确实属于该用户**的标签。
	var owned []int64
	err := s.db.WithContext(ctx).Model(&entity.Tag{}).
		Where("id IN ? AND user_id = ?", tagIDs, userID).
		Pluck("id", &owned).Error
	if err != nil {
		log.Printf("load owned tags for link %d failed: %v", linkID, err)
		return
	}

	for _, tagID := range owned {
		// 重复打标签是"无害操作"：主键 (link_id, tag_id) + ON CONFLICT DO NOTHING。
		err := s.db.WithContext(ctx).
			Clauses(clause.OnConflict{DoNothing: true}).
			Create(&entity.LinkTag{LinkID: linkID, TagID: tagID}).Error
		if err != nil {
			log.Printf("attach tag %d to link %d failed: %v", tagID, linkID, err)
		}
	}
	// 不属于该用户的 id 被安静地忽略：不报错，更不能把它们挂上去。
}

// publishClickSafely 调一次 Kafka，并把 panic 转成 error。
//
// 为什么要 recover？见 LogClick 的注释：原项目每次点击都调用一个 nil 接收者的方法 →
// panic → 进程退出 → systemd 重启 → 那几秒的请求全部 502，而且**点击丢了**
// （降级路径根本没跑到）。recover 之后这次点击会走直写库的那条路。
func (s *LinkService) publishClickSafely(ctx context.Context, event mq.ClickEvent) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("publish click panicked: %v", r)
		}
	}()
	return s.kafka.PublishClick(ctx, event)
}

// ==================== 已给的辅助函数 ====================

// linkListOrderClause 把 sort 参数映射成固定的 ORDER BY 片段。
//
// 白名单是必须的：sort 参数**绝不能**直接拼进 SQL（那是最经典的注入了）。
// 不认识的值一律落到默认排序，而不是报错——一个排序参数写错的列表，比一个 400 更友好。
func linkListOrderClause(sort string) string {
	switch sort {
	case "created_asc":
		return "l.created_at ASC"
	case "clicks_desc":
		return "l.click_count DESC"
	case "created_desc":
		return "l.created_at DESC"
	default:
		return "l.created_at DESC"
	}
}

// isLinkUsable 报告一条链接现在能不能用（**已给，但请读一遍**）。
//
// 两个条件：启用中，且没有过期。注意"过期"用的比较对象是 now 而不是 time.Now()：
// 这样可以测——测试可以传一个固定时间，而不是靠 sleep 来制造过期。
func isLinkUsable(info *domain.LinkInfo, now time.Time) bool {
	if info == nil || !info.IsActive {
		return false
	}
	return info.ExpiresAt == nil || info.ExpiresAt.After(now)
}

// linkInfoFromEntity 把数据库行映射成对外的 LinkInfo。
//
// 这是"密码哈希绝不能离开数据库"的那道闸门：domain.LinkInfo 里根本没有能装哈希的字段，
// 这里只发布 HasPassword 这个事实。
func (s *LinkService) linkInfoFromEntity(l *entity.Link) *domain.LinkInfo {
	if l == nil {
		return nil
	}
	return &domain.LinkInfo{
		ID:          l.ID,
		ShortCode:   l.ShortCode,
		ShortURL:    s.BuildShortURL(l.Domain, l.ShortCode),
		OriginalURL: l.OriginalURL,
		Title:       l.Title,
		Domain:      l.Domain,
		ClickCount:  l.ClickCount,
		IsActive:    l.IsActive,
		ExpiresAt:   l.ExpiresAt,
		CreatedAt:   l.CreatedAt,
		UpdatedAt:   l.UpdatedAt,
		FolderID:    l.FolderID,
		WorkspaceID: l.WorkspaceID,
		// 空切片而不是 nil：JSON 里 `null` 和 `[]` 对前端是两种东西，
		// 而"没有标签"应该序列化成 []。
		Tags:        []domain.LinkTagInfo{},
		HasPassword: l.PasswordHash != nil,
	}
}

// BuildShortURL 拼出对外的短链（已给）。
func (s *LinkService) BuildShortURL(domainName, code string) string {
	if domainName == "" {
		return strings.TrimRight(s.baseURL, "/") + "/r/" + code
	}
	return "https://" + domainName + "/r/" + code
}

// normalizePage 把分页参数收进合理范围（已给）。
//
// 为什么必须夹紧 pageSize？因为 `?page_size=1000000` 是一个不需要任何技术的
// 资源消耗方式，而它看起来只是一个普通的查询参数。
func normalizePage(page, pageSize int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return page, pageSize
}

// nowUTC 统一取当前时间（已给）：全项目的时间比较都用 UTC，
// 否则"服务器时区不同"会表现为"过期时间差了几个小时"这种极难复现的问题。
func nowUTC() time.Time { return time.Now().UTC() }

// isSafeTarget 报告目标 URL 是否可以安全地跳转。
//
// 只允许 http / https 两种 scheme，且必须有主机。
// 不用 strings.HasPrefix 判断：`HTTP://evil.com`、`\tjavascript:`、`java\nscript:`
// 这类变体就是靠"看起来像前缀"绕过检查的，而 url.Parse 会先把它们规范化。
//
// 放在**写入时**（Create/Update）检查，跳转时是否再查一次由 redirect handler 决定
// （那是为了防历史脏数据）。
func isSafeTarget(raw string) bool {
	// 先 trim：`"  javascript:alert(1)"` 这种前置空白不该改变结论
	// （url.Parse 对控制字符虽会直接报错，但普通空格不会）。
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	return u.Host != ""
}
