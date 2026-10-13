package infra

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// NewRedis 建 Redis 客户端。
//
// 注意返回值：**即使 ping 失败也把 client 还给你**（同时带一个 error）。
// 这不是笔误，是设计：go-redis 会在后台自己重连，而本项目的缓存和限流都"失败即放行"，
// 所以 Redis 掉线时正确的行为是继续服务（缓存未命中、限流暂时不拦），
// 等它恢复后自动重新生效，全程不需要重启进程。
//
// 这一份是**已实现**的（样板）。
func NewRedis(redisURL string) (*redis.Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return client, fmt.Errorf("failed to ping Redis: %w", err)
	}

	log.Println("Redis connected")
	return client, nil
}

// CloseRedis 关闭连接。
func CloseRedis(client *redis.Client) {
	if client == nil {
		return
	}
	if err := client.Close(); err != nil {
		log.Printf("close redis failed: %v", err)
		return
	}
	log.Println("Redis connection closed")
}
