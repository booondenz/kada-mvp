package middleware

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// RealIP 返回真实客户端 IP（已给）。
//
// 这个函数值得读懂，因为它是"伪造头"这个经典漏洞的解药：
//   - X-Forwarded-For 是**客户端可以自己写**的头。如果直接信它，
//     任何人加一个 X-Forwarded-For: 1.2.3.4 就能换一个限流桶，限流等于不存在。
//   - 所以我们只认 X-Real-IP，它由 nginx 用 $remote_addr（TCP 连接的对端地址，
//     客户端改不了）重写。
//   - 没有 nginx 时（本地开发）退回到 RemoteAddr。
//
// server 那边还必须配套 `r.SetTrustedProxies(nil)`，否则 Gin 自己会去解析
// X-Forwarded-For 并把它交给 c.ClientIP()，绕过上面这套判断。
func RealIP(c *gin.Context) string {
	if ip := c.GetHeader("X-Real-IP"); ip != "" {
		if parsed := net.ParseIP(strings.TrimSpace(ip)); parsed != nil {
			return parsed.String()
		}
	}
	return c.ClientIP()
}

// RateLimitConfig 描述一档限流。
type RateLimitConfig struct {
	Window  time.Duration               // 时间窗口
	Limit   int                         // 窗口内允许的请求数
	KeyFunc func(c *gin.Context) string // 分桶的键，默认按 IP
}

// RateLimiter 是基于 Redis 滑动窗口的限流中间件。
type RateLimiter struct {
	client *redis.Client
}

// NewRateLimiter 建一个限流器（已给）。client 为 nil 时调用方不应该创建它。
func NewRateLimiter(client *redis.Client) *RateLimiter {
	return &RateLimiter{client: client}
}

// limiterSeq 给每个请求一个进程内唯一的编号，用在下文的 ZSET member 上。
//
// 为什么不能只用时间戳当 member？因为那是一个"在 Linux 上看不出来"的 bug：
// Linux 的墙钟分辨率接近纳秒，两次请求几乎不可能撞上；而 Windows 的时钟分辨率
// 可能粗到毫秒级，同一毫秒内的几个请求会写出**同一个 member**，
// ZADD 于是变成覆盖而不是新增 —— 窗口里始终只有一条记录，限流形同虚设。
// （原项目用的是纯时间戳，它在 Linux 上跑得没问题，但这个前提不该被写进设计里。）
var limiterSeq atomic.Int64

