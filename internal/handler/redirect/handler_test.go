package redirect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
)

func init() { gin.SetMode(gin.TestMode) }

const (
	uaChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"
	uaWeChat = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 MicroMessenger/8.0.40"
	uaGoogle = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
)

type fakeRedirectService struct {
	link    *domain.LinkInfo
	getErr  error
	passOK  bool
	passErr error

	mu          sync.Mutex
	events      []domain.ClickEvent
	gotPassword string
}

func (f *fakeRedirectService) GetByCode(context.Context, string) (*domain.LinkInfo, error) {
	return f.link, f.getErr
}

func (f *fakeRedirectService) CheckPassword(_ context.Context, _, password string) (bool, *domain.LinkInfo, error) {
	f.mu.Lock()
	f.gotPassword = password
	f.mu.Unlock()
	if f.passErr != nil {
		return false, nil, f.passErr
	}
	return f.passOK, f.link, nil
}

func (f *fakeRedirectService) LogClick(_ context.Context, e domain.ClickEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakeRedirectService) recorded() []domain.ClickEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.ClickEvent(nil), f.events...)
}

// waitForEvent 等点击记录落地。
//
// 不能只 sleep 一个"应该够用"的时长：记录是在 handler spawn 出来的 goroutine 上跑的
// （跳转不等它），sleep 要么太短（偶发失败）要么太长（测试变慢）。
func waitForEvent(t *testing.T, svc *fakeRedirectService) domain.ClickEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if events := svc.recorded(); len(events) > 0 {
			return events[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待点击记录超时：LogClick 没有被调用")
	return domain.ClickEvent{}
}

func newTestEngine(svc LinkService) *gin.Engine {
	r := gin.New()
	NewHandler(svc).RegisterRoutes(r, nil)
	return r
}

func get(r *gin.Engine, path, userAgent string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	req.Header.Set("Referer", "https://example.com/from")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func post(r *gin.Engine, path, body, userAgent string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func plainLink() *domain.LinkInfo {
	return &domain.LinkInfo{
		ID:          7,
		ShortCode:   "abc1234",
		OriginalURL: "https://example.com/target",
		IsActive:    true,
	}
}

// TestRedirect 对应练习 71。
func TestRedirect(t *testing.T) {
	svc := &fakeRedirectService{link: plainLink()}
	r := newTestEngine(svc)

	w := get(r, "/r/abc1234", uaChrome)

	// 302 而不是 301：301 会被浏览器永久缓存，之后链接被停用/改目标，
	// 访问者也不会再回到我们这里——点击数直接归零。
	if w.Code != http.StatusFound {
		t.Fatalf("状态码 = %d, want 302（不能用 301）", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://example.com/target" {
		t.Errorf("Location = %q", loc)
	}

	event := waitForEvent(t, svc)
	if event.LinkID != 7 {
		t.Errorf("记录的 LinkID = %d, want 7", event.LinkID)
	}
	if event.Kind != domain.ClickVisit {
		t.Errorf("Kind = %q, want %q（普通浏览器直接跳转，这次请求就是访问的证据）", event.Kind, domain.ClickVisit)
	}
	if event.Platform != domain.PlatformBrowser {
		t.Errorf("Platform = %q, want browser", event.Platform)
	}
	if event.Referer != "https://example.com/from" {
		t.Errorf("Referer = %q, want 请求头里的来源", event.Referer)
	}
}

// 找不到的短码：404 + 一段给人看的纯文本，而且**不记录点击**
// （库里没有这条链接，事件也没有 link_id 可指）。
func TestRedirectMissingLink(t *testing.T) {
	svc := &fakeRedirectService{getErr: domain.ErrLinkNotFound}
	r := newTestEngine(svc)

	w := get(r, "/r/nope123", uaChrome)
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, want 404", w.Code)
	}
	if strings.Contains(w.Body.String(), `{"error"`) {
		t.Errorf("对浏览器的错误页不该是 JSON: %s", w.Body.String())
	}
	if len(svc.recorded()) != 0 {
		t.Error("链接不存在时不该记录点击")
	}
}

// 爬虫仍然被 302（我们不拦它），但它那一行是 request，不是 visit。
func TestRedirectRecordsCrawlersAsRequests(t *testing.T) {
	svc := &fakeRedirectService{link: plainLink()}
	r := newTestEngine(svc)

	if w := get(r, "/r/abc1234", uaGoogle); w.Code != http.StatusFound {
		t.Fatalf("状态码 = %d, want 302", w.Code)
	}
	if event := waitForEvent(t, svc); event.Kind != domain.ClickRequest {
		t.Errorf("Kind = %q, want %q（UA 自称爬虫）", event.Kind, domain.ClickRequest)
	}
}

// 微信/QQ/小红书的内置浏览器拿到的是一张中间页，而不是 302。
// 这次请求记成 request：页面被服务了，但还没有人确认过。
func TestRedirectServesGuidePageForWeChat(t *testing.T) {
	svc := &fakeRedirectService{link: plainLink()}
	r := newTestEngine(svc)

	w := get(r, "/r/abc1234", uaWeChat)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（服务一张引导页）", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := w.Body.String()
	if !strings.Contains(body, "https://example.com/target") {
		t.Error("引导页里没有目标地址")
	}
	// 页面加载后会自己发这个 POST 来确认访问——预取器抓了 HTML 就走，不会走到这一步。
	if !strings.Contains(body, "/r/abc1234/visit") {
		t.Error("引导页里没有访问确认的调用")
	}

	if event := waitForEvent(t, svc); event.Kind != domain.ClickRequest {
		t.Errorf("Kind = %q, want %q", event.Kind, domain.ClickRequest)
	}
}

// 需要密码的链接：先把密码页给出去，**这一趟不算访问**。
func TestRedirectServesPasswordPageWithoutRecording(t *testing.T) {
	link := plainLink()
	link.HasPassword = true
	svc := &fakeRedirectService{link: link}
	r := newTestEngine(svc)

	w := get(r, "/r/abc1234", uaChrome)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（密码页）", w.Code)
	}
	if !strings.Contains(w.Body.String(), "password") {
		t.Error("返回的不是密码页")
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Errorf("不该发生跳转，Location = %q", loc)
	}

	time.Sleep(100 * time.Millisecond) // 给错误的实现一点时间去记
	if events := svc.recorded(); len(events) != 0 {
		t.Errorf("密码还没验证就记录了 %d 条点击：这一趟还不算访问", len(events))
	}
}

// TestVerifyPassword 对应练习 72。
func TestVerifyPassword(t *testing.T) {
	t.Run("密码正确才记录访问并跳转", func(t *testing.T) {
		svc := &fakeRedirectService{link: plainLink(), passOK: true}
		r := newTestEngine(svc)

		w := post(r, "/r/abc1234/verify-password", `{"password":"s3cret"}`, uaChrome)
		if w.Code != http.StatusFound {
			t.Fatalf("状态码 = %d, want 302: %s", w.Code, w.Body.String())
		}
		if loc := w.Header().Get("Location"); loc != "https://example.com/target" {
			t.Errorf("Location = %q", loc)
		}
		if svc.gotPassword != "s3cret" {
			t.Errorf("服务收到的密码 = %q", svc.gotPassword)
		}
		if event := waitForEvent(t, svc); event.Kind != domain.ClickVisit {
			t.Errorf("Kind = %q, want %q（密码被证明是对方知道的）", event.Kind, domain.ClickVisit)
		}
	})

	t.Run("密码错误则 401 且不记录", func(t *testing.T) {
		svc := &fakeRedirectService{link: plainLink(), passOK: false}
		r := newTestEngine(svc)

		w := post(r, "/r/abc1234/verify-password", `{"password":"wrong"}`, uaChrome)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("状态码 = %d, want 401", w.Code)
		}
		time.Sleep(100 * time.Millisecond)
		if len(svc.recorded()) != 0 {
			t.Error("密码错误时不该记录访问")
		}
	})

	t.Run("链接不存在则 404", func(t *testing.T) {
		svc := &fakeRedirectService{passErr: domain.ErrLinkNotFound}
		r := newTestEngine(svc)

		w := post(r, "/r/abc1234/verify-password", `{"password":"x"}`, uaChrome)
		if w.Code != http.StatusNotFound {
			t.Fatalf("状态码 = %d, want 404", w.Code)
		}
	})

	t.Run("缺少密码字段则 400", func(t *testing.T) {
		r := newTestEngine(&fakeRedirectService{link: plainLink(), passOK: true})
		if w := post(r, "/r/abc1234/verify-password", `{}`, uaChrome); w.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d, want 400", w.Code)
		}
	})
}

