package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
)

// TestGenerateShortCode 对应练习 41。
func TestGenerateShortCode(t *testing.T) {
	// shortCodeTaken 在 DryRun 下会得到"0 行"，也就是"没被占用"，于是这里只测生成本身。
	svc := &LinkService{db: newDryRunDB(t)}

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, err := svc.generateShortCode()
		if err != nil {
			t.Fatalf("第 %d 次生成失败: %v", i, err)
		}
		if len(code) != 7 {
			t.Fatalf("短码 %q 长度 = %d, want 7", code, len(code))
		}
		if !shortCodePattern.MatchString(code) {
			t.Fatalf("短码 %q 不符合 shortCodePattern（只能是字母数字和 -_，3~20 位）", code)
		}
		seen[code] = true
	}

	// 62^7 的空间里抽 200 个，碰撞基本不该发生。这条断言真正的目标是抓
	// "用了 math/rand 并且种子固定"这类错误：那种实现会连续吐出同一个短码。
	if len(seen) < 195 {
		t.Errorf("200 次生成只得到 %d 个不同的短码：随机性不足（用的是 crypto/rand 吗？）", len(seen))
	}
}

// TestIsSafeTarget 对应练习 46。
func TestIsSafeTarget(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"https", "https://example.com/a?b=1", true},
		{"http", "http://example.com", true},
		{"大小写混合的 scheme", "HTTPS://example.com", true},
		{"javascript 伪协议", "javascript:alert(1)", false},
		{"data URI", "data:text/html;base64,PHNjcmlwdD4=", false},
		{"file", "file:///etc/passwd", false},
		{"ftp", "ftp://example.com/x", false},
		{"没有 scheme", "example.com", false},
		{"空串", "", false},
		{"只有 scheme 没有主机", "https://", false},
		{"前面带空白的 javascript", "  javascript:alert(1)", false},
		{"前面带制表符的 javascript", "\tjavascript:alert(1)", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSafeTarget(tc.url); got != tc.want {
				t.Errorf("isSafeTarget(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

// TestLinkInfoFromEntityHidesPasswordHash 对应练习 45。
//
// 两条断言：字段映射对不对，以及**密码哈希有没有泄露**。
// 第二条用一个"绝不可能出现在正常输出里"的字符串去序列化整个结构体来验证——
// 这比逐个字段检查更可靠：将来谁往 LinkInfo 里加一个 PasswordHash 字段，这里就会红。
func TestLinkInfoFromEntityHidesPasswordHash(t *testing.T) {
	svc := &LinkService{baseURL: "https://kada.link"}

	hash := "$2a$10$THIS_MUST_NEVER_LEAVE_THE_DATABASE"
	link := &entity.Link{
		ID:           1,
		ShortCode:    "abc1234",
		OriginalURL:  "https://example.com/target",
		Domain:       "kada.link",
		PasswordHash: &hash,
		IsActive:     true,
		ClickCount:   3,
	}

	info := svc.linkInfoFromEntity(link)
	if info == nil {
		t.Fatal("linkInfoFromEntity 返回了 nil")
	}
	if !info.HasPassword {
		t.Error("HasPassword = false, want true（密码哈希非 nil）")
	}
	if info.ShortURL != "https://kada.link/r/abc1234" {
		t.Errorf("ShortURL = %q, want https://kada.link/r/abc1234", info.ShortURL)
	}
	if info.Tags == nil {
		t.Error("Tags 是 nil：JSON 里 null 和 [] 对前端是两种不同的东西，没有标签应该序列化成 []")
	}

	raw, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("序列化 LinkInfo 失败: %v", err)
	}
	if strings.Contains(string(raw), "THIS_MUST_NEVER") {
		t.Errorf("LinkInfo 把密码哈希泄露到了输出里：%s", raw)
	}
}

// 没有密码的链接：HasPassword 必须是 false。
func TestLinkInfoFromEntityWithoutPassword(t *testing.T) {
	svc := &LinkService{baseURL: "https://kada.link"}
	info := svc.linkInfoFromEntity(&entity.Link{ID: 1, ShortCode: "abc1234", Domain: "", IsActive: true})
	if info.HasPassword {
		t.Error("HasPassword = true, want false（没有密码哈希）")
	}
	if info.ShortURL != "https://kada.link/r/abc1234" {
		t.Errorf("ShortURL = %q, want 落回 baseURL 的那一支", info.ShortURL)
	}
}

// TestGetByCode 对应练习 28。
func TestGetByCode(t *testing.T) {
	t.Run("缓存命中时不查库", func(t *testing.T) {
		db, queries := newMissingRowDB(t, 0)
		cache := newFakeLinkCache()
		svc := &LinkService{db: db, cache: cache}

		ctx := context.Background()
		cache.SetLink(ctx, &domain.LinkInfo{
			ShortCode:   "abc1234",
			OriginalURL: "https://example.com/target",
			IsActive:    true,
		})

		info, err := svc.GetByCode(ctx, "abc1234")
		if err != nil {
			t.Fatalf("GetByCode 返回错误: %v", err)
		}
		if info.OriginalURL != "https://example.com/target" {
			t.Errorf("OriginalURL = %q, want 缓存里的目标地址", info.OriginalURL)
		}
		if got := atomic.LoadInt64(queries); got != 0 {
			t.Errorf("缓存命中却查了 %d 次数据库", got)
		}
	})

	t.Run("查不到时记住这个短码", func(t *testing.T) {
		db, queries := newMissingRowDB(t, 0)
		cache := newFakeLinkCache()
		svc := &LinkService{db: db, cache: cache}
		ctx := context.Background()

		for call := 1; call <= 3; call++ {
			if _, err := svc.GetByCode(ctx, "absent99"); !errors.Is(err, domain.ErrLinkNotFound) {
				t.Fatalf("第 %d 次调用返回 %v, want domain.ErrLinkNotFound", call, err)
			}
		}

		if got := atomic.LoadInt64(queries); got != 1 {
			t.Errorf("三次查询同一个不存在的短码，数据库被查了 %d 次, want 1（否定缓存没生效）", got)
		}
		if !cache.IsMissing(ctx, "absent99") {
			t.Error("没有把'这个短码不存在'记下来")
		}
	})

	// 并发问同一个不存在的短码，是缓存穿透最典型的形状：否定缓存要等第一次读**读完**才写得进去。
	t.Run("并发的未命中只查一次库", func(t *testing.T) {
		db, queries := newMissingRowDB(t, 50*time.Millisecond)
		cache := newFakeLinkCache()
		svc := &LinkService{db: db, cache: cache}
		ctx := context.Background()

		const callers = 8
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := svc.GetByCode(ctx, "absent99"); !errors.Is(err, domain.ErrLinkNotFound) {
					t.Errorf("并发查询返回 %v, want domain.ErrLinkNotFound", err)
				}
			}()
		}
		close(start)
		wg.Wait()

		if got := atomic.LoadInt64(queries); got != 1 {
			t.Errorf("%d 个并发查询把数据库打了 %d 次, want 1（singleflight 没生效）", callers, got)
		}
	})

	// 这一条是整个缓存设计里最危险的一处：数据库故障不能被翻译成"短码不存在"。
	t.Run("数据库故障时不能写否定缓存", func(t *testing.T) {
		db := newDryRunDB(t)
		err := db.Callback().Query().After("gorm:query").Register("test:database_down", func(tx *gorm.DB) {
			_ = tx.AddError(errors.New("connection refused"))
		})
		if err != nil {
			t.Fatalf("注册回调失败: %v", err)
		}

		cache := newFakeLinkCache()
		svc := &LinkService{db: db, cache: cache}

		_, gotErr := svc.GetByCode(context.Background(), "abc1234")
		if gotErr == nil {
			t.Fatal("数据库查不动时 GetByCode 应该返回错误")
		}
		if errors.Is(gotErr, domain.ErrLinkNotFound) {
			t.Error("数据库故障被翻译成了 domain.ErrLinkNotFound：调用方再也分不清'不存在'和'查不动'")
		}
		if n := cache.missingCount(); n != 0 {
			t.Errorf("数据库故障时写入了 %d 条否定缓存：这个错误的结论会按 TTL 活过故障本身", n)
		}
	})
}

