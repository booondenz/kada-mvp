package service

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"kada-mvp/internal/domain"
)

// newCacheWithMiniredis 起一个内存里的 Redis，让缓存服务跑在真实的命令上。
//
// 缓存这一层用假对象测不出东西：要验证的恰好是"键怎么拼、TTL 设多长、
// 删的时候删了几个键"——而这些全都是 Redis 命令的细节。
func newCacheWithMiniredis(t *testing.T) (*CacheService, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewCacheService(client), mr
}

func sampleLink(shortCode string) *domain.LinkInfo {
	return &domain.LinkInfo{
		ID:          1,
		ShortCode:   shortCode,
		ShortURL:    "https://kada.link/r/" + shortCode,
		OriginalURL: "https://example.com/target",
		IsActive:    true,
		HasPassword: true,
	}
}

// TestCacheServiceLinkRoundTrip 对应练习 19 / 20。
func TestCacheServiceLinkRoundTrip(t *testing.T) {
	cs, _ := newCacheWithMiniredis(t)
	ctx := context.Background()

	if _, ok := cs.GetLink(ctx, "abc1234"); ok {
		t.Fatal("空缓存里读到了东西")
	}

	cs.SetLink(ctx, sampleLink("abc1234"))

	got, ok := cs.GetLink(ctx, "abc1234")
	if !ok {
		t.Fatal("刚写进去的链接读不回来")
	}
	if got.OriginalURL != "https://example.com/target" {
		t.Errorf("OriginalURL = %q", got.OriginalURL)
	}
	// HasPassword 必须跟着走：跳转路径完全依赖缓存里的这个字段来决定要不要展示密码页。
	if !got.HasPassword {
		t.Error("HasPassword 在缓存往返里丢了：跳转时会把需要密码的链接直接放过去")
	}
}

// TestCacheServiceSetLinkRespectsExpiry 对应练习 20 的第 2 条。
//
// cache-aside 的缓存绝不能比它缓存的事实活得更久。
func TestCacheServiceSetLinkRespectsExpiry(t *testing.T) {
	cs, mr := newCacheWithMiniredis(t)
	ctx := context.Background()

	// 30 秒后过期 → TTL 必须是 30 秒左右，而不是默认的 10 分钟。
	soon := time.Now().Add(30 * time.Second)
	info := sampleLink("expiring")
	info.ExpiresAt = &soon
	cs.SetLink(ctx, info)

	ttl := mr.TTL("cache:link:expiring")
	if ttl <= 0 {
		t.Fatal("缓存里没有这个键")
	}
	if ttl > 31*time.Second {
		t.Errorf("TTL = %v, want ≤ 31s（链接 30 秒后过期，缓存不能活 10 分钟）", ttl)
	}
	if ttl < 25*time.Second {
		t.Errorf("TTL = %v, want ≈ 30s", ttl)
	}

	// 已经过期的链接根本不该被缓存。
	past := time.Now().Add(-time.Second)
	expired := sampleLink("already-gone")
	expired.ExpiresAt = &past
	cs.SetLink(ctx, expired)
	if _, ok := cs.GetLink(ctx, "already-gone"); ok {
		t.Error("已经过期的链接被写进了缓存")
	}
}

// TestCacheServiceInvalidateClearsBothKeys 对应练习 21。
func TestCacheServiceInvalidateClearsBothKeys(t *testing.T) {
	cs, _ := newCacheWithMiniredis(t)
	ctx := context.Background()

	cs.MarkMissing(ctx, "abc1234")
	cs.SetLink(ctx, sampleLink("abc1234"))

	cs.InvalidateLink(ctx, "abc1234")

	if _, ok := cs.GetLink(ctx, "abc1234"); ok {
		t.Error("链接缓存没有被删掉")
	}
	if cs.IsMissing(ctx, "abc1234") {
		t.Error("'不存在'的记录没有被删掉：新建/改名后的短码会被自己的否定缓存挡住最多 60 秒")
	}
}

// TestCacheServiceMissing 对应练习 22 / 23 / 24（MarkMissing / ClearMissing 已给）。
func TestCacheServiceMissing(t *testing.T) {
	cs, mr := newCacheWithMiniredis(t)
	ctx := context.Background()

	if cs.IsMissing(ctx, "absent99") {
		t.Fatal("什么都没写就报'不存在'")
	}

	cs.MarkMissing(ctx, "absent99")
	if !cs.IsMissing(ctx, "absent99") {
		t.Fatal("MarkMissing 之后 IsMissing 仍然为 false")
	}

	// 否定缓存的 TTL 必须明显短于正常链接：它每多活一秒，就多一秒"刚创建的短码被挡住"。
	ttl := mr.TTL("cache:linkmiss:absent99")
	if ttl <= 0 || ttl > 61*time.Second {
		t.Errorf("否定缓存的 TTL = %v, want (0, 60s]", ttl)
	}

	cs.ClearMissing(ctx, "absent99")
	if cs.IsMissing(ctx, "absent99") {
		t.Error("ClearMissing 之后 IsMissing 仍然为 true")
	}
}

// Redis 掉线时，缓存必须"什么都不做"而不是"报'不存在'"。
//
// 把一次 Redis 故障读成"所有短码都不存在"，等于在故障期间把所有访问者挡在门外，
// 而且这个错误的结论还会按 TTL 活过故障本身。
func TestCacheServiceFailsOpenWhenRedisIsDown(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 50 * time.Millisecond,
		MaxRetries:  0,
	})
	t.Cleanup(func() { _ = client.Close() })
	cs := NewCacheService(client)
	ctx := context.Background()

	if _, ok := cs.GetLink(ctx, "abc1234"); ok {
		t.Error("Redis 掉线时 GetLink 报了命中")
	}
	if cs.IsMissing(ctx, "abc1234") {
		t.Error("Redis 掉线时 IsMissing 返回了 true：这会把一次故障翻译成'所有短码都不存在'")
	}

	// 写入类方法在 Redis 掉线时必须只是记日志，不 panic、不死循环。
	cs.SetLink(ctx, sampleLink("abc1234"))
	cs.MarkMissing(ctx, "abc1234")
	cs.ClearMissing(ctx, "abc1234")
	cs.InvalidateLink(ctx, "abc1234")
}
