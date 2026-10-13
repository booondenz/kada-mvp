package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"kada-mvp/internal/mq"
)

// fakeStore 是 service.ClickWriter 的假实现：
// worker 的测试不该需要一个数据库，就像它不该需要一个 Kafka。
type fakeStore struct {
	events []mq.ClickEvent
	err    error
}

func (f *fakeStore) WriteClick(_ context.Context, e mq.ClickEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, e)
	return nil
}

// TestProcessClickMessage 对应练习 89。
func TestProcessClickMessage(t *testing.T) {
	t.Run("合法消息被写进库", func(t *testing.T) {
		store := &fakeStore{}
		raw, err := json.Marshal(mq.ClickEvent{
			EventID:   "evt-1",
			LinkID:    7,
			Kind:      "visit",
			CreatedAt: time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatalf("构造消息失败: %v", err)
		}

		if err := processClickMessage(raw, store); err != nil {
			t.Fatalf("processClickMessage 返回错误: %v", err)
		}
		if len(store.events) != 1 {
			t.Fatalf("写入了 %d 条, want 1", len(store.events))
		}
		if store.events[0].EventID != "evt-1" {
			t.Errorf("EventID = %q", store.events[0].EventID)
		}
	})

	// 坏 JSON 是最典型的"毒消息"：重试多少次都一样，所以它必须被识别成
	// 不可重试的错误，否则它会把单分区消费组永远堵住。
	t.Run("坏 JSON 是不可重试的错误", func(t *testing.T) {
		store := &fakeStore{}
		err := processClickMessage([]byte("{not json"), store)
		if err == nil {
			t.Fatal("坏 JSON 应该返回错误")
		}
		if !isPermanentError(err) {
			t.Errorf("isPermanentError(%v) = false, want true（否则这条消息会被反复重试直到重试上限）", err)
		}
		if len(store.events) != 0 {
			t.Error("坏消息不该写进库")
		}
	})

	// 写库失败是**可重试**的：数据库可能只是暂时忙。
	t.Run("写库失败是可重试的", func(t *testing.T) {
		store := &fakeStore{err: errors.New("database is busy")}
		raw, _ := json.Marshal(mq.ClickEvent{EventID: "evt-2", LinkID: 7, Kind: "visit"})

		err := processClickMessage(raw, store)
		if err == nil {
			t.Fatal("写库失败应该返回错误")
		}
		if isPermanentError(err) {
			t.Error("普通的写库失败被判成了永久错误：那条点击会被直接丢掉，而不是重试")
		}
	})
}

// isPermanentError 是已给的，但它是"什么时候认输"的唯一定义，钉住它。
func TestIsPermanentError(t *testing.T) {
	if isPermanentError(errors.New("some transient failure")) {
		t.Error("普通错误不该被判成永久错误")
	}
	if !isPermanentError(&json.SyntaxError{}) {
		t.Error("JSON 语法错误必须是永久错误")
	}
}

// attemptTracker 是已给的（内存里的重试计数），但它决定"重试上限"是否成立。
func TestAttemptTracker(t *testing.T) {
	tr := newAttemptTracker()
	for i := 1; i <= 3; i++ {
		if got := tr.record("clicks", 0, 100); got != i {
			t.Fatalf("第 %d 次 record = %d", i, got)
		}
	}
	tr.reset("clicks", 0, 100)
	if got := tr.record("clicks", 0, 100); got != 1 {
		t.Errorf("reset 之后 record = %d, want 1", got)
	}
	// 不同的 offset 是不同的消息，计数不能互相污染。
	if got := tr.record("clicks", 0, 101); got != 1 {
		t.Errorf("另一条消息的 record = %d, want 1", got)
	}
}
