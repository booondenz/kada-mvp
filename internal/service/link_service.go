package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
	"kada-mvp/internal/mq"
	"kada-mvp/internal/todo"
)

// LinkService 是短链的核心：创建、查询、跳转解析、更新、删除，以及记录点击。
//
// 对照原项目：kada/internal/service/link_service.go（786 行，是仓库里最大的一个文件）。
// MVP 保留了它的四条主线，每一章都对应一个"原项目踩过坑"的地方：
//
//	1. cache-aside：跳转先查 Redis，未命中才查库，查完回填；改删都要让相关短码失效。
//	2. 否定缓存：查不到的短码也记住 60 秒，且**只记"确实不存在"**。
//	3. 降级与幂等：点击走 Kafka；Kafka 不可用就直写库；event_id 保证不重复计数。
//	4. 归属校验：user_id 永远出现在 WHERE 里，而不是"查出来之后再判断"。
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

// linkListRow 是列表查询的一行：链接本体 + 连接出来的文件夹名（已给）。
//
// 注意这里是**嵌入** entity.Link，而不是把字段抄一遍：GORM 会把嵌入结构体的字段
// 当成同一张表的列来扫描，于是"一行里既有 links.* 又有 folders.name"可以直接落进来。
// 抄字段的写法会在加一列时忘记同步两处。
type linkListRow struct {
	entity.Link
	FolderName *string
}

// ==================== 公开方法（要你填的部分） ====================

// Create 创建一条短链。
//
// TODO(练习 26)：实现它。这一段是"输入校验 → 生成标识 → 落库 → 维护缓存"的完整样本。
//
//	1. isSafeTarget(req.OriginalURL) 必须为真，否则返回错误。
//	   （只允许 http/https。这条不是形式主义：`javascript:` 和 `data:` 的目标 URL
//	    会在跳转页上执行脚本。）
//	2. 短码：
//	   - req.ShortCode 为空 → generateShortCode()；
//	   - 传了就先用 shortCodePattern 校验，再 shortCodeTaken() 确认没被占用；
//	     占用返回一个能直接给用户看的错误（例如 "this short code is already taken"）。
//	3. 密码：req.Password 非空 → hashPassword(*req.Password)。
//	4. 过期时间：req.ExpiresAt 非空 → time.Parse(time.RFC3339, *req.ExpiresAt)，解析失败返回错误。
//	5. 归属校验：validateOwnedRefs(ctx, userID, req.FolderID, req.WorkspaceID)。
//	   为什么必须做？因为 folder_id / workspace_id 是客户端传进来的整数，
//	   不校验就等于允许"把我的链接放进你的文件夹"。
//	6. 落库：插入 entity.Link{...}，Domain 为空时用 s.baseURL 的 host 兜底
//	   （提示：可以用 strings.TrimPrefix(s.baseURL, "https://") 之类的简单处理，
//	     MVP 不追求完美，但要在注释里写清你的假设）。
//	   撞唯一约束（isDuplicateKey(err)）说明短码被并发抢了 → 返回可读的错误。
//	7. 标签：attachOwnedTags(ctx, userID, link.ID, req.TagIDs)。
//	8. 返回 s.linkInfoFromEntity(&link)（里面会拼好 ShortURL）。
//
// 验收：go test ./internal/service/ -run TestCreateLink
func (s *LinkService) Create(ctx context.Context, userID int64, req domain.CreateLinkRequest) (*domain.LinkInfo, error) {
	return nil, todo.Err("LinkService.Create")
}

// GetByID 按 id 取一条链接（只允许取自己的）。
//
// TODO(练习 27)：实现它。
//
//  1. 查询条件里**必须**同时有 id 和 user_id：
//     s.db.WithContext(ctx).Where("id = ? AND user_id = ?", linkID, userID).First(&link)
//     这不是"多此一举的安全检查"，而是**唯一的**归属校验——查不到就是 404，
//     别人的 id 在我这里等于不存在。
//  2. 查不到 → 返回 domain.ErrLinkNotFound（不要直接抛 gorm 的错误出去，
//     那会泄露"这行存在但不是你的"）。
//  3. 用 toLinkInfo 补上文件夹名和标签。
//
// 验收：go test ./internal/service/ -run TestGetByIDEnforcesOwnership
func (s *LinkService) GetByID(ctx context.Context, linkID, userID int64) (*domain.LinkInfo, error) {
	return nil, todo.Err("LinkService.GetByID")
}

