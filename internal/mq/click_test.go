package mq

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// fakeWriter 是 messageWriter 的假实现：只把消息收进切片。
//
// 这就是把依赖面缩到"一个方法"的回报：整个 mq 包的测试不需要一个真 broker。
type fakeWriter struct {
	msgs []kafka.Message
	err  error
}

func (f *fakeWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	if f.err != nil {
		return f.err
	}
	f.msgs = append(f.msgs, msgs...)
	return nil
}

// TestNewKafkaClickPublisher 对应练习 5。
//
// 返回 nil 是有语义的：调用方（cmd/server）据此让 kafka 变量保持接口的 nil，
// 从而走"点击直接写库"的降级路径。返回一个"空 broker 的 publisher"会让这句话落空。
func TestNewKafkaClickPublisher(t *testing.T) {
	if p := NewKafkaClickPublisher(nil, "clicks"); p != nil {
		t.Error("brokers 为 nil 时必须返回 nil（= Kafka 未启用）")
	}
	if p := NewKafkaClickPublisher([]string{"", "   "}, "clicks"); p != nil {
		t.Error("只有空白项的 broker 列表等同于没配置，必须返回 nil")
	}
	if p := NewKafkaClickPublisher([]string{"127.0.0.1:9092"}, "clicks"); p == nil {
		t.Error("有真实 broker 时必须构造出 publisher")
	} else {
		_ = p.Close()
	}
}

// TestPublishClick 对应练习 6。
func TestPublishClick(t *testing.T) {
	fw := &fakeWriter{}
	p := &KafkaClickPublisher{writer: fw}

	created := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	event := ClickEvent{
		EventID:   "evt-1",
		LinkID:    7,
		IP:        "203.0.113.9",
		UserAgent: "Mozilla/5.0",
		Platform:  "browser",
		Kind:      "visit",
		Referer:   "https://example.com/from",
		CreatedAt: created,
	}

	if err := p.PublishClick(context.Background(), event); err != nil {
		t.Fatalf("PublishClick 返回了错误: %v", err)
	}
	if len(fw.msgs) != 1 {
		t.Fatalf("写入了 %d 条消息，want 1", len(fw.msgs))
	}

	var got ClickEvent
	if err := json.Unmarshal(fw.msgs[0].Value, &got); err != nil {
		t.Fatalf("消息不是合法 JSON: %v", err)
	}
	// 幂等键必须真的进到消息里：worker 靠它去重，丢了它 Kafka 的 at-least-once
	// 就会变成"同一次点击算两遍"。
	if got.EventID != event.EventID {
		t.Errorf("EventID = %q, want %q", got.EventID, event.EventID)
	}
	if got.Kind != event.Kind {
		t.Errorf("Kind = %q, want %q", got.Kind, event.Kind)
	}
	if got.LinkID != event.LinkID {
		t.Errorf("LinkID = %d, want %d", got.LinkID, event.LinkID)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
}

// 一个 nil 的 *KafkaClickPublisher 存进 ClickPublisher 接口之后，接口本身不是 nil。
// 所以 PublishClick 必须自己判空并返回错误，而不是解引用空指针——
// 原项目在这里真的把整个 API 进程打挂过（panic 发生在 handler 起的 goroutine 上，
// Gin 的 recovery 覆盖不到，进程直接退出）。
func TestPublishClickOnNilPublisherReportsAnError(t *testing.T) {
	var p *KafkaClickPublisher
	err := p.PublishClick(context.Background(), ClickEvent{EventID: "evt-1"})
	if err == nil {
		t.Fatal("nil publisher 必须返回错误，而不是 panic 或静默成功")
	}
}
