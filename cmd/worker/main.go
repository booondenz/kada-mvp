// Command worker 是 Kafka 消费端：把点击事件写进数据库。
//
// 为什么点击要异步？因为短链跳转是热路径，而"把一次点击记下来"是可以慢一秒的事。
// 两者拆开之后，数据库再忙也不会让跳转失败。
//
// 这个文件的结构是**已给**的（消费循环、毒消息处理、优雅退出），
// 只有中间那一个函数要你填。
//
// 对照原项目：kada/cmd/worker/main.go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
	"gorm.io/gorm"

	"kada-mvp/config"
	"kada-mvp/internal/infra"
	"kada-mvp/internal/mq"
	"kada-mvp/internal/service"
)

// maxDeliveryAttempts 同一条消息最多被处理几次。
//
// 超过之后**提交 offset 并丢弃**，这是刻意的：这个消费组只有一个分区一个消费者，
// 一条永远处理不了的消息（"毒消息"）会把整个消费组永久堵死——
// 于是所有正常的点击都排在它后面，一条都写不进去。
// 丢一条坏消息的代价，比停掉整条管道小得多。
const maxDeliveryAttempts = 3

// isPermanentError 报告那些"重试多少次都一样"的错误（已给）。
//
// 两类：JSON 坏掉（重试还是坏的），以及外键不存在（链接已经被删了）。
// 除此之外的错误（数据库暂时连不上等）都应该重试。
func isPermanentError(err error) bool {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return true
	}
	// infra.NewDB 里开了 TranslateError，外键冲突会被翻译成这个哨兵。
	return errors.Is(err, gorm.ErrForeignKeyViolated)
}

// attemptTracker 在内存里记"这条消息（topic-partition-offset）试过几次"（已给）。
//
// 重启后计数会归零，这是可以接受的：它只影响"一条毒消息多被重试几次"。
// 换成 Redis 或数据库反而会给这条最热的路径加一个依赖。
type attemptTracker struct {
	mu   sync.Mutex
	seen map[string]int
}

func newAttemptTracker() *attemptTracker {
	return &attemptTracker{seen: make(map[string]int)}
}

func (t *attemptTracker) record(topic string, partition int, offset int64) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := fmt.Sprintf("%s-%d-%d", topic, partition, offset)
	t.seen[key]++
	return t.seen[key]
}

func (t *attemptTracker) reset(topic string, partition int, offset int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.seen, fmt.Sprintf("%s-%d-%d", topic, partition, offset))
}

// ensureTopic 幂等地创建 topic（已给：1 个分区、1 个副本）。
//
// **必须在创建 reader 之前调用**，这不是洁癖：如果 reader 在 topic 还在被自动创建的时候
// 加入消费组，kafka-go 会拿到一个空的分配结果并永远挂住
// （segmentio/kafka-go#585）。原项目为此写过一条注释，这里保留。
func ensureTopic(ctx context.Context, brokerList []string, topic string) error {
	conn, err := kafka.DialContext(ctx, "tcp", brokerList[0])
	if err != nil {
		return err
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		return err
	}
	ctrlConn, err := kafka.DialContext(ctx, "tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		return err
	}
	defer ctrlConn.Close()

	return ctrlConn.CreateTopics(kafka.TopicConfig{
		Topic:             topic,
		NumPartitions:     1,
		ReplicationFactor: 1,
	})
}

// decodeClickEvent 把消息解成事件（已给）。
//
// 单独一步是因为它是最典型的"毒消息"来源：JSON 坏掉的正确处理只有一种——丢弃。
func decodeClickEvent(msg []byte) (mq.ClickEvent, error) {
	var e mq.ClickEvent
	if err := json.Unmarshal(msg, &e); err != nil {
		return mq.ClickEvent{}, err
	}
	return e, nil
}