// GetByCode 是跳转路径的核心：按短码解析链接。
//
// TODO(练习 28)：实现它。这一段是 cache-aside 的教科书形状，而且有两个细节必须做对：
//
//	1. 缓存命中（s.cache != nil 且 GetLink 返回 ok）→ 先 isLinkUsable 再返回。
//	   **为什么缓存命中还要再检查？** 因为缓存里那一行可能是 10 分钟前写进去的，
//	   而这段时间里链接可能刚好过期。"从缓存返回"绝不能绕过"它还有效吗"这个判断。
//	   不可用 → 返回 domain.ErrLinkNotFound（顺手 invalidate 掉这条脏缓存）。
//	2. 否定缓存命中（s.cache.IsMissing 为真）→ 直接返回 domain.ErrLinkNotFound，**不查库**。
//	   这就是"扫描器把每个请求都变成一次查询"的解法。
//	3. 都没有 → 用 s.group.Do(shortCode, func() (any, error) { return s.loadLink(ctx, shortCode) })
//	   把并发合并成一次读（见 struct 上 group 的注释）。
//	   注意 group.Do 返回的是 (any, error)，你要做类型断言；断言失败当内部错误处理。
//	4. loadLink 成功 → cacheLink(ctx, info) 后返回。
//	5. loadLink 失败且是"确实不存在"（errors.Is(err, domain.ErrLinkNotFound)）→
//	   markMissing(ctx, shortCode) 后返回该错误。
//	   ⚠ 只有"确实不存在"才写否定缓存。**数据库连不上时绝不能写**：
//	     否则一次数据库故障会把"所有短码都不存在"这个结论写进 Redis，
//	     而且它会按 TTL 活过故障本身（原项目把这条写进了设计文档）。
//	     所以 loadLink 必须把"查不到"和"查不动"分成两种错误返回。
//
// 验收：go test ./internal/service/ -run TestGetByCode
func (s *LinkService) GetByCode(ctx context.Context, shortCode string) (*domain.LinkInfo, error) {
	return nil, todo.Err("LinkService.GetByCode")
}

// CheckPassword 校验访问密码。
//
// TODO(练习 29)：实现它。
//
//  1. loadLink(ctx, shortCode)（走库，不走缓存：这条路径本来就不热，
//     而且从缓存拿到的 LinkInfo 里没有哈希——它压根没这个字段，这是设计好的）。
//  2. 如果链接没有密码（PasswordHash == nil）→ return true, info, nil
//     （没设密码 = 谁都可以访问）。
//  3. checkPassword(*link.PasswordHash, password) → 返回 (ok, 映射后的 LinkInfo, nil)。
//
// 验收：go test ./internal/service/ -run TestCheckPassword
func (s *LinkService) CheckPassword(ctx context.Context, shortCode, password string) (bool, *domain.LinkInfo, error) {
	return false, nil, todo.Err("LinkService.CheckPassword")
}

