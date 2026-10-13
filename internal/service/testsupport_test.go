package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/mq"
)

// newDryRunDB 返回一个只渲染 SQL、从不真的连库的 gorm.DB。
//
// 为什么这个项目大量用它？因为 GORM 把"手写 SQL"换成了"构造 SQL"，
// 于是要防的错误从"语句里打错字"变成了"渲染出语义微妙不同的语句"——
// 而 DryRun 恰好能让你断言"生成的 SQL 长什么样"，却不需要一个 PostgreSQL。
func newDryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{
		// 一个永远不会被拨号的 DSN：DryRun 只渲染 SQL。
		DSN: "postgres://kada:invalid@127.0.0.1:1/kada?sslmode=disable",
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatalf("构建 DryRun 的 gorm.DB 失败: %v", err)
	}
	return db
}

// newMissingRowDB 返回一个"每次查询都答'没有这一行'"的句柄，并统计查询次数。
//
// DryRun 会话永远不会走到 gorm.Scan，而 gorm.ErrRecordNotFound 正是在 Scan 里产生的，
// 所以"查不到"这个结果必须由回调注入。查询次数是测试真正要读的东西：
// 缓存是否真的挡住了数据库，只能靠它来证明。
func newMissingRowDB(t *testing.T, delay time.Duration) (*gorm.DB, *int64) {
	t.Helper()

	db := newDryRunDB(t)
	var queries int64

	err := db.Callback().Query().After("gorm:query").Register("test:count_as_missing", func(tx *gorm.DB) {
		atomic.AddInt64(&queries, 1)
		if delay > 0 {
			time.Sleep(delay) // 让并发的调用者有重叠的机会
		}
		_ = tx.AddError(gorm.ErrRecordNotFound)
	})
	if err != nil {
		t.Fatalf("注册查询回调失败: %v", err)
	}

	return db, &queries
}

// captureSQL 把每一条执行过的语句收进返回的切片，供测试断言。
//
// 记录的不只是 SQL 文本，还有**绑定值**（DryRun 下语句里是 $1/$2 占位符，
// 值在 Statement.Vars 里）。这样断言可以精确到"写进去的是哈希而不是明文"这种程度，
// 而不是只能检查"有没有出现过 code_hash 这个词"。
func captureSQL(t *testing.T, db *gorm.DB) *[]string {
	t.Helper()

	var mu sync.Mutex
	stmts := &[]string{}
	record := func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if sql == "" {
			return
		}
		mu.Lock()
		*stmts = append(*stmts, sql+" ||VARS|| "+fmt.Sprint(tx.Statement.Vars))
		mu.Unlock()
	}
	must := func(err error) {
		if err != nil {
			t.Fatalf("注册 SQL 捕获回调失败: %v", err)
		}
	}
	// 每个回调家族有自己的"最后一步"钩子名；GORM 的这几个类型没有导出，
	// 所以这里只能逐个写出来，不能抽成一个带类型参数的辅助函数。
	must(db.Callback().Query().After("gorm:query").Register("test:capture_query", record))
	must(db.Callback().Row().After("gorm:row").Register("test:capture_row", record))
	must(db.Callback().Raw().After("gorm:raw").Register("test:capture_raw", record))
	must(db.Callback().Create().After("gorm:create").Register("test:capture_create", record))
	must(db.Callback().Update().After("gorm:update").Register("test:capture_update", record))
	must(db.Callback().Delete().After("gorm:delete").Register("test:capture_delete", record))

	return stmts
}

// containsAll 对 SQL 做小写比较后，要求每一段都在里面。
//
// 比较前会去掉引号和反引号：GORM 的构造器渲染出的是 `FROM "links"`，
// 而手写 SQL 是 `FROM links`。测试要盯的是**语义**（有没有 JOIN links、有没有过滤 kind、
// 有没有把 user_id 放进 WHERE），不是引号风格。
func containsAll(t *testing.T, sql string, needles ...string) {
	t.Helper()
	strip := strings.NewReplacer(`"`, "", "`", "").Replace
	lower := strings.ToLower(strip(sql))
	for _, want := range needles {
		if !strings.Contains(lower, strings.ToLower(strip(want))) {
			t.Errorf("生成的 SQL 缺少 %q：\n%s", want, sql)
		}
	}
}

// ==================== 假对象 ====================

// fakeLinkCache 是内存版 LinkCache：跳转路径的测试不需要一个真 Redis。
type fakeLinkCache struct {
	mu            sync.Mutex
	links         map[string]*domain.LinkInfo
	missing       map[string]bool
	gets          int
	sets          int
	invalidations int
}

func newFakeLinkCache() *fakeLinkCache {
	return &fakeLinkCache{links: map[string]*domain.LinkInfo{}, missing: map[string]bool{}}
}

func (f *fakeLinkCache) GetLink(_ context.Context, shortCode string) (*domain.LinkInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	info, ok := f.links[shortCode]
	return info, ok
}

func (f *fakeLinkCache) SetLink(_ context.Context, info *domain.LinkInfo) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets++
	f.links[info.ShortCode] = info
	// 故意**不**清理 missing：真实的 CacheService.SetLink 也不清。
	// 清否定缓存是 LinkService.cacheLink 的职责，假对象如果"顺手"做了这件事，
	// 就会让一个漏掉 ClearMissing 的实现照样通过测试。
}

func (f *fakeLinkCache) InvalidateLink(_ context.Context, shortCode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidations++
	delete(f.links, shortCode)
	delete(f.missing, shortCode)
}

func (f *fakeLinkCache) MarkMissing(_ context.Context, shortCode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing[shortCode] = true
	delete(f.links, shortCode)
}

func (f *fakeLinkCache) ClearMissing(_ context.Context, shortCode string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.missing, shortCode)
}

func (f *fakeLinkCache) IsMissing(_ context.Context, shortCode string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.missing[shortCode]
}

func (f *fakeLinkCache) missingCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.missing)
}

// fakePublisher 是 mq.ClickPublisher 的假实现，可以指定"返回错误"或"直接 panic"。
type fakePublisher struct {
	mu      sync.Mutex
	events  []mq.ClickEvent
	err     error
	panicOn bool
	calls   int
}

func (f *fakePublisher) PublishClick(_ context.Context, e mq.ClickEvent) error {
	f.mu.Lock()
	f.calls++
	panicOn, err := f.panicOn, f.err
	f.mu.Unlock()

	if panicOn {
		panic("测试用的 publish panic")
	}
	if err != nil {
		return err
	}

	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
	return nil
}

func (f *fakePublisher) recorded() []mq.ClickEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mq.ClickEvent(nil), f.events...)
}

// fakeClickWriter 是 service.ClickWriter 的假实现（降级直写那条路）。
type fakeClickWriter struct {
	mu     sync.Mutex
	events []mq.ClickEvent
	err    error
}

func (f *fakeClickWriter) WriteClick(_ context.Context, e mq.ClickEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, e)
	return nil
}

func (f *fakeClickWriter) recorded() []mq.ClickEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mq.ClickEvent(nil), f.events...)
}
