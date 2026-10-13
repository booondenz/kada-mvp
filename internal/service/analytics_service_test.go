package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"kada-mvp/internal/domain"
)

// AnalyticsService 的三条查询都是只读的，所以 DryRun 完全够用。
// 测试要盯的不是"数字算得对不对"（那需要真数据），而是**查询里有没有那三个
// 决定结果正确与否的东西**：归属条件、kind 过滤、以及 click_logs 到 links 的连接。

// TestOverview 对应练习 47。
func TestOverview(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	svc := NewAnalyticsService(db)

	if _, err := svc.Overview(context.Background(), 42); err != nil {
		t.Fatalf("Overview 返回错误: %v", err)
	}

	sql := strings.Join(*stmts, "\n")
	if sql == "" {
		t.Fatal("Overview 一条 SQL 都没有发出")
	}
	containsAll(t, sql, "count(*)", "coalesce", "sum(click_count)", "from links", "user_id")
}

// TestDailyTrend 对应练习 48。
func TestDailyTrend(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	svc := NewAnalyticsService(db)

	points, err := svc.DailyTrend(context.Background(), 42, 7)
	if err != nil {
		t.Fatalf("DailyTrend 返回错误: %v", err)
	}

	sql := strings.Join(*stmts, "\n")
	// join links 是必须的：click_logs 里没有 user_id，"我的点击"只能通过"我的链接"表达。
	// 少了这个 join，趋势图会把全站的数字画成你自己的。
	containsAll(t, sql, "click_logs", "join links", "user_id", "kind", "group by")

	// 空缺的日期必须补齐：SQL 只会返回有点击的那些天，
	// 而缺了几天会让折线把"3 天前"和"10 天前"画成相邻的两点。
	if len(points) != 7 {
		t.Errorf("返回了 %d 个点, want 7（没有点击的日期也要出现，值为 0）", len(points))
	}
	seen := map[string]bool{}
	for _, p := range points {
		if seen[p.Day] {
			t.Errorf("日期 %q 出现了两次", p.Day)
		}
		seen[p.Day] = true
		if _, err := time.Parse("2006-01-02", p.Day); err != nil {
			t.Errorf("日期 %q 不是 YYYY-MM-DD 格式: %v", p.Day, err)
		}
		if p.Clicks < 0 {
			t.Errorf("日期 %q 的点击数 = %d", p.Day, p.Clicks)
		}
	}
}

// days 参数是客户端传进来的，必须夹紧：一个 ?days=100000 的请求不该让
// 服务去生成十万个点。
func TestDailyTrendClampsDays(t *testing.T) {
	db := newDryRunDB(t)
	svc := NewAnalyticsService(db)
	ctx := context.Background()

	points, err := svc.DailyTrend(ctx, 42, 0)
	if err != nil {
		t.Fatalf("DailyTrend(days=0) 返回错误: %v", err)
	}
	if len(points) == 0 || len(points) > 365 {
		t.Errorf("days=0 时返回了 %d 个点，应该在 1~365 之间（用默认值兜底）", len(points))
	}

	points, err = svc.DailyTrend(ctx, 42, 100000)
	if err != nil {
		t.Fatalf("DailyTrend(days=100000) 返回错误: %v", err)
	}
	if len(points) > 365 {
		t.Errorf("days=100000 时返回了 %d 个点，必须夹到 365 以内", len(points))
	}
}

// TestPlatformBreakdown 对应练习 49。
func TestPlatformBreakdown(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	svc := NewAnalyticsService(db)

	if _, err := svc.PlatformBreakdown(context.Background(), 42); err != nil {
		t.Fatalf("PlatformBreakdown 返回错误: %v", err)
	}

	sql := strings.Join(*stmts, "\n")
	containsAll(t, sql, "click_logs", "join links", "user_id", "kind", "group by", "platform")
}

// 已给的枚举：平台名是报表里的公开词汇，改一个字就会让前端的映射悄悄失效。
func TestKnownPlatforms(t *testing.T) {
	if len(domain.KnownPlatforms) == 0 {
		t.Fatal("KnownPlatforms 是空的")
	}
	for _, p := range domain.KnownPlatforms {
		if !domain.IsKnownPlatform(p) {
			t.Errorf("IsKnownPlatform(%q) = false", p)
		}
	}
	if domain.IsKnownPlatform(domain.Platform("weixin")) {
		t.Error("IsKnownPlatform 接受了一个没有定义过的平台名")
	}
}
