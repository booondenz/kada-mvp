// Package redirect 是公开的 /r/:code 路径——整个产品最热的一条路。
//
// 它和别的 handler 有两个不同：
//  1. **没有认证中间件**（谁都能点短链），取而代之的是最宽的一档限流。
//  2. 它要"记录点击"，而记录绝不能拖慢或影响这次跳转。
//
// 对照原项目：kada/internal/handler/redirect/handler.go（752 行，其中一多半是引导页和
// 二维码渲染）。MVP 只保留 302 跳转 + 访问密码 + 点击记录这三件事。
package redirect

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/handler/httpx"
	"kada-mvp/internal/infra/ua"
	"kada-mvp/internal/middleware"
)

// LinkService 是跳转路径需要的服务能力。
type LinkService interface {
	GetByCode(ctx context.Context, shortCode string) (*domain.LinkInfo, error)
	CheckPassword(ctx context.Context, shortCode, password string) (bool, *domain.LinkInfo, error)
	// LogClick 没有返回值，这一点很重要：跳转的响应不能因为"点击没记上"而失败。
	LogClick(ctx context.Context, event domain.ClickEvent)
}

// Handler 持有服务。
type Handler struct {
	svc LinkService
}

// NewHandler 构造 handler（已给）。
func NewHandler(svc LinkService) *Handler { return &Handler{svc: svc} }

// RegisterRoutes 挂载公开路由（已给）。
//
// redirectMW 可以为 nil（Redis 没起来时），nil 就**不挂**，而不是挂一个空函数：
// 一个"什么都不做"的中间件会让读代码的人以为限流生效了。
func (h *Handler) RegisterRoutes(r *gin.Engine, redirectMW gin.HandlerFunc) {
	rg := r.Group("/r")
	if redirectMW != nil {
		rg.Use(redirectMW)
	}

	rg.GET("/:code", h.Redirect)
	rg.POST("/:code/verify-password", h.VerifyPassword)
	// 访问确认仍然是 POST，而且留在跳转这一档限流下：
	// 它才是"让一次点击被算数"的那个请求，丢掉它等于丢掉一次真实访问，
	// 而它的流量最多只能是"服务了页面的那些跳转"的一小部分。
	rg.POST("/:code/visit", h.ConfirmVisit)
}

// evidenceKind 在响应发出**之前**判断这次命中是什么的证据（已给）。
//
// 一个被直接跳到目标地址的客户端永远不会运行我们的任何 JS，
// 所以它这次请求就是"曾经有过这样一次访问"的唯一证据——除非它的 UA 自称爬虫
// （浏览器从不这样自称）。而被服务了引导页的客户端是另一回事：页面加载后
// 会调用 /r/:code/visit 来确认自己，预取器不会走到那一步。
// 这就是为什么"是不是人"不能靠 UA 过滤回答（微信/QQ 的预取器穿的正是 App 自己的 UA）。
func evidenceKind(userAgent string, platform domain.Platform) domain.ClickKind {
	if ua.IsCrawler(userAgent) || ua.NeedsIntermediatePage(platform) {
		return domain.ClickRequest
	}
	return domain.ClickVisit
}

// record 补上"每个事件都要从请求里拿"的字段，然后交给服务（已给）。
//
// 两件事值得注意：
//   - 用**具名字段**构造结构体：这个结构体里有四个相邻的字符串字段，
//     按位置传参迟早会传反，而传反了照样编译通过。
//   - `go h.svc.LogClick(...)`：点击记录在**另一个 goroutine** 上跑，跳转不等它。
//     代价是这里 panic 会结束进程（Gin 的 recovery 只覆盖服务请求的那个 goroutine），
//     所以 LogClick 内部必须自己兜住 panic（见 service.LinkService.LogClick）。
func (h *Handler) record(c *gin.Context, event domain.ClickEvent) {
	event.IP = middleware.RealIP(c)
	go h.svc.LogClick(context.Background(), event)
}

