package service

import (
	"context"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/mq"
)

// ClickWriter 把一条点击事件写进库。
//
// 参数是整个事件而不是"一个字段一个参数"：事件的形状已经存在（生产端和消费端共用的
// mq.ClickEvent），而一个"每加一列就加一个参数"的签名，迟早会出现两个参数传反
// 而照样编译通过的情况。
type ClickWriter interface {
	WriteClick(ctx context.Context, event mq.ClickEvent) error
}

// ClickStore 是 ClickWriter 的 GORM 实现。
type ClickStore struct {
	db *gorm.DB
}

// NewClickStore 构造点击存储（已给）。
func NewClickStore(db *gorm.DB) *ClickStore {
	return &ClickStore{db: db}
}

// WriteClick 在一个事务里插入点击日志并按需累加计数器。
//
// 每一步都在防一个具体的问题：
//
//  1. kind 归一化：空值当成 request。为什么不是 visit？因为"说不清它是不是一个人打开的"
//     是唯一**绝不能计数**的答案——空值只可能来自旧版生产端，而旧版并不知道 kind 这回事。
//  2. 插入和累加必须在同一个事务里，否则会出现"日志写了但计数没加"的中间状态。
//  3. 幂等性写在 INSERT 这一句里：Kafka 是 at-least-once，同一条消息可能被投递两次，
//     降级直写也可能和 worker 撞车。event_id 上的唯一索引 + ON CONFLICT DO NOTHING
//     让第二次插入变成"什么都没发生"，这就是"点击计数必须准确"的全部依靠。
//  4. RowsAffected == 0 → 这条事件已经写过了 → 提交，但**不要**再加计数。
//  5. 非 visit 的行要记下来（"有多少请求是预取"本身是有价值的数据），但不动计数器：
//     links.click_count 和分析接口必须对"一次点击"有同一个定义。
//  6. 只有 visit 且是新插入的那一行，才 UPDATE links 的计数。
func (s *ClickStore) WriteClick(ctx context.Context, event mq.ClickEvent) error {
	kind := domain.ClickKind(event.Kind)
	if kind == "" {
		kind = domain.ClickRequest
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(
			`INSERT INTO click_logs (link_id, ip, user_agent, platform, kind, referer, created_at, event_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT (event_id) DO NOTHING`,
			event.LinkID,
			nullable(event.IP), nullable(event.UserAgent), nullable(event.Platform),
			string(kind), nullable(event.Referer), event.CreatedAt, event.EventID,
		)
		if res.Error != nil {
			// 插入失败必须让整个事务回滚：一次"日志没写成但计数加了"
			// 会让统计永远对不上。
			return res.Error
		}

		if res.RowsAffected == 0 {
			// 重复事件：已经写过了，安静地提交（不加计数）。
			return nil
		}

		if kind != domain.ClickVisit {
			// 预取/无法证明有人的请求：记下来，但计数器不动。
			return nil
		}

		return tx.Exec(
			`UPDATE links SET click_count = click_count + 1 WHERE id = ?`,
			event.LinkID,
		).Error
	})
}

// nullable 把空字符串变成 SQL NULL（已给）。
//
// 为什么不能存空串？因为"这一行有没有记录某个可选值"这件事，将来是用
// `column IS NOT NULL` 来问的，而一个空串会对所有这样的问题回答"有"。
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// 编译期断言：少实现一个方法就会在这里编译失败。
var _ ClickWriter = (*ClickStore)(nil)
