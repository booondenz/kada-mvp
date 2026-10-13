package service

import (
	"context"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/mq"
	"kada-mvp/internal/todo"
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
// TODO(练习 25)：实现它。这段代码不长，但每一行都在防一个具体的问题：
//
//  1. kind 归一化：event.Kind 为空 → 当成 domain.ClickKind(domain.ClickRequest)。
//     为什么不是 visit？因为"说不清它是不是一个人打开的"是唯一**绝不能计数**的答案。
//     空值只可能来自旧版生产端，而旧版并不知道 kind 这回事。
//
//  2. 开事务：s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error { ... })
//     插入和累加必须在同一个事务里，否则会出现"日志写了但计数没加"的中间状态。
//
//  3. 插入用**原生 SQL**，因为幂等性就写在这一句里：
//
//     INSERT INTO click_logs (link_id, ip, user_agent, platform, kind, referer, created_at, event_id)
//     VALUES (?, ?, ?, ?, ?, ?, ?, ?)
//     ON CONFLICT (event_id) DO NOTHING
//
//     Kafka 是 at-least-once：同一条消息可能被投递两次，降级直写也可能和 worker 撞车。
//     event_id 上的唯一索引 + ON CONFLICT DO NOTHING 让第二次插入变成"什么都没发生"，
//     这就是"点击计数必须准确"的全部依靠。
//
//  4. res.RowsAffected == 0 → 说明这条事件已经写过了 → return nil（提交，但**不要**再加计数）。
//
//  5. kind != domain.ClickVisit → return nil（这一行记下来了，但不动计数器）。
//     为什么要记？因为"有多少请求是预取"本身是有价值的数据；
//     为什么不动计数器？因为 links.click_count 和分析接口必须对"一次点击"有同一个定义，
//     而不是一个过滤、另一个不过滤。
//
//  6. 最后 UPDATE links SET click_count = click_count + 1 WHERE id = ?。
//
// 用到的参数（按列顺序，共 8 个占位符）：
//
//	event.LinkID,
//	nullable(event.IP), nullable(event.UserAgent), nullable(event.Platform),
//	string(kind), nullable(event.Referer), event.CreatedAt, event.EventID
//
// 提示：本文件的 nullable() 已给，用来把空字符串变成 NULL。
//
// 验收：go test ./internal/service/ -run TestWriteClick
func (s *ClickStore) WriteClick(ctx context.Context, event mq.ClickEvent) error {
	return todo.Err("ClickStore.WriteClick")
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

// 这个空引用是给你的实现准备的（domain.ClickKind / domain.ClickVisit 会用到）：
// 填空阶段它还没有被使用，用它保持编译通过。写完删掉。
var _ = domain.ClickVisit