// platformOf 取平台（已给的小helper）。
func platformOf(c *gin.Context) domain.Platform {
	return ua.Detect(c.GetHeader("User-Agent"))
}

// Redirect 跳转。
//
// TODO(练习 71)：实现它。分支顺序就是语义顺序：
//
//	1. code := c.Param("code")
//	2. link, err := h.svc.GetByCode(ctx, code)
//	   err != nil → 404 + 纯文本 "link not found or expired"
//	   （这里用 c.String 而不是 JSON：这个 URL 是给人点的，浏览器直接展示这段文字，
//	    而 `{"error":"..."}` 显示出来是一串让人困惑的符号。）
//	3. link.HasPassword → 200 + text/html + passwordPageHTML(code)
//	   （**在这一步之前不要记录点击**：还没证明访问者能打开它。
//	     密码页本身是"服务了一个页面"，不是一次访问。）
//	4. userAgent := c.GetHeader("User-Agent")；platform := ua.Detect(userAgent)
//	5. 平台需要中间页（ua.NeedsIntermediatePage(platform)）→
//	   - record 一条 Kind=domain.ClickRequest 的事件（页面被服务了，但还没人确认），
//	     Referer 用 c.GetHeader("Referer")（**这一行才是访问者真正的来路**）；
//	   - 回 200 + text/html + guidePageHTML(link)。
//	6. 否则（可以直接跳）→ record 一条 Kind=evidenceKind(userAgent, platform) 的事件，
//	   然后 c.Redirect(http.StatusFound, link.OriginalURL)。
//	7. ⚠ 顺序：先 record，再写响应。反过来的话，响应已经发出时再去 spawn
//	   goroutine 记录，在"进程紧接着退出"这种极端情况下更容易把点击丢掉。
//
// 关于 302 而不是 301：301 会被浏览器**永久**缓存，之后即使这条链接被停用、
// 被改目标，访问者也不会再回到我们这里——点击数直接归零，而且我们根本不知道。
// 这是这个 handler 里最容易写错、代价最大的一行。
//
// 验收：go test ./internal/handler/redirect/ -run TestRedirect
func (h *Handler) Redirect(c *gin.Context) {
	httpx.NotImplemented(c, "redirect.Redirect")
}

// VerifyPassword 校验访问密码。
//
// TODO(练习 72)：实现它。
//  1. 绑定 struct{ Password string `json:"password" binding:"required"` }（失败 → 400）。
//  2. ok, link, err := h.svc.CheckPassword(ctx, c.Param("code"), req.Password)。
//     err != nil → 404。
//  3. !ok → 401 {"error": "incorrect password"}。
//  4. ok → **现在才记录这次访问**（密码被证明是对方知道的），
//     然后 c.Redirect(http.StatusFound, link.OriginalURL)。
//     注意：记录时 kind 用 domain.ClickVisit——人已经通过了验证，
//     这次访问是确凿的；但 platform 仍然要从 UA 现算。
//
// 验收：go test ./internal/handler/redirect/ -run TestVerifyPassword
func (h *Handler) VerifyPassword(c *gin.Context) {
	httpx.NotImplemented(c, "redirect.VerifyPassword")
}

// ConfirmVisit 由页面自己调用，确认"这次访问真的有人在看"。
//
// TODO(练习 73)：实现它。
//  1. 先 GetByCode 拿到 link（拿不到 → 404）：这个接口收的是短码，需要一个 link ID 才能记事。
//  2. h.record(c, domain.ClickEvent{LinkID: link.ID, UserAgent: ..., Platform: ...,
//     Kind: domain.ClickVisit})。
//  3. 200 {"ok": true}。
//
// 为什么需要这个接口？因为微信/QQ 的预览抓取器和真实访客发的是同一个 UA，
// 唯一能把两者分开的信号是"页面里的 JS 有没有跑起来"——而只有真人浏览器会跑 JS。
// 预取器抓了 HTML 就走，永远不会发这个 POST。
//
// 注意它**不记录 referer**：这个 POST 的 Referer 是我们自己刚服务出去的那个页面，
// 存下来会把我们自己的域名顶到"来源"报表的第一名。真正的来源记在请求那一行上。
//
// 验收：go test ./internal/handler/redirect/ -run TestConfirmVisit
func (h *Handler) ConfirmVisit(c *gin.Context) {
	httpx.NotImplemented(c, "redirect.ConfirmVisit")
}

