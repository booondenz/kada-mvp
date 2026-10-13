package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// miniredis 是一个纯 Go 的 Redis 服务端实现，跑在内存里。
//
// 为什么不用"假 client"？因为限流的核心逻辑就在 Redis 命令上（ZSET 的滑动窗口），
// 换成一个自制的假对象，测试就变成在验证假设而不是验证行为。
func newMiniredisClient(t *testing.T) (*redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client, mr
}

func hit(r *gin.Engine, ip string) int {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if ip != "" {
		// 走的是 nginx 会重写的那个头，也就是限流器唯一信任的来源。
		req.Header.Set("X-Real-IP", ip)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestRateLimit 对应练习 4。
func TestRateLimit(t *testing.T) {
	client, _ := newMiniredisClient(t)
	rl := NewRateLimiter(client)

	r := gin.New()
	r.GET("/x", rl.Limit(RateLimitConfig{Window: time.Minute, Limit: 3}),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	// 前 3 次放行。
	for i := 1; i <= 3; i++ {
		if code := hit(r, "203.0.113.1"); code != http.StatusOK {
			t.Fatalf("第 %d 次请求 = %d, want 200", i, code)
		}
	}

	// 第 4 次要被拦住，并且带上 Retry-After 语义的信息。
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Real-IP", "203.0.113.1")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("第 4 次请求 = %d, want 429", w.Code)
	}
	if got := w.Header().Get("X-RateLimit-Limit"); got != "3" {
		t.Errorf("X-RateLimit-Limit = %q, want \"3\"", got)
	}
	if got := w.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Errorf("X-RateLimit-Remaining = %q, want \"0\"", got)
	}
	if body := w.Body.String(); body == `{"ok":true}` {
		t.Error("被限流的请求仍然执行到了 handler：缺少 c.Abort()")
	}
}

// 两个不同的 IP 是两个独立的桶。这条测试同时验证了 RealIP 被用在分桶键上。
func TestRateLimitIsPerClientIP(t *testing.T) {
	client, _ := newMiniredisClient(t)
	rl := NewRateLimiter(client)

	r := gin.New()
	r.GET("/x", rl.Limit(RateLimitConfig{Window: time.Minute, Limit: 1}),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	if code := hit(r, "203.0.113.1"); code != http.StatusOK {
		t.Fatalf("第一个 IP 的首次请求 = %d, want 200", code)
	}
	if code := hit(r, "203.0.113.1"); code != http.StatusTooManyRequests {
		t.Fatalf("同一个 IP 的第二次请求 = %d, want 429", code)
	}
	if code := hit(r, "198.51.100.7"); code != http.StatusOK {
		t.Fatalf("另一个 IP 的首次请求 = %d, want 200（分桶必须按 IP）", code)
	}
}

// 每个计数 key 都必须有 TTL。
//
// 这一条不是锦上添花：滑动窗口的 ZSET 只在"同一个 key 再来请求"时才会被清理，
// 而一个再也没有人访问的 IP（比如扫描器换了一个地址）如果不带 TTL，
// 它的 key 会永远留在 Redis 里。线上表现为"内存缓慢上涨，而且找不到是谁写的"。
func TestRateLimitKeyExpires(t *testing.T) {
	client, mr := newMiniredisClient(t)
	rl := NewRateLimiter(client)

	r := gin.New()
	r.GET("/x", rl.Limit(RateLimitConfig{Window: time.Minute, Limit: 5}),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	if code := hit(r, "203.0.113.1"); code != http.StatusOK {
		t.Fatalf("首次请求 = %d, want 200", code)
	}

	ttl := mr.TTL("ratelimit:203.0.113.1")
	if ttl <= 0 {
		t.Errorf("限流的 key 没有过期时间（TTL = %v）；它必须被 Expire 设成窗口的若干倍", ttl)
	}
}

// Redis 不可用时必须 **fail open**（放行），而不是 429 或 500。
//
// 这个取舍写在原项目的设计文档里：产品的可用性排在限流的严格性之前。
// 一个连不上 Redis 的部署如果开始拒绝所有请求，故障面会从"限流失效"扩大到"全站不可用"。
func TestRateLimitFailsOpenWhenRedisIsDown(t *testing.T) {
	// 指向一个没有服务在监听的端口；DialTimeout 调小以免测试变慢。
	client := redis.NewClient(&redis.Options{
		Addr:        "127.0.0.1:1",
		DialTimeout: 50 * time.Millisecond,
		MaxRetries:  0,
	})
	t.Cleanup(func() { _ = client.Close() })

	rl := NewRateLimiter(client)
	r := gin.New()
	r.GET("/x", rl.Limit(RateLimitConfig{Window: time.Minute, Limit: 1}),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })

	for i := 1; i <= 3; i++ {
		if code := hit(r, "203.0.113.1"); code != http.StatusOK {
			t.Fatalf("Redis 掉线时第 %d 次请求 = %d, want 200（fail open）", i, code)
		}
	}
}

// 四档限流是已给的配置，但它们是否真的挂在中间件上值得钉一下：
// 一个返回 nil 的档位在 main.go 里会被 `if rateLimiter != nil` 悄悄跳过。
func TestRateLimitTiersExist(t *testing.T) {
	client, _ := newMiniredisClient(t)
	rl := NewRateLimiter(client)

	for name, mw := range map[string]gin.HandlerFunc{
		"Strict":   rl.Strict(),
		"Normal":   rl.Normal(),
		"Redirect": rl.Redirect(),
	} {
		if mw == nil {
			t.Errorf("%s() 返回了 nil", name)
		}
	}
}
