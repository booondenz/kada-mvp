package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/todo"
)

// missingTTL 是一个"查过、不存在"的短码会被记住多久。
//
// 它比正常链接的 TTL 短得多，这是有意的：这条记录存在的目的是
// **不让一个扫描器把每次请求都变成一次数据库查询**，它不是事实来源。
// 它每多存在一秒，就多一秒"刚刚创建的短码被它挡住"的风险。

const missingTTL = 60 * time.Second

// LinkCache 是 LinkService 需要缓存提供的能力。
//
// 为什么抽成接口？因为跳转路径的测试不该依赖 Redis。原项目用这个接口
// 让"缓存命中 / 未命中 / 记住不存在"这几条分支都能在内存里验证
// （见原项目 link_cache_test.go 与这里的 cache 相关测试）。
//
// ⚠ 一个必须记住的 Go 陷阱：**不要把 (*CacheService)(nil) 存进这个接口**。
// 接口的值是 (类型, 指针) 两元组，只要类型不为空，接口本身就不是 nil——
// 于是 `if cache != nil` 全部通过，然后在方法体里解引用空指针。
// 正确做法是让变量本身就是接口类型，并且只在真正构造成功时才赋值（见 cmd/server/main.go）。
type LinkCache interface {
	GetLink(ctx context.Context, shortCode string) (*domain.LinkInfo, bool)
	SetLink(ctx context.Context, info *domain.LinkInfo)
	InvalidateLink(ctx context.Context, shortCode string)
	MarkMissing(ctx context.Context, shortCode string)
	ClearMissing(ctx context.Context, shortCode string)
	IsMissing(ctx context.Context, shortCode string) bool
}

// CacheService 是基于 Redis 的缓存实现。
type CacheService struct {
	client *redis.Client
	ttl    time.Duration
}

// NewCacheService 构造缓存服务（已给）。默认 TTL 10 分钟。
func NewCacheService(client *redis.Client) *CacheService {
	return &CacheService{client: client, ttl: 10 * time.Minute}
}

// key 拼缓存键。
//
// TODO(练习 18)：实现它。格式：fmt.Sprintf("cache:%s:%s", prefix, identifier)。
// 前缀是必须的：Redis 是一个共用命名空间，限流用的键是 "ratelimit:..."，
// 少了前缀迟早会撞上。
func (cs *CacheService) key(prefix, identifier string) string {
	return todo.String("CacheService.key")
}

// GetLink 按短码取链接。
//
// TODO(练习 19)：实现它。
//
//  1. cs.client.Get(ctx, cs.key("link", shortCode)).Bytes()
//  2. 任何错误（包括 redis.Nil 表示键不存在）→ return nil, false。
//     缓存读取失败永远不该变成业务失败：未命中的代价只是一次数据库查询。
//  3. json.Unmarshal 失败 → 同样 return nil, false。
//     为什么不返回错误？因为这里的调用方只有一种合理反应——当它没有缓存。
//     把"缓存坏了"和"业务错了"分成两条错误路径，只会让热路径多一个分支。
//  4. 成功 → &info, true。
//
// 验收：go test ./internal/service/ -run TestCacheServiceLinkRoundTrip
func (cs *CacheService) GetLink(ctx context.Context, shortCode string) (*domain.LinkInfo, bool) {
	return nil, false
}

