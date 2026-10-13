// Package infra 是"外部世界"的适配层：数据库、Redis、以及各种小工具。
//
// 分层规则：handler 不认识数据库，service 通过 *gorm.DB 认识数据库，
// 而"怎么连上数据库"这件事只在 infra 里。
package infra

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"kada-mvp/internal/domain/entity"
)

// NewDB 打开 PostgreSQL 连接池并 ping 一次。
//
// 注意这里**没有**调用 AutoMigrate：能不能改表结构由调用方决定（见 Migrate）。
// 如果连接时顺手迁移，那么"API 重启"就等于"可能改数据库"，生产环境不能接受这种副作用。
//
// 这一份是**已实现**的（样板）。
func NewDB(databaseURL string) (*gorm.DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is empty")
	}

	cfg := &gorm.Config{
		// 把 PostgreSQL 的 SQLSTATE 翻译成 gorm.ErrDuplicatedKey / gorm.ErrForeignKeyViolated，
		// 这样 service 层可以用 errors.Is 判断，而不用去匹配驱动特有的错误码。
		TranslateError: true,
		Logger:         gormLogger(),
	}

	db, err := gorm.Open(postgres.Open(databaseURL), cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to access the underlying connection pool: %w", err)
	}

	// 连接池参数显式写出来，避免"换台机器行为就变了"。
	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	sqlDB.SetConnMaxLifetime(time.Hour)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("Database connected")
	return db, nil
}

// gormLogger 在 release 模式下安静下来；其他情况下打印慢查询和错误。
func gormLogger() logger.Interface {
	level := logger.Warn
	if os.Getenv("GIN_MODE") != "release" {
		level = logger.Info
	}
	return logger.Default.LogMode(level)
}

// Migrate 把表结构补齐到 entity 定义的样子。
//
// AutoMigrate 只做加法：建缺失的表、加缺失的列和索引、补缺失的约束，永远不删东西。
// 所以它可以在已有数据上反复执行，也可以安全地回滚二进制（多出来的列留着不动）。
//
// 代价要记住：**模型不是现有数据库的全部事实**。原项目那台机器上，
// 早期用 SQL 建的表还留着模型里已经没有的 NOT NULL 列，AutoMigrate 不会去放宽它，
// 于是每次插入都失败。所以原项目在 AutoMigrate 之前跑了两个显式的 DDL 兼容步骤
// （见 kada/internal/infra/schema/compat.go）——MVP 从零开始，用不到，但你得知道这个坑存在。
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(entity.Models()...); err != nil {
		return fmt.Errorf("auto migration failed: %w", err)
	}
	log.Println("Database schema is up to date")
	return nil
}

// CloseDB 关闭底层连接池。
func CloseDB(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Printf("failed to access the connection pool while closing: %v", err)
		return
	}
	if err := sqlDB.Close(); err != nil {
		log.Printf("failed to close the database connection: %v", err)
		return
	}
	log.Println("Database connection closed")
}
