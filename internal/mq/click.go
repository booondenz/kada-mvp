// Package mq 是"点击事件"的生产端：把一次点击变成一条 Kafka 消息。
//
// 为什么点击要绕一圈 Kafka？因为短链跳转是热路径、分析是冷路径：
// 跳转必须快，而且**不能因为数据库忙就失败**；而把点击数上去晚一秒完全可以接受。
// 整条架构就是沿这条线切开的。
//
// 对照原项目：kada/internal/mq/click.go
package mq

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"

	"kada-mvp/internal/todo"
)

// ClickEvent 是一次点击的消息载荷：生产端（API）和消费端（worker）共用同一个结构体。
//
// 共用一个结构体是刻意的：如果两端各写一个，那么"字段改了但只改了一边"这种错误
// 只会在运行时以反序列化出零值的形式出现，而那种错误看起来像"数据丢了"。
type ClickEvent struct {
	EventID   string `json:"event_id"` // 幂等键，worker 靠它去重
	LinkID    int64  `json:"link_id"`
	IP        string `json:"ip"`
	UserAgent string `json:"user_agent"`
	Platform  string `json:"platform"`
	// Kind 说明这条事件"是什么的证据"（domain.ClickKind）。
	// 它跟着事件走，而不是在写入时现算：只有请求所在的位置知道
	// 页面是被服务了（可能是预取）还是客户端直接去了目标地址。
	Kind      string    `json:"kind"`
	Referer   string    `json:"referer"`
	CreatedAt time.Time `json:"created_at"`
}

// ClickPublisher 是生产端接口。
//
// 抽成接口是为了两件事：测试里可以塞一个假实现；以及**降级**——
// 没有 Kafka 时这个变量保持 nil，service 会走"直接写库"的分支。
type ClickPublisher interface {
	PublishClick(ctx context.Context, e ClickEvent) error
}

// messageWriter 把 kafka.Writer 的写入方法缩窄成一行。
//
// 为什么不直接用 *kafka.Writer？因为那样测试就必须连一个真 broker。
// 缩窄成接口之后，`mq` 包自己的测试可以塞一个只把消息存进切片的假实现
// （见 click_test.go）。这是"为可测试性缩小依赖面"的最小示例。
type messageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// KafkaClickPublisher 是基于 segmentio/kafka-go 的实现。
type KafkaClickPublisher struct {
	writer messageWriter
}

// NewKafkaClickPublisher 构造一个 publisher；brokers 为空时返回 nil（= Kafka 未启用）。
//
// TODO(练习 5)：实现它。
//
// 要求：
//  1. 先过滤掉 broker 列表里的空白项（config.SplitBrokers 已经做了一次，
//     但这里再挡一次：一个 "" broker 会让 kafka-go 在第一次发送时才报错）。
//  2. 过滤后为空 → 返回 nil。**这个 nil 是有语义的**：调用方据此判断"没有 Kafka，
//     走直接写库的降级路径"。
//  3. 否则返回 &KafkaClickPublisher{writer: &kafka.Writer{...}}，需要的字段：
//     Addr: kafka.TCP(validBrokers...), Topic: topic,
//     Balancer: &kafka.LeastBytes{}, RequiredAcks: kafka.RequireOne。
//
// 为什么 RequiredAcks 用 RequireOne 而不是 RequireAll？点击是"量"的生意：
// 一次点击丢了对全局统计的影响可以忽略，但每次都等所有副本确认会拖慢热路径。
// 幂等性由 event_id 保证，不靠 broker 的复制因子。
//
// 验收：go test ./internal/mq/ -run TestNewKafkaClickPublisher
func NewKafkaClickPublisher(brokers []string, topic string) *KafkaClickPublisher {
	_ = strings.TrimSpace
	return nil
}

// PublishClick 把事件序列化成 JSON 写进 topic。
//
// TODO(练习 6)：实现它。
//
// 要求：
//  1. **第一件事是判 p == nil**，返回一个 error（例如 errors.New("the kafka publisher is not configured")）。
//     这一行不是防御性编程的花架子，原项目为此挂过一次线上进程，原因是 Go 里一个经典陷阱：
//     `var pub mq.ClickPublisher` 保持 nil 是安全的，但把 `(*KafkaClickPublisher)(nil)`
//     存进这个接口之后，接口本身**不再是 nil**——所有 `if pub != nil` 的判断都会通过，
//     然后在方法体里解引用一个 nil 指针。
//  2. json.Marshal(e)，出错返回该错误。
//  3. p.writer.WriteMessages(ctx, kafka.Message{Value: b})。
//
// 验收：go test ./internal/mq/ -run TestPublishClick
func (p *KafkaClickPublisher) PublishClick(ctx context.Context, e ClickEvent) error {
	// 这两行只是为了让 encoding/json 和 errors 的 import 在填空阶段不报错
	// （实现里会用到 json.Marshal 和 errors.New），写完删掉：
	_, _ = json.Marshal, errors.New
	_, _ = ctx, e
	return todo.Err("mq.KafkaClickPublisher.PublishClick")
}

// Close 关闭底层 writer（已给）。
func (p *KafkaClickPublisher) Close() error {
	if p == nil {
		return nil
	}
	w, ok := p.writer.(*kafka.Writer)
	if !ok {
		return nil
	}
	return w.Close()
}