// TestLogClick 对应练习 35。
func TestLogClick(t *testing.T) {
	ctx := context.Background()
	base := domain.ClickEvent{
		LinkID:    7,
		IP:        "203.0.113.9",
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/121.0.0.0",
		Platform:  domain.PlatformBrowser,
		Kind:      domain.ClickVisit,
		Referer:   "https://example.com/from",
	}

	t.Run("Kafka 可用时发消息，不再直写", func(t *testing.T) {
		pub := &fakePublisher{}
		writer := &fakeClickWriter{}
		svc := &LinkService{kafka: pub, clickWriter: writer}

		svc.LogClick(ctx, base)

		events := pub.recorded()
		if len(events) != 1 {
			t.Fatalf("Kafka 收到 %d 条事件, want 1", len(events))
		}
		if events[0].EventID == "" {
			t.Error("事件没有 event_id：worker 无法去重，Kafka 的 at-least-once 会变成重复计数")
		}
		if events[0].Kind != string(domain.ClickVisit) {
			t.Errorf("Kind = %q, want %q", events[0].Kind, domain.ClickVisit)
		}
		if len(writer.recorded()) != 0 {
			t.Error("Kafka 成功之后又写了一次数据库：这次点击会被记两遍")
		}
	})

	t.Run("Kafka 失败时降级直写，event_id 不变", func(t *testing.T) {
		pub := &fakePublisher{err: errors.New("broker is down")}
		writer := &fakeClickWriter{}
		svc := &LinkService{kafka: pub, clickWriter: writer}

		svc.LogClick(ctx, base)

		events := writer.recorded()
		if len(events) != 1 {
			t.Fatalf("降级路径写了 %d 条, want 1", len(events))
		}
		if events[0].EventID == "" {
			t.Error("降级写入的事件没有 event_id：它和 worker 会各写一行")
		}
		if events[0].LinkID != base.LinkID {
			t.Errorf("LinkID = %d, want %d", events[0].LinkID, base.LinkID)
		}
	})

	// 这个函数是在 handler 起的 goroutine 上跑的，Gin 的 recovery 覆盖不到它。
	// 一次 publish panic 如果没被兜住，整个进程就没了——原项目真的这样挂过。
	t.Run("publish panic 不能杀进程，并且要降级", func(t *testing.T) {
		pub := &fakePublisher{panicOn: true}
		writer := &fakeClickWriter{}
		svc := &LinkService{kafka: pub, clickWriter: writer}

		svc.LogClick(ctx, base) // 不 recover 的话这里直接把测试进程带走

		if len(writer.recorded()) != 1 {
			t.Errorf("panic 之后没有走降级直写，这次点击丢了（writer 收到 %d 条）", len(writer.recorded()))
		}
	})

	t.Run("没有 Kafka 时直接写库", func(t *testing.T) {
		writer := &fakeClickWriter{}
		svc := &LinkService{kafka: nil, clickWriter: writer}

		svc.LogClick(ctx, base)

		if len(writer.recorded()) != 1 {
			t.Fatalf("没有 Kafka 时写了 %d 条, want 1（降级路径没跑）", len(writer.recorded()))
		}
	})

	t.Run("找不到 writer 也不能 panic", func(t *testing.T) {
		svc := &LinkService{kafka: nil, clickWriter: nil}
		svc.LogClick(ctx, base) // 什么都不该发生，尤其不该 panic
	})
}

