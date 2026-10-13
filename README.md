# kada-mvp：Kada 后端的「填空骨架」

这是把你自己的 [kada](../kada) 项目（Go + Gin + GORM + PostgreSQL + Redis + Kafka）拆成
**骨架 + 87 个填空 + 验收测试**的 MVP 版本，只做后端。

- 骨架**能编译、能启动**：配置解析、数据库/Redis 连接、路由注册、依赖注入、优雅退出、GORM 模型全都写好了。
- 每个空函数上方都有一段 `TODO(练习 N)` 注释：写清了**要做什么**、**为什么这么做**、以及
  原项目在这个点上踩过什么坑。这些注释本身就是教程，慢慢读。
- 每个模块都有**验收测试**：现在是红的，填对就绿。
- `solution` 分支是参考答案（同一份测试全绿）。卡住时 `git switch solution` 对照，再 `git switch main` 回来。

> 这个仓库是独立 module（`module kada-mvp`），不会影响原来的 kada 项目。

---

## 1. 快速开始

需要 Go 1.24+ 和 Docker。

```bash
cd kada-mvp

# 1) 起 PostgreSQL 和 Redis
docker compose up -d

# 2) 配置
cp .env.example .env

# 3) 建表（GORM AutoMigrate；结构体的唯一事实来源在 internal/domain/entity）
go run ./cmd/migrate

# 4) 起 API
go run ./cmd/server
# → Kada MVP API listening on :8080

# 5) 看一眼
curl -s localhost:8080/api/health
# {"service":"kada-mvp-api","status":"ok","version":"0.1.0"}
```

Kafka 是**可选**的：`KAFKA_BROKERS` 留空时，点击事件直接写数据库（降级模式），
整条链路照样跑通。想练 `cmd/worker` 时再单独起一个 Kafka，把 `KAFKA_BROKERS=localhost:9092` 填上。

### 现在能跑，但接口会回 501

骨架里没实现的 handler 会明确返回 `501 Not Implemented`，而不是假装成功：

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8080/api/links   # 501
```

这是刻意的：`/api/health` 和已实现的样板接口（`internal/handler/auth`）现在就能用，
其余接口在你填完之前不让"看起来像成功"。

---

## 2. 怎么知道自己填对了

```bash
# 全量验收（骨架态：大量 FAIL；填完：全部 ok）
go test ./...

# 只看某个模块
go test ./internal/middleware/ -v

# 还剩多少空没填
grep -rn "TODO(练习" --include=*.go . | wc -l

