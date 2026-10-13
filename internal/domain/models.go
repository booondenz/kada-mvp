// Package domain 放"跨层的公共语言"：HTTP 请求/响应结构体、枚举、以及各层都要认识的哨兵错误。
//
// 对照原项目：kada/internal/domain/models.go
//
// 为什么 DTO 要和 entity 分开？entity 是数据库行长什么样，domain 是 API 收发什么。
// 两者合并的代价很具体：LinkInfo 会被序列化进 Redis 又会被序列化进 API 响应，
// 如果它就是 entity.Link，那么 PasswordHash 会同时进缓存和响应。
// 所以 LinkInfo 里根本没有能装密码哈希的字段，只有一个 HasPassword 布尔值。
//
// 这一份是**已给**的：字段和标签是填空题的题干，不用改。
package domain

import (
	"errors"
	"slices"
	"time"
)

// ErrLinkNotFound 表示这个短码没有对应的可用链接（不存在、已过期、已停用都算）。
//
// 为什么"过期"也复用这一个错误？因为对调用方来说答案一样：这条路走不通。
// 想把三者区分开（比如给用户不同的页面文案）时再拆，不要提前拆。
var ErrLinkNotFound = errors.New("link not found")

// RateLimitError 表示这次失败不是请求有问题，而是配额用完了。
//
// 用类型而不是哨兵，是因为调用方需要两样哨兵带不了的东西：给用户看的话，和要等多久。
// HTTP 层把它映射成 429 + Retry-After；其他一切失败都是调用方自己的问题，保持 400。
type RateLimitError struct {
	Message           string
	RetryAfterSeconds int
}

func (e *RateLimitError) Error() string { return e.Message }

// ErrInvalidCredentials 覆盖密码登录失败的全部三种情况：号码没注册、密码不对、账号还没有密码。
//
// 三种情况共用一个错误是刻意的：一旦能区分，这个接口就变成了"查询某个号码在本站有没有账号"
// 的工具，而且是对任何人开放、每分钟能问二十次的工具。
var ErrInvalidCredentials = errors.New("the phone number or password is incorrect")

// ErrAlreadyRegistered 表示这个号码已经有密码了。它单独成一个错误，因为调用方能据此行动：
// 账号存在，去登录。
var ErrAlreadyRegistered = errors.New("this phone number already has an account, please sign in")

// ==================== 认证 ====================

// SendSMSRequest 请求发送登录验证码。
//
// 验证码字段是 required 而不是可选：这道挑战的意义就是"没有人类答对过就不许触发短信"，
// 而一个可选的挑战，攻击者只要不发它就行了。
type SendSMSRequest struct {
	Phone       string `json:"phone" binding:"required"`
	CaptchaID   string `json:"captcha_id" binding:"required"`
	CaptchaCode string `json:"captcha_code" binding:"required"`
}

// CaptchaResponse 是图形挑战：一个不透明的 id（回传用），加一张 data URI 的图。
// 用 data URI 是为了让客户端不需要第二个请求、也不需要额外的图片接口。
type CaptchaResponse struct {
	ID    string `json:"captcha_id"`
	Image string `json:"image"`
}

// LoginByPhoneRequest 用手机号 + 验证码登录。
type LoginByPhoneRequest struct {
	Phone string `json:"phone" binding:"required"`
	Code  string `json:"code" binding:"required"`
}