// TestConfirmVisit 对应练习 73。
//
// 这条路由存在的唯一理由：微信/QQ 的预览抓取器和真实访客发的是同一个 UA，
// 唯一能把两者分开的信号是"页面里的 JS 有没有跑起来"。
func TestConfirmVisit(t *testing.T) {
	svc := &fakeRedirectService{link: plainLink()}
	r := newTestEngine(svc)

	w := post(r, "/r/abc1234/visit", "", uaWeChat)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200: %s", w.Code, w.Body.String())
	}

	event := waitForEvent(t, svc)
	if event.Kind != domain.ClickVisit {
		t.Errorf("Kind = %q, want %q（自己确认的访问是确凿的）", event.Kind, domain.ClickVisit)
	}
	if event.Platform != domain.PlatformWechat {
		t.Errorf("Platform = %q, want wechat", event.Platform)
	}
	// 这个 POST 的 Referer 是我们自己刚服务出去的页面，存下来会把我们自己的域名
	// 顶到"来源"报表的第一名。
	if event.Referer != "" {
		t.Errorf("Referer = %q, want 空（不该记录我们自己的页面）", event.Referer)
	}
}

func TestConfirmVisitOnAMissingLink(t *testing.T) {
	svc := &fakeRedirectService{getErr: domain.ErrLinkNotFound}
	r := newTestEngine(svc)
	if w := post(r, "/r/abc1234/visit", "", uaChrome); w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, want 404", w.Code)
	}
}

// evidenceKind 是已给的判定函数，但它是"什么算一次访问"的唯一定义，值得钉住。
func TestEvidenceKind(t *testing.T) {
	cases := []struct {
		name     string
		ua       string
		platform domain.Platform
		want     domain.ClickKind
	}{
		{"普通浏览器", uaChrome, domain.PlatformBrowser, domain.ClickVisit},
		{"爬虫", uaGoogle, domain.PlatformBrowser, domain.ClickRequest},
		{"空 UA", "", domain.PlatformBrowser, domain.ClickRequest},
		{"微信内置浏览器（需要中间页）", uaWeChat, domain.PlatformWechat, domain.ClickRequest},
		{"微博（不需要中间页）", "Mozilla/5.0 weibo__13.8.0", domain.PlatformWeibo, domain.ClickVisit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evidenceKind(tc.ua, tc.platform); got != tc.want {
				t.Errorf("evidenceKind(%q, %q) = %q, want %q", tc.ua, tc.platform, got, tc.want)
			}
		})
	}
}