// List 返回分页后的链接列表，支持搜索和筛选。
//
// TODO(练习 30)：实现它。这是全项目最容易写出"能跑但会错"的 SQL 的地方。
//
//	1. 先 normalizePage(page, pageSize)（已给）。
//	2. 拼条件切片 filters []string 和参数 args []any，第一个条件永远是 "l.user_id = ?"。
//	   **归属是这个列表的第一个条件**，不是查完再过滤。
//	3. 可选条件：
//	   - search 非空 → "(l.title ILIKE ? OR l.original_url ILIKE ? OR l.short_code ILIKE ?)"，
//	     三个参数都用 "%"+search+"%"（ILIKE 是 PostgreSQL 的大小写不敏感匹配）。
//	   - folderID > 0 → "l.folder_id = ?"
//	   - tagID > 0 → "l.id IN (SELECT link_id FROM link_tags WHERE tag_id = ?)"
//	     （子查询而不是 JOIN：JOIN 会让"一条链接有多个标签"变成多行，把分页数搞乱。）
//	   - workspaceID > 0 → "l.workspace_id = ?"
//	4. 总数：先用同样的条件 Count 一次（返回给前端算页数）。
//	5. 数据：Table("links AS l").Select("l.*, f.name AS folder_name").
//	   Joins("LEFT JOIN folders AS f ON f.id = l.folder_id AND f.user_id = l.user_id").
//	   Where(strings.Join(filters, " AND "), args...)。Order(...).Limit().Offset().
//	   Scan(&[]linkListRow{})。
//	6. 排序白名单：sort 参数**绝不能**直接拼进 SQL（那是最经典的注入了）。
//	   只允许几个固定值映射到固定字符串，例如：
//	   "created_desc" → "l.created_at DESC"，"created_asc" → "l.created_at ASC"，
//	   "clicks_desc" → "l.click_count DESC"；其他一律用默认值。
//	7. 标签：用 tagsForLinks(ctx, userID, ids) 一次性取全部（不要每行查一次，那是 N+1）。
//	8. 组装 domain.PaginatedLinks{Links, TotalCount, Page, PageSize}。
//
// 验收：go test ./internal/service/ -run TestListLinks
func (s *LinkService) List(ctx context.Context, userID int64, page, pageSize int, search string, folderID, tagID, workspaceID int64, sort string) (*domain.PaginatedLinks, error) {
	return nil, todo.Err("LinkService.List")
}

// Update 更新一条链接（只允许改自己的）。
//
// TODO(练习 31)：实现它。
//
//	1. 先按 id + user_id 把行查出来（不存在 → domain.ErrLinkNotFound）。
//	2. 逐字段应用"非 nil 才改"：
//	   - OriginalURL：同样要 isSafeTarget 校验。
//	   - ShortCode：校验形状 + shortCodeTaken(..., 排除自己)；
//	     **改短码意味着旧短码要失效**，记下 oldCode。
//	   - Password：空字符串表示"取消密码"（哈希置 NULL），非空则重新哈希。
//	   - ExpiresAt：解析 RFC3339。
//	   - IsActive / FolderID / WorkspaceID。
//	3. 用 Updates(map[string]any{...}) 落库。
//	   ⚠ 可空字段要清空时必须写 gorm.Expr("NULL") 或直接给 nil 值配合 Select，
//	     因为 GORM 的 Updates(struct) 会忽略零值——`Updates(Link{FolderID: nil})`
//	     永远不会把 folder_id 改回 NULL。原项目为此专门写过测试。
//	4. 标签：先删 link_tags 里这条链接的旧记录，再 attachOwnedTags。
//	5. 缓存：invalidate 旧短码和新短码（两个都要！改名前后的入口都不能留着旧数据）。
//	6. 返回 toLinkInfo(...)。
//
// 验收：go test ./internal/service/ -run TestUpdateLink
func (s *LinkService) Update(ctx context.Context, linkID, userID int64, req domain.UpdateLinkRequest) (*domain.LinkInfo, error) {
	return nil, todo.Err("LinkService.Update")
}

// Delete 删除一条链接。
//
// TODO(练习 32)：实现它。
//
//  1. 先按 id + user_id 取出 short_code（还要用这个短码去失效缓存）。
//  2. Delete：同样把 user_id 放进 WHERE，而不是"先查出来再判断"。
//  3. invalidate(ctx, shortCode)。
//  4. click_logs 会随外键 CASCADE 一起走（见 entity 里的约束）。
//
// 验收：go test ./internal/service/ -run TestDeleteLink
func (s *LinkService) Delete(ctx context.Context, linkID, userID int64) error {
	return todo.Err("LinkService.Delete")
}

