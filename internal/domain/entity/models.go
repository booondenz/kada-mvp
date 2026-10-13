// Package entity 里的结构体就是数据库表结构本身。
//
// 对照原项目：kada/internal/domain/entity/models.go
//
// 关键决定（原项目踩过坑，所以这里照抄）：
//   - 没有 SQL 迁移文件。GORM 的 AutoMigrate 拿这些结构体对比数据库，只补缺失的表/列/索引，
//     永远不删东西，因此可以安全地重复执行。
//   - 所以"结构体"是 schema 的唯一事实来源，改了字段就等于改了表。
//   - Models() 的顺序有讲究：PostgreSQL 不能给还不存在的表加外键，
//     被引用的表必须排在引用它的表前面。
//
// 这一份是**已给**的：字段和约束就是填空题的题干，不需要你改（除非你想加字段）。
package entity

import (
	"time"

	"gorm.io/gorm"
)

// User 是账号：手机号 + 密码。
//
// 手机号在注册时用短信验证码验证一次，之后就用密码登录——短信要花钱，
// 而一个账号被登录的次数远多于被创建的次数。
type User struct {
	ID int64 `gorm:"primaryKey" json:"id"`
	// uniqueIndex：一个手机号只能有一个账号，这条约束也是"并发注册同一号码"的最后一道防线。
	Phone string `gorm:"type:varchar(20);uniqueIndex;not null" json:"phone"`
	// PasswordHash 存 bcrypt 结果。json:"-" 让它在任何 API 响应里都不会出现——
	// 这类"忘了排除"的字段是最常见的泄露来源。
	PasswordHash *string    `gorm:"type:varchar(255)" json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	LastLoginAt  *time.Time `json:"last_login_at"`
	LastLoginIP  *string    `gorm:"type:varchar(45)" json:"last_login_ip"`
}

// Link 是一条短链接。
type Link struct {
	ID int64 `gorm:"primaryKey" json:"id"`
	// ShortCode 是 /r/:code 里的 code。unique 是必需的：两行同一个 code，
	// 跳转就变成了"看数据库心情"。
	ShortCode    string     `gorm:"type:varchar(20);uniqueIndex;not null" json:"short_code"`
	OriginalURL  string     `gorm:"type:text;not null" json:"original_url"`
	Title        *string    `gorm:"type:varchar(500)" json:"title"`
	Domain       string     `gorm:"type:varchar(255);default:kada.link" json:"domain"`
	PasswordHash *string    `gorm:"type:varchar(255)" json:"-"` // 访问密码，bcrypt
	ExpiresAt    *time.Time `json:"expires_at"`
	IsActive     bool       `gorm:"not null;default:true" json:"is_active"`
	// ClickCount 是"已确认的访问"计数，只由 kind=visit 的点击累加（见 ClickLog.Kind）。
	ClickCount  int64     `gorm:"not null;default:0" json:"click_count"`
	UserID      int64     `gorm:"index;not null" json:"user_id"`
	FolderID    *int64    `gorm:"index" json:"folder_id"`
	WorkspaceID *int64    `gorm:"index" json:"workspace_id"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// 关联。constraint:OnDelete 决定"父行被删时子行怎么办"：
	//   - 账号被删 → 它的链接一起删（CASCADE）。
	//     原项目这里是 SET NULL（公开短链不能因为账号注销就失效），MVP 选了更简单的语义，
	//     改回 SET NULL 只需要把 UserID 改成 *int64 并把约束换掉。
	//   - 文件夹/工作区被删 → 只把链接摘出来（SET NULL），链接本身留着。
	User      *User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
	Folder    *Folder    `gorm:"foreignKey:FolderID;constraint:OnDelete:SET NULL" json:"-"`
	Workspace *Workspace `gorm:"foreignKey:WorkspaceID;constraint:OnDelete:SET NULL" json:"-"`
}