// TestCacheLinkClearsMissingEntry 对应练习 37。
//
// 顺序是有意义的：先清"不存在"的记录，再写正向缓存。反过来的话，
// ClearMissing 会把刚写进去的正向缓存一起删掉（它们是两个键）。
func TestCacheLinkClearsMissingEntry(t *testing.T) {
	cache := newFakeLinkCache()
	svc := &LinkService{cache: cache}
	ctx := context.Background()

	cache.MarkMissing(ctx, "abc1234")
	svc.cacheLink(ctx, &domain.LinkInfo{ShortCode: "abc1234", OriginalURL: "https://example.com/t", IsActive: true})

	if cache.IsMissing(ctx, "abc1234") {
		t.Error("否定缓存活过了它否定掉的那条链接")
	}
	if _, ok := cache.GetLink(ctx, "abc1234"); !ok {
		t.Error("链接没有被写进缓存")
	}
}

// TestBuildShortURL / TestNormalizePage / TestIsLinkUsable 测的是已给的辅助函数，
// 但它们的行为是上面那些逻辑的"地基"，被改坏时最容易悄无声息。
func TestGivenHelpers(t *testing.T) {
	svc := &LinkService{baseURL: "https://kada.link/"}
	if got := svc.BuildShortURL("", "abc1234"); got != "https://kada.link/r/abc1234" {
		t.Errorf("BuildShortURL(空域名) = %q", got)
	}
	if got := svc.BuildShortURL("short.example.com", "abc1234"); got != "https://short.example.com/r/abc1234" {
		t.Errorf("BuildShortURL(自定义域名) = %q", got)
	}

	if page, size := normalizePage(0, 0); page != 1 || size != 20 {
		t.Errorf("normalizePage(0,0) = %d,%d, want 1,20", page, size)
	}
	if page, size := normalizePage(2, 100000); page != 2 || size != 100 {
		t.Errorf("normalizePage(2,100000) = %d,%d, want 2,100（page_size 必须夹紧）", page, size)
	}
}

// TestLinkCacheInterfaceIsSatisfied 保证假对象和真实现在同一个接口上，
// 否则测试会"因为签名漂移"而测了个寂寞。
func TestLinkCacheInterfaceIsSatisfied(t *testing.T) {
	var _ LinkCache = newFakeLinkCache()
	var _ ClickWriter = &fakeClickWriter{}
}