// BatchDelete 批量删除，返回实际删掉的行数。
//
// TODO(练习 33)：实现它。
//
//  1. ids 为空 → 直接返回 0, nil（不要生成 `id IN ()` 这种非法 SQL）。
//  2. **先查 short_code 再删**：删完就查不到短码了，而缓存必须失效。
//     查询：Where("id IN ? AND user_id = ?", ids, userID).Pluck("short_code", &codes)
//  3. Where("id IN ? AND user_id = ?", ids, userID).Delete(&entity.Link{})，
//     返回 RowsAffected。
//  4. 逐个 invalidate。
//
// 注意 GORM 的 `IN ?` 写法（问号前不加括号）：它会正确展开成 ($1,$2,$3)。
//
// 验收：go test ./internal/service/ -run TestBatchDelete
func (s *LinkService) BatchDelete(ctx context.Context, ids []int64, userID int64) (int64, error) {
	return 0, todo.Err("LinkService.BatchDelete")
}

// BatchTag 给多条链接批量打同一个标签。
//
// TODO(练习 34)：实现它。
//
//  1. 先确认这个标签属于当前用户（否则就是把别人的标签挂到自己的链接上）。
//  2. 再确认这些链接都属于当前用户（用一次 Count 判断数量是否相等即可）。
//  3. 批量插入 link_tags，冲突忽略：
//     tx.Exec("INSERT INTO link_tags (link_id, tag_id) VALUES ? ON CONFLICT DO NOTHING", ...)
//     或写成循环 + clause.OnConflict{DoNothing: true}。
//     注意 link_tags 的主键是 (link_id, tag_id)，所以"重复打标签"天然被拒。
//
// 验收：go test ./internal/service/ -run TestBatchTag
func (s *LinkService) BatchTag(ctx context.Context, ids []int64, tagID int64, userID int64) error {
	return todo.Err("LinkService.BatchTag")
}

// LogClick 记录一次点击。
//
// TODO(练习 35)：实现它。这个函数**没有返回值**，那正是重点：
// 跳转的响应绝不能因为"点击没记上"而失败或变慢。
//
//	1. 造 event_id：randomID()（失败就用一个兜底值，绝不能因为随机数失败而丢掉点击）。
//	2. 组装 mq.ClickEvent（字段来自 domain.ClickEvent，加上
//	   EventID 与 CreatedAt: time.Now().UTC()）。
//	3. s.kafka != nil → PublishClick。**必须用 defer/recover 包住这次调用**：
//	   这个函数是在 handler 起的 goroutine 里跑的，而 Gin 的 recovery 只覆盖
//	   "服务这个请求的那个 goroutine"。这里 panic 会直接结束进程——
//	   原项目正是这样挂过：每次点击都调用一个 nil 接收者的方法 → panic → 进程退出 →
//	   systemd 重启 → 那几秒的请求全部 502，而且**点击丢了**（降级路径根本没跑到）。
//	4. publish 成功 → return。
//	5. publish 失败或 panic → 把 panic 转成 error，走同一条降级路径：
//	   s.clickWriter.WriteClick(ctx, event)。失败只 log。
//	6. ⚠ 降级和 Kafka 必须用**同一个 event_id**：worker 和直写可能同时到达，
//	   event_id 的 ON CONFLICT 是它们唯一不会各写一行的保证。
//
// 验收：go test ./internal/service/ -run TestLogClick
func (s *LinkService) LogClick(ctx context.Context, e domain.ClickEvent) {
	todo.Stub("LinkService.LogClick")
}

// ==================== 内部方法（要你填的部分） ====================

// loadLink 从数据库按短码读一条链接。
//
// TODO(练习 36)：实现它。**这个函数的错误分类决定了否定缓存是否安全**：
//
//  1. s.db.WithContext(ctx).Where("short_code = ?", shortCode).First(&link)
//  2. 查不到（isNotFound(err)）→ 返回 domain.ErrLinkNotFound
//  3. 其他错误 → 用 fmt.Errorf("...: %w", err) 包一层返回
//     （**不要**返回 ErrLinkNotFound！调用方靠这个区分"不存在"和"查不动"，
//      把两者混在一起就等于让一次数据库故障变成"所有短码都不存在"。）
//  4. 查到但 isLinkUsable 为假（停用/过期）→ 返回 domain.ErrLinkNotFound
//     （对调用方来说，"停用了"和"不存在"是同一个答案。）
//
// 验收：go test ./internal/service/ -run TestLoadLinkErrorKinds
func (s *LinkService) loadLink(ctx context.Context, shortCode string) (*domain.LinkInfo, error) {
	return nil, todo.Err("LinkService.loadLink")
}