// Limit 返回一档限流的 Gin 中间件。
//
// TODO(练习 4)：实现滑动窗口限流。
//
// 思路（Redis 有序集合 ZSET 是这类计数器的标准工具）：一个 key 存一个 ZSET，
// 成员是请求标识、score 是请求时间戳。所谓"滑动窗口"就是每次请求先丢掉窗口外的旧成员，
// 再数剩下的、然后把本次请求加进去。
//
// 要求：
//
//  1. KeyFunc 为 nil 时用 defaultKeyFunc（按 IP 分桶）。
//     key 加前缀 "ratelimit:"，否则会和缓存用的 key 撞在一个命名空间里。
//
//  2. now := time.Now().UnixNano()；windowStart := now - cfg.Window.Nanoseconds()。
//
//  3. 用一条 Pipeline（一次往返）依次执行：
//     - ZRemRangeByScore(key, "0", windowStart)：清掉窗口外的记录
//     - ZCard(key)：数窗口内的记录数（**必须在 ZAdd 之前数**，否则本次请求会把自己算进去）
//     - ZAdd(key, now, now)：把本次请求记进去
//     - Expire(key, cfg.Window*2)：给 key 一个过期时间，否则冷 key 永远留在 Redis 里
//
//  4. Pipeline 执行失败 → **直接 c.Next() 放行**（fail open）。
//     这是本项目的立场：Redis 挂了宁可暂时不拦，也不能让整个 API 变成 429 或 500。
//
//  5. 设置响应头 X-RateLimit-Limit 和 X-RateLimit-Remaining（remaining 最小为 0）。
//
//  6. 如果 count >= Limit → 429：
//
//     c.JSON(http.StatusTooManyRequests, gin.H{
//     "error":               "too many requests, please try again later",
//     "retry_after_seconds": int(cfg.Window.Seconds()),
//     })
//     c.Abort(); return
//
//  7. 否则 c.Next()。
//
// 注意成员名用 fmt.Sprintf("%d", now)：同一纳秒内的两个请求会共用成员名而只留一条，
// 这是这个实现可接受的取舍（纳米级同刻请求几乎不存在），但你要知道它存在。
//
// 验收：go test ./internal/middleware/ -run TestRateLimit
func (rl *RateLimiter) Limit(cfg RateLimitConfig) gin.HandlerFunc {
	if cfg.KeyFunc == nil {
		cfg.KeyFunc = defaultKeyFunc
	}

	return func(c *gin.Context) {
		key := "ratelimit:" + cfg.KeyFunc(c)
		now := time.Now().UnixNano()
		windowStart := now - cfg.Window.Nanoseconds()

		pipe := rl.client.Pipeline()
		pipe.ZRemRangeByScore(c.Request.Context(), key, "0", fmt.Sprintf("%d", windowStart))
		cardCmd := pipe.ZCard(c.Request.Context(), key)
		pipe.ZAdd(c.Request.Context(), key, redis.Z{
			Score: float64(now),
			// member 用"时间戳-序号"：时间戳负责 score（窗口比较），序号保证每个请求
			// 都是窗口里独立的一条。见 limiterSeq 的注释。
			Member: fmt.Sprintf("%d-%d", now, limiterSeq.Add(1)),
		})
		pipe.Expire(c.Request.Context(), key, cfg.Window*2)

		if _, err := pipe.Exec(c.Request.Context()); err != nil {
			// fail open：Redis 挂了宁可暂时不拦，也不能让整个 API 变成 429 或 500。
			//
			// 但**不能默默放行**：一句日志是这个取舍的记账单。没有它，
			// "限流好像没生效"在生产上会变成一个查不出来的问题——代码看起来是对的，
			// 而唯一知道真相的地方（这里的 err）被丢掉了。
			log.Printf("rate limit pipeline failed for %q, failing open: %v", cfg.KeyFunc(c), err)
			c.Next()
			return
		}

		count := int(cardCmd.Val())
		remaining := cfg.Limit - count
		if remaining < 0 {
			remaining = 0
		}
		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", cfg.Limit))
		c.Header("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))

		if count >= cfg.Limit {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":               "too many requests, please try again later",
				"retry_after_seconds": int(cfg.Window.Seconds()),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Strict：认证类接口（登录、注册、发验证码）。已给。
//
// 1 分钟 20 次不是随手写的数字：这几个接口是唯一"猜中了就有收益"的地方，
// 20 次/分钟/地址就是密码和脚本之间的那道墙。
func (rl *RateLimiter) Strict() gin.HandlerFunc {
	return rl.Limit(RateLimitConfig{Window: time.Minute, Limit: 20})
}

// Normal：普通 API 流量。已给。
func (rl *RateLimiter) Normal() gin.HandlerFunc {
	return rl.Limit(RateLimitConfig{Window: time.Minute, Limit: 120})
}

// Redirect：短链跳转（高频）。已给。
//
// 比 Normal 宽，因为一个办公室或一个运营商 NAT 后面的所有人共用一个出口 IP，
// 而跳转又是这个产品里最热的路径。
func (rl *RateLimiter) Redirect() gin.HandlerFunc {
	return rl.Limit(RateLimitConfig{Window: time.Minute, Limit: 300})
}

func defaultKeyFunc(c *gin.Context) string {
	return RealIP(c)
}