// RegisterRequest 注册：验证码证明号码是本人的，密码是以后登录用的。
type RegisterRequest struct {
	Phone    string `json:"phone" binding:"required"`
	Code     string `json:"code" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// PasswordLoginRequest 用注册时设的密码登录。
type PasswordLoginRequest struct {
	Phone    string `json:"phone" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// AuthResponse 是登录/注册成功的响应。
type AuthResponse struct {
	Token string   `json:"token"`
	User  UserInfo `json:"user"`
}

// UserInfo 是 API 对外发布的账号信息：手机号，没有别的。
//
// 这个结构体会被前端存进 localStorage，所以它的形状值得被测试钉住。
type UserInfo struct {
	ID    int64  `json:"id"`
	Phone string `json:"phone"`
}

// ==================== 短链 ====================

// CreateLinkRequest 创建短链。
type CreateLinkRequest struct {
	OriginalURL string  `json:"original_url" binding:"required,url"`
	ShortCode   *string `json:"short_code"` // 不填就自动生成
	Title       *string `json:"title"`
	Domain      *string `json:"domain"`
	Password    *string `json:"password"`   // 访问密码，明文进来，出去只剩哈希
	ExpiresAt   *string `json:"expires_at"` // RFC3339；不填 = 永不过期
	FolderID    *int64  `json:"folder_id"`
	TagIDs      []int64 `json:"tag_ids"`
	WorkspaceID *int64  `json:"workspace_id"`
}

// UpdateLinkRequest 更新短链。全部字段都是指针：
// nil = "这次不改这个字段"，与 Go 零值区分开（否则 is_active 的 false 和"没传"没法区分）。
type UpdateLinkRequest struct {
	OriginalURL *string `json:"original_url"`
	ShortCode   *string `json:"short_code"`
	Title       *string `json:"title"`
	Domain      *string `json:"domain"`
	Password    *string `json:"password"`
	ExpiresAt   *string `json:"expires_at"`
	IsActive    *bool   `json:"is_active"`
	FolderID    *int64  `json:"folder_id"`
	TagIDs      []int64 `json:"tag_ids"`
	WorkspaceID *int64  `json:"workspace_id"`
}

// LinkTagInfo 是链接上附着的一个标签。
type LinkTagInfo struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// LinkInfo 是 API 和缓存看到的链接。它没有能装密码哈希的字段，只有 HasPassword。
type LinkInfo struct {
	ID          int64         `json:"id"`
	ShortCode   string        `json:"short_code"`
	ShortURL    string        `json:"short_url"`
	OriginalURL string        `json:"original_url"`
	Title       *string       `json:"title"`
	Domain      string        `json:"domain"`
	ClickCount  int64         `json:"click_count"`
	IsActive    bool          `json:"is_active"`
	ExpiresAt   *time.Time    `json:"expires_at"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	FolderID    *int64        `json:"folder_id"`
	FolderName  *string       `json:"folder_name"`
	WorkspaceID *int64        `json:"workspace_id"`
	Tags        []LinkTagInfo `json:"tags"`
	HasPassword bool          `json:"has_password"`
}

// PaginatedLinks 是列表接口的返回。
type PaginatedLinks struct {
	Links      []LinkInfo `json:"links"`
	TotalCount int64      `json:"total_count"`
	Page       int        `json:"page"`
	PageSize   int        `json:"page_size"`
}

// ==================== 平台识别 ====================

// Platform 是访问者来自哪个 App 的内置浏览器。
type Platform string

const (
	PlatformBrowser     Platform = "browser"
	PlatformWechat      Platform = "wechat"
	PlatformQQ          Platform = "qq"
	PlatformWeibo       Platform = "weibo"
	PlatformXiaohongshu Platform = "xiaohongshu"
	PlatformUnknown     Platform = "unknown"
)

// ==================== 点击事件 ====================

// ClickKind 说明这一行点击记录"是什么的证据"。
//
// 它存在的理由：**一次短链请求不等于一个人打开了它**。微信和 QQ 会在人点之前先抓一次链接
// 来生成预览卡片，而那个抓取器的 User-Agent 就是 App 内置浏览器的 User-Agent——
// 所以"是不是人"这个问题用 UA 过滤是答不出来的。区分办法是让页面自己来确认。
type ClickKind string

const (
	// ClickVisit 是"一个人打开了链接"的证据，也是唯一会让计数器加一的 kind。
	ClickVisit ClickKind = "visit"
	// ClickRequest 是一次不能证明有人的请求：UA 自称爬虫，或者页面被服务了但没人确认。
	// 记下来但不计数——预取的比例本身也是有用的数据。
	ClickRequest ClickKind = "request"
)

// ClickEvent 是一次点击在应用层的样子：HTTP 请求已经被分类，剩下的问题只有一个——
// 它算不算一次访问。
//
// 用结构体而不是参数列表：除了 LinkID 以外每个字段都是字符串，其中好几个还彼此相邻，
// 传错顺序照样能编译通过。
type ClickEvent struct {
	LinkID    int64
	IP        string
	UserAgent string
	Platform  Platform
	Kind      ClickKind
	Referer   string
}

// ==================== 文件夹 ====================

type Folder struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	LinkCount int64     `json:"link_count,omitempty"`
}

type CreateFolderRequest struct {
	Name string `json:"name" binding:"required"`
}

// ==================== 标签 ====================

type Tag struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateTagRequest struct {
	Name  string  `json:"name" binding:"required"`
	Color *string `json:"color"`
}

// ==================== 工作区 ====================

type Workspace struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	UserID    int64     `json:"user_id"`
	LinkCount int64     `json:"link_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateWorkspaceRequest struct {
	Name string `json:"name" binding:"required"`
	Slug string `json:"slug" binding:"required"`
}

type UpdateWorkspaceRequest struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}

// ==================== 分析 ====================

// Overview 是一个用户的总体数字。
type Overview struct {
	TotalLinks  int64 `json:"total_links"`
	TotalClicks int64 `json:"total_clicks"`
}

// TrendPoint 是每日趋势上的一个点。
type TrendPoint struct {
	Day    string `json:"day"` // YYYY-MM-DD（UTC）
	Clicks int64  `json:"clicks"`
}

// PlatformCount 是平台分布里的一项。
type PlatformCount struct {
	Platform string `json:"platform"`
	Clicks   int64  `json:"clicks"`
}

// KnownPlatforms 列出平台分布接口认识的全部平台，顺序稳定，方便测试与前端展示。
var KnownPlatforms = []Platform{
	PlatformBrowser, PlatformWechat, PlatformQQ, PlatformWeibo, PlatformXiaohongshu, PlatformUnknown,
}

// IsKnownPlatform 报告这个平台是否是应用定义的平台之一。
func IsKnownPlatform(p Platform) bool {
	return slices.Contains(KnownPlatforms, p)
}