// SetLink 缓存一条链接。
//
// TODO(练习 20)：实现它。这里有个**容易被忽略的正确性问题**：
//
//  1. 基础 TTL 是 cs.ttl（10 分钟）。
//  2. 但 cache-aside 缓存绝不能比它缓存的事实活得更久：
//     如果链接 3 分钟后过期，而你把它缓存 10 分钟，那么"已过期"这件事
//     会晚 7 分钟才生效——访问者会一直跳转到一个本该失效的链接上。
//     所以 TTL 要取 min(cs.ttl, time.Until(*info.ExpiresAt))，
//     并且如果算出来 <= 0，直接**不缓存**。
//     注意 info.ExpiresAt 是 *time.Time（可以为 nil = 永不过期）。
//  3. json.Marshal 失败 → 直接 return（缓存写失败不该影响主流程）。
//  4. cs.client.Set(ctx, key, data, ttl)。
//     写入失败要 log 一行：静默失败会让"缓存好像没生效"变成一个查不出来的问题。
//
// 验收：go test ./internal/service/ -run TestCacheServiceSetLinkRespectsExpiry
func (cs *CacheService) SetLink(ctx context.Context, info *domain.LinkInfo) {
	todo.Panic("CacheService.SetLink")
}

// InvalidateLink 让一条链接的缓存失效。
//
// TODO(练习 21)：实现它。**两个键都要删**：
//   - cache:link:<code>
//   - cache:linkmiss:<code>
//
// 为什么第二个也要删？因为一个刚刚被创建/改名的短码，可能还挂着一条
// "这个码不存在"的记录（60 秒）。不删它，新链接在最多 60 秒内会被自己的否定缓存挡住。
//
// 删除失败要 log，不能静默吞掉：跳转路径信任缓存里的"是否需要密码"这个字段，
// 一次失败的 DEL 意味着旧的（可能不带密码的）入口还会继续生效到 TTL 结束。
//
// 验收：go test ./internal/service/ -run TestCacheServiceInvalidateClearsBothKeys
func (cs *CacheService) InvalidateLink(ctx context.Context, shortCode string) {
	todo.Panic("CacheService.InvalidateLink")
}

// missingKey 是"这个短码不存在"用的独立键（已给）。
//
// 为什么不复用 link 键、往里面存一个特殊值？因为那样读回来要当 LinkInfo 解析，
// 而 JSON 的 "null" 解码之后和"缓存了一条空链接"完全无法区分。
func (cs *CacheService) missingKey(shortCode string) string {
	return cs.key("linkmiss", shortCode)
}

// MarkMissing 记住"这个短码查过了，不存在"（已给：写法就是"写缓存 + 失败记日志"的标准形状）。
func (cs *CacheService) MarkMissing(ctx context.Context, shortCode string) {
	if err := cs.client.Set(ctx, cs.missingKey(shortCode), "1", missingTTL).Err(); err != nil {
		log.Printf("remember missing short code %q failed: %v", shortCode, err)
	}
}

// ClearMissing 删掉"这个短码不存在"的记录（已给）。
func (cs *CacheService) ClearMissing(ctx context.Context, shortCode string) {
	if err := cs.client.Del(ctx, cs.missingKey(shortCode)).Err(); err != nil {
		log.Printf("clear missing short code %q failed: %v", shortCode, err)
	}
}

// IsMissing 报告这个短码是否被记为不存在。
//
// TODO(练习 24)：实现它。用 Exists 判断，注意这条要求：
//
//	**Redis 出错时必须返回 false**，而不是 true。
//	因为 true 的含义是"别查数据库了，这个码不存在"——把一次 Redis 故障
//	翻译成"所有短码都不存在"，是在故障期间把所有用户挡在门外，
//	而且这个错误的结论还会按 TTL 活过故障本身。返回 false 的代价只是多一次数据库查询。
//
// 验收：go test ./internal/service/ -run TestCacheServiceMissing
func (cs *CacheService) IsMissing(ctx context.Context, shortCode string) bool {
	return todo.Bool("CacheService.IsMissing")
}

// 编译期保证：(已给) 这个断言把你实现的方法和接口绑在一起——
// 只要你少实现一个方法，或者签名写错，编译就会在这里失败，而不是在调用处。
var _ LinkCache = (*CacheService)(nil)

// 这两个空引用是给你的实现准备的（encoding/json / fmt）：
// 填空阶段它们还没有被使用，用它们保持编译通过。写完删掉。
var (
	_ = json.Marshal
	_ = fmt.Sprintf
)
