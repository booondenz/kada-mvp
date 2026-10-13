// Package config 是进程唯一的配置来源：整个程序只有这个包读环境变量。
//
// 对照原项目：kada/config/config.go（那一份还带 AI、阿里云短信、WeChat 的配置项，
// MVP 里没有这些组件，所以这里的 Config 更短）。
//
// 这一份是**已实现**的，作为样板：读环境变量 → 解析 → 兜底默认值 → 在 release 模式下
// 拒绝弱密钥。你可以照着它的风格去填别的文件。
package config

import (
	"os"
	"strconv"
	"strings"
)

// Config 是进程的全部配置。字段名对应一个环境变量，见 .env.example。
type Config struct {
	Port        string
	DatabaseURL string
	RedisURL    string
	JWTSecret   string
	JWTExpires  string // 形如 "720h"，喂给 time.ParseDuration
	BaseURL     string // 生成短链时的前缀，例如 https://kada.link
	FrontendURL string // CORS 白名单

	// AutoMigrate 决定这个进程是否有权在启动时改数据库结构。
	// 生产环境建议 false，由 cmd/migrate 显式执行，这样"重启服务"永远不会顺手改表。
	AutoMigrate bool

	// Kafka（点击事件流；留空 = 没有 Kafka，点击直接落库，也就是降级模式）
	KafkaBrokers string
	KafkaTopic   string
}

// Load 读取环境变量，缺省值按开发环境给。
func Load() *Config {
	return &Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://kada:kada123@localhost:5432/kada?sslmode=disable"),
		RedisURL:    getEnv("REDIS_URL", "redis://localhost:6379"),
		JWTSecret:   getEnv("JWT_SECRET", "kada-dev-secret-change-in-production"),
		JWTExpires:  getEnv("JWT_EXPIRES_IN", "720h"),
		BaseURL:     getEnv("API_BASE_URL", "https://kada.link"),
		FrontendURL: getEnv("FRONTEND_URL", "http://localhost:3000"),

		AutoMigrate:  getEnvBool("DB_AUTO_MIGRATE", true),
		KafkaBrokers: getEnv("KAFKA_BROKERS", ""),
		KafkaTopic:   getEnv("KAFKA_TOPIC", "clicks"),
	}
}

// Brokers 把逗号分隔的 broker 列表切开，去掉空白项。
func (c *Config) Brokers() []string {
	return SplitBrokers(c.KafkaBrokers)
}

// SplitBrokers 被 server 和 worker 共用：两处对同一个变量的解析必须一致，
// 否则会出现"API 认为 Kafka 开着、worker 认为没开"这种最难查的分歧。
func SplitBrokers(raw string) []string {
	var out []string
	for _, b := range strings.Split(raw, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	return out
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getEnvBool 解析布尔值；解析不了（包括空值）时回到默认值，而不是静默关掉一个功能。
func getEnvBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// weakJWTSecrets 是已知的弱默认密钥。release 模式下用到这些值就直接拒绝启动，
// 否则任何人都能伪造登录 token。
var weakJWTSecrets = []string{
	"",
	"kada-dev-secret-change-in-production",
	"changeme",
	"secret",
	"jwt-secret",
}

// IsWeakJWTSecret 报告这个密钥是否属于已知的弱密钥。
func IsWeakJWTSecret(s string) bool {
	for _, w := range weakJWTSecrets {
		if s == w {
			return true
		}
	}
	return false
}