# 还有哪些占位还在生效（这些函数的测试一定是红的）
grep -rn "todo\." --include=*.go .
```

测试都写在**包内**（`package service`、`package middleware`…），因为要验证的很多东西
（`isSafeTarget`、`generateShortCode`、`linkInfoFromEntity`、`evidenceKind`…）本来就是非导出的。
这不是巧合：**能测的粒度是设计的一部分**，纯函数越多、依赖越窄，测试越容易写。

### 一个会浪费半天的坑：DryRun 下不要用 `Scan`

服务层的测试大多跑在 GORM 的 **DryRun** 会话上（`newDryRunDB`：只渲染 SQL，不真的连库）。
在 DryRun 下：

- `Find` / `First` / `Count` / `Pluck` / `Create` / `Updates` / `Delete` / `Exec` 都正常；
- **`Scan` / `Rows` / `Row` 会返回 `gorm.ErrDryRunModeUnsupported`**（它们走 `sql.Rows` 那条路，
  而 DryRun 没有 rows 可给）。症状是"你的 SQL 看起来完全正确，但函数返回了一个和 SQL 无关的错误"。

所以凡是**被 DryRun 测试覆盖**的函数，取多列结果时要用 `Find(&rows)`（或用 `Raw(...).Find(...)`）。
`internal/service/analytics_service.go`、`folder_service.go`、`workspace_service.go` 的注释里
都标了这一条。反过来，只会在真实数据库上跑的代码（例如 `auth_service.go` 里那句带 `RETURNING id`
的原子更新）用 `Scan` 完全没问题。

---

## 3. 填空路线

按依赖顺序走，每一步都能单独验收。练习号就是代码里的 `TODO(练习 N)`。

| 阶段 | 练习 | 文件 | 这一阶段在教什么 | 验收命令 |
|---|---|---|---|---|
| **0. 先读** | — | `config/`、`cmd/server/main.go`、`internal/infra/`、`internal/domain/`、`internal/handler/auth/` | 分层、对象图、唯一配置来源、AutoMigrate | — |
| **1. 入口与中间件** | 1–6 | `infra/ua/detect.go`、`middleware/auth.go`、`middleware/ratelimit.go`、`mq/click.go` | UA 识别（爬虫 vs 真人）、JWT 校验（算法混淆、`Abort`）、Redis 滑动窗口与 fail-open、Kafka 生产端与 nil 陷阱 | `go test ./internal/infra/ua/ ./internal/middleware/ ./internal/mq/` |
| **2. 缓存与点击落库** | 18–21、24、25 | `service/cache_service.go`、`service/click_store.go` | cache-aside 的 TTL 边界、否定缓存的键、幂等插入（`ON CONFLICT`）、只有 `visit` 才计数 | `go test ./internal/service/ -run 'TestCacheService\|TestWriteClick\|TestNullable'` |
| **3. 短链核心** | 26–46 | `service/link_service.go` | 归属校验写进 WHERE、短码生成、否定缓存只记"确实不存在"、singleflight、点击异步化 + panic 兜底 + 降级直写 | `go test ./internal/service/ -run 'TestGenerateShortCode\|TestIsSafeTarget\|TestLinkInfo\|TestGetByCode\|TestLogClick\|TestCacheLink\|TestGivenHelpers'` |
| **4. 认证** | 7–17 | `service/auth_service.go` | 图形验证码先烧后发、两级配额、原子消耗验证码、密码登录不泄露"这个号有没有账号"、bcrypt 的 72 字节边界 | `go test ./internal/service/ -run 'TestValidatePassword\|TestPasswordHashing\|TestRandom\|TestLoginWithPassword\|TestConsume\|TestUserByPhone'` |
| **5. 分析与组织** | 47–63 | `service/analytics_service.go`、`service/folder_service.go`、`service/tag_service.go`、`service/workspace_service.go` | 聚合必须 JOIN links、只算 `kind='visit'`、趋势补零、CRUD 的归属校验、slug 校验 | `go test ./internal/service/ -run 'TestOverview\|TestDailyTrend\|TestPlatformBreakdown\|TestKnownPlatforms\|TestValidateSlug\|TestOrganization\|TestTagList'` |
| **6. HTTP 层** | 64–88 | `handler/link`、`handler/redirect`、`handler/analytics`、`handler/folder`、`handler/tag`、`handler/workspace` | 绑定/校验、错误→状态码映射（401/409/404/429）、302 而不是 301、密码页与引导页、访问证据（`kind`） | `go test ./internal/handler/...` |
| **7. Kafka 消费者** | 89 | `cmd/worker/main.go` | 手动提交 offset、毒消息丢弃、超时 | `go test ./cmd/worker/` |

阶段 1 和阶段 2 可以互换；阶段 3 依赖阶段 2 的实现（缓存接口），阶段 6 依赖阶段 3–5。

> 两个小提示：
> - 练习编号里**没有 22 和 23**：那两个（`MarkMissing` / `ClearMissing`）已经作为"写缓存 + 失败记日志"的
>   样板给出，编号保留空缺，这样你对照笔记时不会错位。
> - `go test ./internal/handler/auth/` 一开始就是**绿的**：那是已给的 handler 样板，
>   它的测试同时也示范了 handler 测试的写法（假 service + `httptest`）。

### 冒烟测试（填得差不多之后）

```bash
# 取一张图形验证码；答案就画在 SVG 里，解码即可看到 4 位数字
curl -s localhost:8080/api/auth/captcha | python -c "import sys,json,base64;d=json.load(sys.stdin);print(d['captcha_id']);print(base64.b64decode(d['image'].split(',')[1]).decode())"

