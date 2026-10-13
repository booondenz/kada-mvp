package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"kada-mvp/internal/mq"
)

var errInsertFailed = errors.New("insert failed")

// newMockDB 把 GORM 接到一个"假的数据库驱动"上。
//
// 为什么这里不能用 DryRun？因为 WriteClick 用的是**显式事务**，
// 而 GORM 的 Begin() 不管 DryRun 与否都会去问连接池要一个真事务
// （见 gorm/finisher_api.go 的 Begin：直接调用 BeginTx）。
// 所以这段逻辑只能在"有一个真的会应答的连接"上测——sqlmock 就是那个连接。
func newMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	t.Helper()
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("构建 sqlmock 失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{
		TranslateError:         true,
		SkipDefaultTransaction: true,
		DisableAutomaticPing:   true,
	})
	if err != nil {
		t.Fatalf("把 GORM 接到 sqlmock 失败: %v", err)
	}
	return db, mock
}

func sampleEvent(kind string) mq.ClickEvent {
	return mq.ClickEvent{
		EventID:   "evt-1",
		LinkID:    7,
		IP:        "203.0.113.9",
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/121.0.0.0",
		Platform:  "browser",
		Kind:      kind,
		Referer:   "https://example.com/from",
		CreatedAt: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC),
	}
}

// 正则里用 "? 容忍 GORM 的引号：原样写 SQL 的人和用 clause 构造器的人
// 生成的大小写/引号可能不同，而这条测试要盯的是**语义**。
var (
	insertClickLog = `INSERT INTO "?click_logs"?`
	updateCount    = `UPDATE "?links"?`
)

// TestWriteClickCountsOnlyAVisit 对应练习 25。
//
// 这一条覆盖了"插入成功 + kind=visit"这条唯一会动计数器的路。
func TestWriteClickCountsOnlyAVisit(t *testing.T) {
	db, mock := newMockDB(t)
	store := NewClickStore(db)

	mock.ExpectBegin()
	mock.ExpectExec(insertClickLog).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(updateCount).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := store.WriteClick(context.Background(), sampleEvent("visit")); err != nil {
		t.Fatalf("WriteClick 返回错误: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// 非 visit 的行要**记下来但不动计数器**：一条预取请求是有价值的观测数据，
// 但它不是一个人打开了链接。
func TestWriteClickRecordsWithoutCountingOtherKinds(t *testing.T) {
	for _, kind := range []string{"request", ""} {
		name := kind
		if name == "" {
			name = "(空 kind)"
		}
		t.Run(name, func(t *testing.T) {
			db, mock := newMockDB(t)
			store := NewClickStore(db)

			mock.ExpectBegin()
			mock.ExpectExec(insertClickLog).WillReturnResult(sqlmock.NewResult(1, 1))
			// 这里**故意不**期待 UPDATE：多余的 UPDATE 会让 ExpectationsWereMet 失败。
			mock.ExpectCommit()

			if err := store.WriteClick(context.Background(), sampleEvent(kind)); err != nil {
				t.Fatalf("WriteClick 返回错误: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Error(err)
			}
		})
	}
}

// Kafka 是 at-least-once，重复投递是常态而不是异常。
// 第二次插入撞上 event_id 的唯一索引 → RowsAffected=0 → 计数不能再加一次。
func TestWriteClickDoesNotCountADuplicateEvent(t *testing.T) {
	db, mock := newMockDB(t)
	store := NewClickStore(db)

	mock.ExpectBegin()
	mock.ExpectExec(insertClickLog).WillReturnResult(sqlmock.NewResult(0, 0)) // ON CONFLICT DO NOTHING
	mock.ExpectCommit()

	if err := store.WriteClick(context.Background(), sampleEvent("visit")); err != nil {
		t.Fatalf("重复事件不该报错（它应该被安静地忽略）: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// 插入失败必须让整个事务回滚：一次"日志没写成但计数加了"会让统计永远对不上。
func TestWriteClickRollsBackOnInsertFailure(t *testing.T) {
	db, mock := newMockDB(t)
	store := NewClickStore(db)

	mock.ExpectBegin()
	mock.ExpectExec(insertClickLog).WillReturnError(errInsertFailed)
	mock.ExpectRollback()

	if err := store.WriteClick(context.Background(), sampleEvent("visit")); err == nil {
		t.Fatal("插入失败时 WriteClick 应该返回错误")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// TestNullable 是已给的辅助函数，但它决定了"空字符串"和"没有值"的区别。
func TestNullable(t *testing.T) {
	if got := nullable(""); got != nil {
		t.Errorf("nullable(\"\") = %v, want nil（空串会让 `column IS NOT NULL` 答'有'）", got)
	}
	if got := nullable("x"); got != "x" {
		t.Errorf("nullable(\"x\") = %v, want \"x\"", got)
	}
}