// 这个空引用是给你的实现准备的（net/http）：填空阶段它还没有被使用，
// 用它保持编译通过。写完删掉。
var _ = http.StatusFound

// guidePageHTML 渲染中间引导页（已给）。
//
// 三件事同时发生在这个页面上：
//  1. 把目标地址显示出来（微信里"点了没反应"时，用户至少能看到要去哪）；
//  2. 提供一个真正可点的入口（很多内置浏览器只允许用户手势触发的跳转）；
//  3. **页面加载后自己发一个 POST /r/:code/visit**，这就是"这次访问确实有人在看"
//     的唯一证据——预取器抓了 HTML 就走，永远不会执行到这里。
//
// 注意第 3 步用 fetch 且忽略失败：确认访问失败只该少算一次点击，
// 绝不该让访问者看到一个错误页面。
func guidePageHTML(link *domain.LinkInfo) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh"><head><meta charset="utf-8"><title>即将跳转</title>
<meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="font-family:system-ui;max-width:28rem;margin:4rem auto;padding:0 1rem">
<h1 style="font-size:1.25rem">即将打开</h1>
<p style="word-break:break-all;color:#374151">%s</p>
<a href="%s" style="display:block;margin-top:1rem;padding:.7rem;text-align:center;
   background:#4f46e5;color:#fff;text-decoration:none;border-radius:.5rem">继续访问</a>
<p style="color:#6b7280;font-size:.875rem;margin-top:1rem">如果按钮没有反应，请点右上角菜单选择「在浏览器中打开」。</p>
<script>
fetch('/r/' + %q + '/visit', {method: 'POST'}).catch(function () {});
</script>
</body></html>`, link.OriginalURL, link.OriginalURL, link.ShortCode)
}

// passwordPageHTML 渲染"输入访问密码"的页面（已给）。
//
// 它故意保持成一个内联的 HTML 字符串：这个页面只做一件事，
// 而引入一个模板引擎意味着多一个部署面（模板文件路径、缓存、转义规则）。
// 注意 code 是被 %q 引起来的 —— 短码会出现在 HTML 和 JS 里，
// 把它当字符串插值而不是当代码插值，是这类页面最基本的一条纪律。
func passwordPageHTML(code string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="zh"><head><meta charset="utf-8"><title>需要密码</title>
<meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="font-family:system-ui;max-width:24rem;margin:4rem auto;padding:0 1rem">
<h1 style="font-size:1.25rem">这个链接需要访问密码</h1>
<form id="f">
  <input id="p" type="password" placeholder="请输入访问密码" required
         style="width:100%%;padding:.6rem;font-size:1rem;box-sizing:border-box">
  <button type="submit" style="margin-top:.75rem;width:100%%;padding:.6rem;font-size:1rem;cursor:pointer">继续</button>
</form>
<p id="msg" style="color:#b91c1c"></p>
<script>
document.getElementById('f').addEventListener('submit', async function (e) {
  e.preventDefault();
  var res = await fetch('/r/' + %q + '/verify-password', {
    method: 'POST',
    headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({password: document.getElementById('p').value})
  });
  if (res.status === 401) { document.getElementById('msg').textContent = '密码不正确'; return; }
  if (!res.ok) { document.getElementById('msg').textContent = '链接不可用'; return; }
  // 校验通过后由后端 302，浏览器跟过去即可（fetch 会跟着重定向拿到最终响应）。
  // 这里直接用返回的最终 URL 跳转，避免把目标地址暴露在 URL 参数里。
  window.location.href = res.url;
});
</script>
</body></html>`, code)
}
