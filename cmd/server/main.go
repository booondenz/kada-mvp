// Command server 是 API 进程的入口。
//
// 这个文件是**已实现**的，而且它有一件很具体的职责：**它是整个程序里唯一组装对象图的地方**。
// 数据库、Redis、Kafka、service、handler、中间件——全都在这里被创建、被连接、被挂到路由上。
// 别处不要再 new 一个 service：那样"谁依赖谁"就没有一个能一眼看完的地方了。
//
// 对照原项目：kada/cmd/server/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"kada-mvp/config"
	analyticsHandler "kada-mvp/internal/handler/analytics"
	authHandler "kada-mvp/internal/handler/auth"
	folderHandler "kada-mvp/internal/handler/folder"
	linkHandler "kada-mvp/internal/handler/link"
	redirectHandler "kada-mvp/internal/handler/redirect"
	tagHandler "kada-mvp/internal/handler/tag"
	workspaceHandler "kada-mvp/internal/handler/workspace"
	"kada-mvp/internal/infra"
	"kada-mvp/internal/middleware"
	"kada-mvp/internal/mq"
	"kada-mvp/internal/service"
)

func main() {
	// .env 只影响本地开发；生产环境的值由部署流程写进进程环境。
	_ = godotenv.Load()

	cfg := config.Load()

	// release 模式下拒绝已知的弱 JWT 密钥：否则任何人都能伪造登录 token。
	// 这个检查放在启动路径上，而不是"文档里提醒一句"——提醒不会阻止一次事故。
	if os.Getenv("GIN_MODE") == "release" && config.IsWeakJWTSecret(cfg.JWTSecret) {
		log.Fatal("refusing to use the default JWT_SECRET in production; set a strong random secret")
	}

	db, err := infra.NewDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer infra.CloseDB(db)

	// 表结构：GORM 的 AutoMigrate 是唯一的事实来源（见 internal/domain/entity）。
	// 生产环境一般把 DB_AUTO_MIGRATE 设为 false，由 cmd/migrate 显式执行——
	// 这样"重启一次服务"永远不会顺手改数据库。
	if cfg.AutoMigrate {
		if err := infra.Migrate(db); err != nil {
			log.Fatalf("Failed to migrate database: %v", err)
		}
	} else {
		log.Println("DB_AUTO_MIGRATE=false: skipping schema migration (run cmd/migrate to apply it)")
	}

	// Redis：**即使第一次 ping 失败也保留 client**（go-redis 会自己重连）。
	// 缓存和限流都"失败即放行"，所以 Redis 掉线时正确的行为是继续服务，等它恢复。
	redisClient, err := infra.NewRedis(cfg.RedisURL)
	if err != nil {
		log.Printf("Redis temporarily unavailable (rate limiting and caching will fail open): %v", err)
	}
	if redisClient != nil {
		defer infra.CloseRedis(redisClient)
	}

	// 缓存变量声明成**接口类型**，而且只在真正构造成功时才赋值。
	// 这不是风格问题：把 (*CacheService)(nil) 存进接口之后，接口本身不再是 nil，
	// 于是所有 `cache != nil` 的判断都会通过，第一次调用就解引用空指针。
	var cacheSvc service.LinkCache
	if redisClient != nil {
		cacheSvc = service.NewCacheService(redisClient)
	}

	// Kafka publisher：没有 broker 时返回 nil，变量保持接口的 nil（同上）。
	var kafkaPub mq.ClickPublisher
	if pub := mq.NewKafkaClickPublisher(cfg.Brokers(), cfg.KafkaTopic); pub != nil {
		defer pub.Close()
		kafkaPub = pub
	} else {
		log.Println("Kafka not configured: clicks will be written straight to the database")
	}

	// ---- service 层 ----
	authSvc := service.NewAuthService(db, cfg.JWTSecret, cfg.JWTExpires, nil) // nil → 用"把验证码打到日志"的开发实现
	linkSvc := service.NewLinkService(db, cfg.BaseURL, cacheSvc, kafkaPub, service.NewClickStore(db))
	folderSvc := service.NewFolderService(db)
	tagSvc := service.NewTagService(db)
	workspaceSvc := service.NewWorkspaceService(db)
	analyticsSvc := service.NewAnalyticsService(db)

	// ---- handler 层 ----
	authH := authHandler.NewHandler(authSvc)
	linkH := linkHandler.NewHandler(linkSvc)
	redirectH := redirectHandler.NewHandler(linkSvc)
	analyticsH := analyticsHandler.NewHandler(analyticsSvc)
	folderH := folderHandler.NewHandler(folderSvc)
	tagH := tagHandler.NewHandler(tagSvc)
	workspaceH := workspaceHandler.NewHandler(workspaceSvc)

	// ---- 中间件 ----
	authMW := middleware.JWTAuth(cfg.JWTSecret)

	var rateLimiter *middleware.RateLimiter
	if redisClient != nil {
		rateLimiter = middleware.NewRateLimiter(redisClient)
	}

	if os.Getenv("GIN_MODE") == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()

	// 不信任何代理发来的 X-Forwarded-For（客户端可以自己伪造它，用来绕过限流）。
	// 真实 IP 只从 nginx 重写的 X-Real-IP 取（见 middleware.RealIP）。
	_ = r.SetTrustedProxies(nil)

	if rateLimiter != nil {
		r.Use(rateLimiter.Normal())
	}

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "kada-mvp-api", "version": "0.1.0"})
	})

	// 短链跳转：公开路径，挂最宽的一档限流。
	var redirectMW gin.HandlerFunc
	if rateLimiter != nil {
		redirectMW = rateLimiter.Redirect()
	}
	redirectH.RegisterRoutes(r, redirectMW)

	// 其余 API，全部在 JWT 之后。
	v1 := r.Group("/api")
	{
		var strictMW gin.HandlerFunc
		if rateLimiter != nil {
			strictMW = rateLimiter.Strict()
		}
		authH.RegisterRoutes(v1, authMW, strictMW)
		linkH.RegisterRoutes(v1, authMW)
		analyticsH.RegisterRoutes(v1, authMW)
		folderH.RegisterRoutes(v1, authMW)
		tagH.RegisterRoutes(v1, authMW)
		workspaceH.RegisterRoutes(v1, authMW)
	}

	// 用 http.Server 而不是 r.Run()：为了能优雅退出，也为了设置超时。
	// ReadHeaderTimeout 是防 Slowloris（慢头攻击）的那一项，不是可有可无的装饰。
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		log.Printf("Kada MVP API listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 优雅退出：收到 SIGINT/SIGTERM 之后，给正在处理的请求最多 10 秒。
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()
	log.Println("shutdown signal received, starting graceful shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	log.Println("server stopped")
}