# 用上面的 captcha_id 和 4 位数字换一条短信验证码：
# 没配真实短信服务时，验证码会打在 cmd/server 的日志里（internal/service/sms.go 的 LogSMSSender）
curl -s -X POST localhost:8080/api/auth/send-sms-code -H 'Content-Type: application/json' \
  -d '{"phone":"13800138000","captcha_id":"<id>","captcha_code":"1234"}'

curl -s -X POST localhost:8080/api/auth/register -H 'Content-Type: application/json' \
  -d '{"phone":"13800138000","code":"<日志里的 6 位>","password":"pw12345678"}'

# 拿 token 建一条短链，然后确认它是 302
TOKEN=<上一步返回的 token>
curl -s -X POST localhost:8080/api/links -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"original_url":"https://example.com"}'
curl -s -o /dev/null -w '%{http_code} %{redirect_url}\n' localhost:8080/r/<short_code>
```

`/api/analytics/overview` 是检验"计数只在 `visit` 上累加"最快的地方：用 `curl -A 'Googlebot/2.1'`
访问一次短链，再对比普通浏览器访问一次，看 total_clicks 涨了几次。

---

## 4. 分层与目录

```text
HTTP 请求
   │
   ▼
middleware/     JWT 校验、限流、真实客户端 IP          ← 练习 1–4
   │
   ▼
handler/<资源>  绑定 + 校验 JSON、把服务错误映射成状态码  ← 练习 64–88
   │  （只依赖自己声明的一小片 service 接口，所以 handler 测试不需要数据库）
   ▼
service/<资源>  业务规则、归属校验、事务、缓存失效、点击记录 ← 练习 7–63
   │  （只依赖 *gorm.DB 和几个接口，不认识 gin）
   ▼