// cacheLink 把链接写进缓存，并清掉它的否定缓存。
//
// TODO(练习 37)：实现它。三件事，顺序有意义：
//  1. s.cache == nil → 直接返回。
//  2. ClearMissing（先清否定缓存，再写正向缓存：反过来的话，
//     ClearMissing 会把刚写进去的正向缓存一起删掉——它们是两个键，但顺序错了就白写）。
//  3. SetLink。
func (s *LinkService) cacheLink(ctx context.Context, info *domain.LinkInfo) {
	todo.Stub("LinkService.cacheLink")
}

// markMissing 记住"这个短码不存在"。
//
// TODO(练习 38)：实现它。s.cache == nil → 直接返回；否则 MarkMissing。
func (s *LinkService) markMissing(ctx context.Context, shortCode string) {
	todo.Stub("LinkService.markMissing")
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
//
// TODO(练习 39)：实现它。
//  1. 重新按 id + user_id 查一次行（连同 LEFT JOIN folders 拿 folder_name）。
//     提示：可以复用 linkListRow 这个扫描目标。
//  2. linkInfoFromEntity 得到基础信息。
//  3. tagsForLinks(ctx, userID, []int64{linkID}) 补标签。
//
// 验收：go test ./internal/service/ -run TestToLinkInfo
func (s *LinkService) toLinkInfo(ctx context.Context, linkID, userID int64) (*domain.LinkInfo, error) {
	return nil, todo.Err("LinkService.toLinkInfo")
}

// tagsForLinks 一次取回多条链接的标签，避免 N+1。
//
// TODO(练习 40)：实现它。
//
//	SELECT lt.link_id, t.id, t.name, t.color
//	  FROM link_tags AS lt
//	  JOIN tags AS t ON t.id = lt.tag_id
//	 WHERE lt.link_id IN ? AND t.user_id = ?
//
//	用 s.db.WithContext(ctx).Raw(sql, ids, userID).Scan(&rows) 之类的写法，
//	然后聚合成 map[int64][]domain.LinkTagInfo。
//	（注意 t.user_id 这个条件也要有：标签是用户的，不是链接的。）
func (s *LinkService) tagsForLinks(ctx context.Context, userID int64, linkIDs []int64) (map[int64][]domain.LinkTagInfo, error) {
	return nil, todo.Err("LinkService.tagsForLinks")
}

// generateShortCode 生成一个没被占用的短码。
//
// TODO(练习 41)：实现它。
//  1. 字符表：62 个字符（0-9a-zA-Z）——大小写敏感让短码短而容量大。
//  2. 长度 7（62^7 ≈ 3.5 万亿，碰撞概率可忽略，但仍要检查）。
//  3. 用 crypto/rand（**不是 math/rand**：短码可预测意味着别人能猜到还没人用的短码，
//     从而"抢占"将来某个人的短码）。
//     提示：rand.Int(rand.Reader, big.NewInt(62)) 取一个下标。
//  4. 生成后 shortCodeTaken 检查一次，撞了就重试（设一个重试上限，例如 5 次，
//     超了返回错误而不是死循环）。
//
// 验收：go test ./internal/service/ -run TestGenerateShortCode
func (s *LinkService) generateShortCode() (string, error) {
	return "", todo.Err("LinkService.generateShortCode")
}

// shortCodeTaken 报告短码是否已被占用。
//
// TODO(练习 42)：实现它。exceptID 是"改名时排除自己"用的：
// 把一条链接的短码从 a 改成 a（等于没改）不该被判成冲突。
//
//	SELECT count(*) FROM links WHERE short_code = ? AND id <> ?
//	用 Model(&entity.Link{}).Where(...).Count(&n) 即可；出错时返回 true
//	（宁可误判"被占用"而报一个可读的错，也不要因为查询失败而放行一个冲突的短码）。
func (s *LinkService) shortCodeTaken(ctx context.Context, shortCode string, exceptID int64) bool {
	return todo.Bool("LinkService.shortCodeTaken")
}

// validateOwnedRefs 确认 folder_id / workspace_id 属于这个用户。
//
// TODO(练习 43)：实现它。两个指针都可能为 nil（表示"不设置"）。
//
//	对每个非 nil 的 id：
//	  SELECT count(*) FROM folders WHERE id = ? AND user_id = ?
//	  count == 0 → 返回 errors.New("folder not found")
//	工作区同理。
//
// 这一步是"客户端传来的 id"和"数据库里的归属"之间唯一的桥梁。
func (s *LinkService) validateOwnedRefs(ctx context.Context, userID int64, folderID, workspaceID *int64) error {
	return todo.Err("LinkService.validateOwnedRefs")
}

// attachOwnedTags 把标签挂到链接上（只挂这个用户自己的标签）。
//
// TODO(练习 44)：实现它。
//  1. tagIDs 为空 → 直接返回。
//  2. 先查出这些 id 里**确实属于该用户**的标签：
//     Model(&entity.Tag{}).Where("id IN ? AND user_id = ?", tagIDs, userID).Pluck("id", &owned)
//  3. 逐个插入 link_tags（用 clause.OnConflict{DoNothing: true} 或
//     "ON CONFLICT DO NOTHING" 让重复打标签成为无害操作）。
//  4. 忽略不属于该用户的 id，不要报错——更不要把别人的标签挂上去。
func (s *LinkService) attachOwnedTags(ctx context.Context, userID, linkID int64, tagIDs []int64) {
	todo.Stub("LinkService.attachOwnedTags")
}

// ==================== 已给的辅助函数 ====================

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
// TODO(练习 45)：实现它。这是"密码哈希绝不能离开数据库"的那道闸门：
//
//  1. HasPassword = (l.PasswordHash != nil) —— 只发布"有没有密码"这个事实。
//  2. 其余字段直接搬运；ShortURL = s.BuildShortURL(l.Domain, l.ShortCode)。
//     ⚠ 注意这是个方法而不是自由函数：它需要 s.baseURL。写成方法可以让测试
//       构造一个只填了 baseURL 的 LinkService（不需要数据库）。
//  3. Tags 初始化为空切片而不是 nil：JSON 里 `null` 和 `[]` 对前端是两种东西，
//     而"没有标签"应该序列化成 []。原项目在 LinkInfo 上有过这条约定。
//
// 验收：go test ./internal/service/ -run TestLinkInfoFromEntityHidesPasswordHash
func (s *LinkService) linkInfoFromEntity(l *entity.Link) *domain.LinkInfo {
	todo.Stub("LinkService.linkInfoFromEntity")
	return nil
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
// TODO(练习 46)：实现它。只允许 http / https 两种 scheme。
//
//	提示：用 net/url 的 url.Parse，然后检查 u.Scheme 是否为 "http" 或 "https"，
//	并且 u.Host 不为空。**不要**用 strings.HasPrefix 判断——
//	`HTTP://evil.com`、`\tjavascript:`、`java\nscript:` 这类变体就是靠
//	"看起来像前缀"绕过检查的，而 url.Parse 会先把它们规范化。
//
//	为什么这条检查放在**写入时**（Create/Update）而不是跳转时？
//	原项目两边都做了（跳转时是防历史脏数据）。MVP 先做写入时的；
//	想在跳转路径也加一层的话，redirect handler 里也调它一次即可。
//
// 验收：go test ./internal/service/ -run TestIsSafeTarget
func isSafeTarget(raw string) bool {
	// 这行只是为了让 fmt 的 import 不报错（实现里写错误信息会用到），写完删掉：
	_ = fmt.Sprintf
	return todo.Bool("isSafeTarget")
}