// processClickMessage 处理一条消息。
//
// TODO(练习 89)：实现它。
//
//  1. e, err := decodeClickEvent(msg)；err != nil → 直接返回该错误（会被判为毒消息）。
//  2. 给写入**加一个超时**：ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
//     然后 defer cancel()。
//     为什么？因为这个函数的调用方（下面的循环）用的是不带超时的 ctx。
//     如果数据库卡住，没有超时的话这一条会永远挂着，消费组也就停在那里。
//  3. return writer.WriteClick(ctx, e)
//
// 注意这里**故意不重试**：重试由外层循环控制（它才知道已经试了几次），
// 在函数内部再套一层重试会让"最多三次"变成"最多九次"，而那个数字就没人能说清了。
//
// 验收：go test ./cmd/worker/ -run TestProcessClickMessage
func processClickMessage(msg []byte, writer service.ClickWriter) error {
	e, err := decodeClickEvent(msg)
	if err != nil {
		return err
	}

	// 调用方的 ctx 没有超时；没有这一条，数据库卡住时这个消费者会永远挂着。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return writer.WriteClick(ctx, e)
}

func main() {
	databaseURL := os.Getenv("DATABASE_URL")
	topic := os.Getenv("KAFKA_TOPIC")
	if topic == "" {
		topic = "clicks"
	}
	brokerList := config.SplitBrokers(os.Getenv("KAFKA_BROKERS"))
	if len(brokerList) == 0 {
		log.Fatal("KAFKA_BROKERS is required for worker")
	}

	// 先确保 topic 存在：冷启动时 broker 可能还没就绪，所以带退避重试；
	// 一直失败就退出——否则我们会永远拿着一个空分配结果挂在那里，而且看起来"在运行"。
	var topicErr error
	for attempt := 1; attempt <= 10; attempt++ {
		topicCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		topicErr = ensureTopic(topicCtx, brokerList, topic)
		cancel()
		if topicErr == nil {
			break
		}
		log.Printf("ensure kafka topic %q failed (attempt %d/10): %v", topic, attempt, topicErr)
		time.Sleep(2 * time.Second)
	}
	if topicErr != nil {
		log.Fatalf("cannot ensure kafka topic %q exists: %v", topic, topicErr)
	}

	db, err := infra.NewDB(databaseURL)
	if err != nil {
		log.Fatalf("database connect failed: %v", err)
	}
	defer infra.CloseDB(db)

	store := service.NewClickStore(db)
	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers: brokerList,
		Topic:   topic,
		GroupID: "click-worker",
		// MinBytes 保持 1：一条点击事件大约 200 字节，
		// 调大会让它攒批，从而给消费延迟加上最多 MaxWait（默认 10 秒）。
		MinBytes: 1,
		MaxBytes: 10e6,
	})
	defer reader.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("click-worker consuming topic %q from %v", topic, brokerList)
	tracker := newAttemptTracker()

	for {
		// 用 FetchMessage 而不是 ReadMessage：ReadMessage 会**自动提交 offset**，
		// 于是"处理失败"也会被提交，那条点击就永远丢了。
		// 这里只有写库成功之后才提交。
		m, err := reader.FetchMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.Println("shutdown signal received")
				return
			}
			log.Printf("fetch message failed: %v", err)
			continue
		}

		procErr := processClickMessage(m.Value, store)
		if procErr == nil {
			tracker.reset(m.Topic, m.Partition, m.Offset)
			if err := reader.CommitMessages(ctx, m); err != nil {
				log.Printf("commit message failed: %v", err)
			}
			continue
		}

		attempts := tracker.record(m.Topic, m.Partition, m.Offset)
		if isPermanentError(procErr) || attempts >= maxDeliveryAttempts {
			// 毒消息，或者重试用尽：提交 offset 跳过它，
			// 否则这个单分区消费组会被同一条消息永远堵住。
			log.Printf("dropping click message after %d attempts: %v", attempts, procErr)
			if err := reader.CommitMessages(ctx, m); err != nil {
				log.Printf("commit message failed: %v", err)
			}
			tracker.reset(m.Topic, m.Partition, m.Offset)
			continue
		}

		log.Printf("process message failed (attempt %d/%d): %v", attempts, maxDeliveryAttempts, procErr)
		time.Sleep(time.Second) // 短暂退避，避免空转
	}
}
