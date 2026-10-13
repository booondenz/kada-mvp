package service

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/todo"
)

// AnalyticsService 回答"我的链接一共被点了多少次、什么时候点的、从哪里点的"。
//
// 对照原项目：kada/internal/service/analytics_service.go
//
// 这个包最重要的原则只有一条：**只有 kind = 'visit' 的行才算一次点击**。
// links.click_count、平台分布、每日趋势、访客列表——每一处聚合都必须过滤同一个 kind，
// 否则两个页面会给出互相矛盾的数字，而这种矛盾极难被发现（数字看起来都"差不多对"）。
type AnalyticsService struct {
	db *gorm.DB
}

// NewAnalyticsService 构造分析服务（已给）。
func NewAnalyticsService(db *gorm.DB) *AnalyticsService {
	return &AnalyticsService{db: db}
}

// Overview 汇总一个用户的链接数和点击数。
//
// TODO(练习 47)：实现它。
//
// 查询：
//
//	SELECT COUNT(*) AS total_links, COALESCE(SUM(click_count), 0) AS total_clicks
//	  FROM links WHERE user_id = ?
//
// 两种写法都行：
//
//	s.db.WithContext(ctx).Model(&entity.Link{}).
//	    Select("COUNT(*) AS total_links, COALESCE(SUM(click_count), 0) AS total_clicks").
//	    Where("user_id = ?", userID).Find(&out)
//	// 或者直接手写 SQL：
//	s.db.WithContext(ctx).Raw(`SELECT ... FROM links WHERE user_id = ?`, userID).Find(&out)
//
// ⚠ 用 Find，不要用 Scan：两者在真实数据库上执行的 SQL 完全一样，但 Scan 走的是 sql.Rows
// 那条路，而 DryRun 会话不支持它（gorm v1.31 的 Scan/Rows 会直接返回 ErrDryRunModeUnsupported）。
// 验收测试全部跑在 DryRun 上，所以用 Scan 会得到一个和你的 SQL 毫无关系的错误。
//
// 两个细节：
//   - **COALESCE(..., 0)**：一个还没有链接的用户，SUM 返回的是 NULL，
//     而它会被扫描成 0 还是报错，取决于驱动和字段类型。显式写出来就不用赌。
//   - **WHERE user_id**：归属条件在 SQL 里，不是在拿到结果之后过滤。
//     这不是安全上的偏好，而是"根本不去读别人的行"。
//
// 提示：Overview 类型定义在 internal/domain（domain.Overview）。
//
// 验收：go test ./internal/service/ -run TestOverview
func (s *AnalyticsService) Overview(ctx context.Context, userID int64) (domain.Overview, error) {
	return domain.Overview{}, todo.Err("AnalyticsService.Overview")
}

// DailyTrend 返回最近 days 天的每日点击数（没有点击的日期也要出现，值为 0）。
//
// TODO(练习 48)：实现它。
//
// 朴素查询（先写对它，再想优化）：
//
//	SELECT to_char(cl.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD') AS day,
//	       COUNT(*) AS clicks
//	  FROM click_logs AS cl
//	  JOIN links AS l ON l.id = cl.link_id
//	 WHERE l.user_id = ? AND cl.kind = 'visit' AND cl.created_at >= ?
//	 GROUP BY day
//	 ORDER BY day
//
// 要点：
//  1. **JOIN links 是必须的**：click_logs 里没有 user_id，
//     "我的点击"只能通过"我的链接"来表达。忘了这个 JOIN 就会返回全站的数字。
//  2. `cl.kind = 'visit'`：预取请求（kind='request'）不能算进趋势图。
//  3. `AT TIME ZONE 'UTC'`：库里是 timestamptz，按 UTC 切天才不会因为服务器时区而错位。
//  4. 结果要**补齐空缺的日期**：SQL 只会返回有点击的那些天，
//     而趋势图缺了几天会变成一条错误的折线（把 3 天前和 10 天前画成相邻的两点）。
//     在 Go 里用 map[day]clicks 补全最近 days 天，从今天往前推。
//  5. days 要做上限保护（例如 <= 0 用 30，> 365 用 365）。
//
// 验收：go test ./internal/service/ -run TestDailyTrend
func (s *AnalyticsService) DailyTrend(ctx context.Context, userID int64, days int) ([]domain.TrendPoint, error) {
	return nil, todo.Err("AnalyticsService.DailyTrend")
}

// PlatformBreakdown 返回按平台分组的点击数。
//
// TODO(练习 49)：实现它。
//
//	SELECT COALESCE(cl.platform, 'unknown') AS platform, COUNT(*) AS clicks
//	  FROM click_logs AS cl
//	  JOIN links AS l ON l.id = cl.link_id
//	 WHERE l.user_id = ? AND cl.kind = 'visit'
//	 GROUP BY platform
//	 ORDER BY clicks DESC
//
// 要点：
//  1. platform 列是可空的（老数据、无法识别的 UA），COALESCE 让它们归到 "unknown"
//     而不是从结果里"消失"——消失的平台会让人以为没有这类流量。
//  2. 只返回出现过的平台。前端要展示固定几种平台时，由前端补零；
//     也可以在这里用 domain.KnownPlatforms 补齐（已给，方便你想这么做时直接用）。
//
// 验收：go test ./internal/service/ -run TestPlatformBreakdown
func (s *AnalyticsService) PlatformBreakdown(ctx context.Context, userID int64) ([]domain.PlatformCount, error) {
	return nil, todo.Err("AnalyticsService.PlatformBreakdown")
}

// compile-time 引用，避免 fmt / time 在填空阶段被误报为未使用（写完删掉）。
var (
	_ = fmt.Sprintf
	_ = time.Now
)