domain/entity   GORM 模型 == 数据库表结构（唯一事实来源）
```

```text
kada-mvp/
├── cmd/
│   ├── server/       API 入口：唯一组装对象图的地方（已给）
│   ├── worker/       Kafka 消费端（练习 89）
│   └── migrate/      只跑 AutoMigrate（已给）
├── config/           环境变量解析，全项目唯一的配置来源（已给）
├── internal/
│   ├── domain/       API 收发结构体、枚举、哨兵错误（已给）
│   │   └── entity/   GORM 模型（已给）
│   ├── infra/        database.go / redis.go（已给）、ua/（练习 1–2）
│   ├── middleware/   auth.go、ratelimit.go（练习 3–4）
│   ├── mq/           Kafka 生产端（练习 5–6）
│   ├── service/      业务逻辑（练习 7–63）
│   ├── handler/      HTTP 层（练习 64–88）
│   └── todo/         占位符工具（骨架专用，填完可以整个删掉）
├── docker-compose.yml  PostgreSQL + Redis
└── .env.example
```

三条纪律（原项目 `docs/design.md` 4.1 节，这里的代码结构就是照着它长的）：

1. **handler 不碰数据库**，它声明自己需要的那一小片接口，于是 handler 的测试不需要数据库、Redis、Kafka。
2. **service 不碰 `*gin.Context`**，只收 `context.Context` 和普通值。
3. **归属校验写在 `WHERE` 里**（`user_id = ?`），不是"查出来之后再判断"——别人的行根本不会被读出来。

---

## 5. 哪些是"已给"的，为什么

| 已给 | 理由 |
|---|---|
| `config/`、`infra/database.go`、`infra/redis.go` | 连接池参数、超时、`TranslateError` 这类东西是纯配置管道，填空学不到东西，但抄错会浪费一整天 |
| `cmd/server/main.go` | 它是唯一组装对象图的地方，也是"接口变量 vs nil 指针"那个 Go 陷阱的示范（见文件里的注释） |
| `internal/domain/**`、`internal/domain/entity/**` | 它们是填空的**题干**：字段、约束、`LinkInfo` 里为什么没有密码哈希字段，都在这里 |
| `internal/handler/auth/handler.go` | 一整份 handler 样板：绑定 → 调用 → 错误映射（400/401/409/429），其余 handler 照着它写 |
| `service/sms.go`（`SMSSender` + 日志实现） | 把云的 SMS SDK 抽成接口，让你没有阿里云账号也能把注册登录跑通 |
| `service/errors.go`、`todo/` | 小工具 |

---

## 6. 与原项目 kada 的差异（MVP 砍了什么）

| 原项目 | 这里 | 为什么 |
|---|---|---|
| 阿里云 PNVS 短信（`infra/sms/aliyun.go`） | `service.SMSSender` 接口 + 把验证码打到日志的开发实现 | 去掉云账号依赖，保留"验证码由谁生成、哈希怎么存"这些真问题 |
| 目录 / 标签 / 工作区 / 自定义域名 / UTM 模板 / API Token / AI 助手 | 保留目录、标签、工作区；其余砍掉 | 前三个是"标准 CRUD + 归属校验"的模板；域名验证 DNS、UTM 是纯 CRUD 复制；AI 助手（Eino）和 MVP 后端关系最弱 |
| Prometheus `/metrics`、二维码、OG 预览抓取 | 砍掉 | 都不影响主链路的结构；预览抓取另有 SSRF 防护要讲，属于进阶 |
| 引导页的 deeplink / 平台文案 | 保留**一张**引导页（`guidePageHTML`） | `kind`（visit/request）这套设计的全部意义就在这里，砍了它整条"什么算一次访问"就没法讲了 |
| 历史遗留列、`schema.Reconcile*` 兼容 DDL | 砍掉（从零建库用不到） | 但 `infra.Migrate` 的注释里保留了那个教训：**已有的数据库是第二个事实来源** |
| 前端（Vite/React） | 无 | 按你的要求只做后端 |

---

## 7. 进阶方向（填完之后）

- **原子性验证**：现在 `consumeCaptcha` 的测试只验证 SQL 形状。写一个带真 PostgreSQL 的集成测试
  （用 `testcontainers` 或本地实例）并发消耗同一个验证码，验证只有一次成功。
- **点击链路**：起一个 Kafka，跑 `cmd/server` + `cmd/worker`，验证 kill worker 再启动后 offset 语义、
  重复投递（故意重发同一条消息）不会重复计数。
- **可观测性**：加 `/metrics`（`prometheus/client_golang`），把跳转结果、缓存命中率、点击投递结果做成指标。
  注意原项目的两个决定：**结果名由"哪个分支回答"决定**、**延迟桶从 1ms 以下开始**。
- **`linkInfoFromEntity` 之外的泄露面**：给 `LinkInfo` 加一个字段，看 `TestLinkInfoFromEntityHidesPasswordHash`
  是不是照样绿（它用的是"序列化后不含明文"的检查，所以等价于一条防泄露的护栏）。
- **把 `internal/todo` 删掉**：当 `grep -rn "todo\." --include=*.go .` 没有任何输出时，这个包就该消失了。

---

## 8. 杂项

- **`.cache/`**：Go 的构建缓存和模块缓存被指到了项目内的 `.cache/gopath`、`.cache/go-build`
  （和原项目 kada 的做法一致），这样在磁盘/权限受限的环境里也能构建。它在 `.gitignore` 里，
  删掉只损失一次冷编译。正常环境不需要，用默认的 `GOCACHE`/`GOMODCACHE` 即可。
- **`internal/todo`**：骨架专用。`todo.Err` 让空函数返回一个明确的"未实现"错误，
  `todo.Bool/String/Int64` 是零值占位，`todo.Stub` 给"没有返回值"的地方留一行日志。
  它们存在的意义只有一个：**让"还没写"立刻可见，而不是伪装成成功**。
- **测试依赖**：`miniredis`（内存 Redis，用来验证限流和缓存的真实命令行为）、
  `go-sqlmock`（假数据库，用来验证事务里的 SQL 和幂等分支）。两者都只在 `_test.go` 里出现。
