// Command migrate 只做一件事：把表结构补齐到 entity 定义的样子，然后退出。
//
// 为什么要有这个独立命令？因为"服务启动时顺手迁移"在生产环境是不可接受的副作用：
// 一次回滚、一次重启都可能改数据库。标准做法是：部署时先跑 migrate，再重启服务，
// 于是 API 进程只需要 DB_AUTO_MIGRATE=false。
//
// 这个文件是**已实现**的。
package main

import (
	"log"

	"github.com/joho/godotenv"

	"kada-mvp/config"
	"kada-mvp/internal/infra"
)

func main() {
	_ = godotenv.Load()

	cfg := config.Load()

	log.Println("Applying database schema (GORM AutoMigrate)...")
	db, err := infra.NewDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer infra.CloseDB(db)

	if err := infra.Migrate(db); err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	log.Println("Migration done.")
}