// ClickLog 是一次点击的原始记录：一条请求一行，绝不覆盖、绝不合并。
type ClickLog struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	LinkID    int64     `gorm:"index;not null" json:"link_id"`
	IP        *string   `gorm:"type:varchar(45)" json:"ip"` // 45 = IPv6 最长
	UserAgent *string   `gorm:"type:text" json:"user_agent"`
	Platform  *string   `gorm:"type:varchar(20);index" json:"platform"` // domain.Platform
	Kind      string    `gorm:"type:varchar(16);not null;default:visit;index" json:"kind"`
	Referer   *string   `gorm:"type:text" json:"referer"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
	// EventID 是生产端生成的幂等键。Kafka 是 at-least-once，
	// 有了这个唯一索引，"重复投递"就撞 ON CONFLICT DO NOTHING 而不是把一次点击算两遍。
	EventID string `gorm:"type:varchar(64);not null;uniqueIndex" json:"event_id"`

	Link *Link `gorm:"foreignKey:LinkID;constraint:OnDelete:CASCADE" json:"-"`
}

// SMSVerificationCode 是一条待用的短信验证码。
//
// 只存 sha256(code)，明文既不入库也不打日志：一次性凭证的明文没有任何理由被持久化。
type SMSVerificationCode struct {
	ID    int64  `gorm:"primaryKey" json:"id"`
	Phone string `gorm:"type:varchar(20);not null;index:idx_sms_codes_phone,priority:1" json:"phone"`
	// CodeHash = sha256(code)，64 个十六进制字符。
	CodeHash *string `gorm:"type:varchar(64)" json:"-"`
	// IP 有索引：按 IP 统计发送次数（每 IP 每小时/每天的配额）就靠它，而那个查询发生在每次发送之前。
	IP        *string   `gorm:"type:varchar(45);index" json:"ip"`
	Used      bool      `gorm:"not null;default:false" json:"used"`
	Attempts  int       `gorm:"not null;default:0" json:"attempts"`
	ExpiresAt time.Time `gorm:"not null" json:"expires_at"`
	CreatedAt time.Time `gorm:"index:idx_sms_codes_phone,priority:2" json:"created_at"`
}

// TableName 钉死表名：GORM 默认复数化会得到 sms_verification_codes，
// 而我们想要 sms_codes（原项目的表名，也是这里唯一被改名的表）。
func (SMSVerificationCode) TableName() string { return "sms_codes" }

// LoginCaptcha 是"发短信之前必须先答对"的图形验证码。
//
// 它存在 PostgreSQL 而不是 Redis，这是个刻意的选择：Redis 在本项目里是可选的
// （挂了就跳过限流、跳过缓存），而一个"悄悄不再生效"的验证码比没有验证码更糟——
// 接口看起来还是受保护的。数据库不是可选的。
//
// 同样只存 sha256(答案的大写形式)。
type LoginCaptcha struct {
	// ID 是 32 个十六进制字符（crypto/rand），不是 UUID：这里不需要版本位。
	ID        string    `gorm:"type:varchar(32);primaryKey" json:"id"`
	CodeHash  string    `gorm:"type:varchar(64);not null" json:"-"`
	IP        *string   `gorm:"type:varchar(45)" json:"ip"`
	Used      bool      `gorm:"not null;default:false" json:"used"`
	Attempts  int       `gorm:"not null;default:0" json:"attempts"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func (LoginCaptcha) TableName() string { return "login_captchas" }

// Folder 把用户的链接分组。
type Folder struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	UserID    int64     `gorm:"not null;index" json:"user_id"`
	Name      string    `gorm:"type:varchar(100);not null" json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	User *User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

// Tag 给链接打标签，多对多经过 link_tags。
type Tag struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	UserID    int64     `gorm:"not null;index" json:"user_id"`
	Name      string    `gorm:"type:varchar(50);not null" json:"name"`
	Color     string    `gorm:"type:varchar(7);default:#6366F1" json:"color"`
	CreatedAt time.Time `json:"created_at"`

	User *User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`
}

// LinkTag 是 link <-> tag 的中间表：两个字段组成复合主键，
// 天然保证"同一条链接不会被打两次同一个标签"。
type LinkTag struct {
	LinkID int64 `gorm:"primaryKey" json:"link_id"`
	TagID  int64 `gorm:"primaryKey" json:"tag_id"`

	Link *Link `gorm:"foreignKey:LinkID;constraint:OnDelete:CASCADE" json:"-"`
	Tag  *Tag  `gorm:"foreignKey:TagID;constraint:OnDelete:CASCADE" json:"-"`
}

func (LinkTag) TableName() string { return "link_tags" }

// Workspace 把链接归属到一个团队/项目，靠 slug 对外标识。
type Workspace struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"type:varchar(100);not null" json:"name"`
	Slug      string    `gorm:"type:varchar(50);not null;uniqueIndex" json:"slug"`
	UserID    int64     `gorm:"not null;index" json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// 原项目这里没有 ON DELETE 子句：还有链接挂在工作区上时，删账号会被数据库拒绝。
	// 这里保留默认行为（NO ACTION），和原项目一致。
	User *User `gorm:"foreignKey:UserID" json:"-"`
}

// Models 返回全部实体，顺序是稳定的、依赖在前的。
//
// AutoMigrate 按这个顺序建表：users → workspaces/folders/tags → links → link_tags/click_logs。
// 少写一个模型 = 那张表永远不会被创建，而且要到运行时报错才会发现。
func Models() []any {
	return []any{
		&User{},
		&Workspace{},
		&Folder{},
		&Tag{},
		&Link{},
		&LinkTag{},
		&ClickLog{},
		&SMSVerificationCode{},
		&LoginCaptcha{},
	}
}

// BeforeCreate 补齐结构体上的零值默认。
//
// 为什么不指望 GORM 的 `default:xxx` 标签？因为标签生成的默认值只在 INSERT 语句里生效，
// 而 Go 这边的零值和"调用方没填"是同一个值：`IsActive` 的一个"故意的 false"
// 会被 `default:true` 重新写成 true。所以在服务层显式赋值、在这里兜底。
func (l *Link) BeforeCreate(*gorm.DB) error {
	if l.Domain == "" {
		l.Domain = "kada.link"
	}
	return nil
}

// BeforeCreate 给 Tag 补默认颜色。
func (t *Tag) BeforeCreate(*gorm.DB) error {
	if t.Color == "" {
		t.Color = "#6366F1"
	}
	return nil
}
