package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
)

func init() { gin.SetMode(gin.TestMode) }

// fakeAuthService 记住被调用的参数并返回预设结果。
//
// 这就是"handler 自己声明它需要的那一小片接口"的回报：
// handler 的测试不需要数据库、Redis、短信服务，也不需要 JWT 密钥。
type fakeAuthService struct {
	captcha     *domain.CaptchaResponse
	captchaErr  error
	sendErr     error
	registerRes *domain.AuthResponse
	registerErr error
	loginRes    *domain.AuthResponse
	loginErr    error
	meRes       *domain.UserInfo
	meErr       error

	gotSendPhone      string
	gotSendIP         string
	gotCaptchaID      string
	gotCaptchaAnswer  string
	gotRegisterParams [3]string
	gotLoginPhone     string
	gotLoginPassword  string
	gotUserID         int64
}

func (f *fakeAuthService) GenerateCaptcha(context.Context, string) (*domain.CaptchaResponse, error) {
	return f.captcha, f.captchaErr
}

func (f *fakeAuthService) SendSMSCode(_ context.Context, phone, ip, captchaID, captchaAnswer string) error {
	f.gotSendPhone, f.gotSendIP, f.gotCaptchaID, f.gotCaptchaAnswer = phone, ip, captchaID, captchaAnswer
	return f.sendErr
}

func (f *fakeAuthService) Register(_ context.Context, phone, code, password string) (*domain.AuthResponse, error) {
	f.gotRegisterParams = [3]string{phone, code, password}
	return f.registerRes, f.registerErr
}

func (f *fakeAuthService) LoginWithPassword(_ context.Context, phone, password string) (*domain.AuthResponse, error) {
	f.gotLoginPhone, f.gotLoginPassword = phone, password
	return f.loginRes, f.loginErr
}

func (f *fakeAuthService) LoginByPhone(context.Context, string, string) (*domain.AuthResponse, error) {
	return f.loginRes, f.loginErr
}

func (f *fakeAuthService) GetUserByID(_ context.Context, userID int64) (*domain.UserInfo, error) {
	f.gotUserID = userID
	return f.meRes, f.meErr
}

// newTestRouter 挂上真实的注册逻辑（RegisterRoutes 是已给的），
// 只把中间件换成"已经登录"的替身。
func newTestRouter(svc AuthService) *gin.Engine {
	r := gin.New()
	h := NewHandler(svc)
	authMW := func(c *gin.Context) { c.Set("user_id", int64(42)); c.Next() }
	h.RegisterRoutes(r.Group("/api"), authMW)
	return r
}

func postJSON(r *gin.Engine, path, body string, headers ...[2]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCaptchaIsNotCacheable(t *testing.T) {
	svc := &fakeAuthService{captcha: &domain.CaptchaResponse{ID: "cid", Image: "data:image/svg+xml;base64,AAA"}}
	r := newTestRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/captcha", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	// 一份被缓存下来的挑战，等于把同一个未解的题交给了代理后面的所有人。
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应不是 JSON: %v", err)
	}
	if body["captcha_id"] != "cid" || body["image"] == "" {
		t.Errorf("响应 = %v", body)
	}
}

// 配额用尽必须是 429 + Retry-After，而不是 400 或 500。
// 报成 500 会让客户端重试一个永远失败的请求，也会让运维去找一个不存在的故障。
func TestSendSMSCodeMapsQuotaTo429(t *testing.T) {
	svc := &fakeAuthService{sendErr: &domain.RateLimitError{Message: "slow down", RetryAfterSeconds: 30}}
	r := newTestRouter(svc)

	w := postJSON(r, "/api/auth/send-sms-code",
		`{"phone":"13800138000","captcha_id":"cid","captcha_code":"1234"}`,
		[2]string{"X-Real-IP", "203.0.113.9"})

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("状态码 = %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want \"30\"", got)
	}
	if !strings.Contains(w.Body.String(), "retry_after_seconds") {
		t.Errorf("响应体里没有 retry_after_seconds: %s", w.Body.String())
	}
	// IP 必须取自 X-Real-IP（nginx 重写的那个头），而不是客户端可以伪造的 X-Forwarded-For。
	if svc.gotSendIP != "203.0.113.9" {
		t.Errorf("服务收到的 IP = %q, want 203.0.113.9", svc.gotSendIP)
	}
}

func TestSendSMSCodeMapsBadCaptchaTo400(t *testing.T) {
	svc := &fakeAuthService{sendErr: errors.New("the graphical verification code is incorrect")}
	r := newTestRouter(svc)

	w := postJSON(r, "/api/auth/send-sms-code",
		`{"phone":"13800138000","captcha_id":"cid","captcha_code":"0000"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400", w.Code)
	}
}

func TestSendSMSCodeRequiresAllFields(t *testing.T) {
	r := newTestRouter(&fakeAuthService{})
	// 少一个字段就根本不该调到服务：验证码是这个接口的第一道闸门。
	w := postJSON(r, "/api/auth/send-sms-code", `{"phone":"13800138000"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400", w.Code)
	}
}

// "这个号码已经有账号了"不是输入错误，而是一个可以去登录的提示：409。
func TestRegisterMapsAlreadyRegisteredTo409(t *testing.T) {
	svc := &fakeAuthService{registerErr: domain.ErrAlreadyRegistered}
	r := newTestRouter(svc)

	w := postJSON(r, "/api/auth/register", `{"phone":"13800138000","code":"123456","password":"pw12345678"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("状态码 = %d, want 409", w.Code)
	}
	if svc.gotRegisterParams[0] != "13800138000" || svc.gotRegisterParams[2] != "pw12345678" {
		t.Errorf("服务收到的参数 = %v", svc.gotRegisterParams)
	}
}

func TestRegisterReturnsToken(t *testing.T) {
	svc := &fakeAuthService{registerRes: &domain.AuthResponse{
		Token: "jwt-token",
		User:  domain.UserInfo{ID: 1, Phone: "13800138000"},
	}}
	r := newTestRouter(svc)

	w := postJSON(r, "/api/auth/register", `{"phone":"13800138000","code":"123456","password":"pw12345678"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "jwt-token") {
		t.Errorf("响应里没有 token: %s", w.Body.String())
	}
}

// 密码登录失败一律 401，而且三种失败（没这个号码 / 密码不对 / 还没有密码）
// 由服务层统一成一个错误——handler 这边只负责把它翻成 401。
func TestLoginMapsInvalidCredentialsTo401(t *testing.T) {
	svc := &fakeAuthService{loginErr: domain.ErrInvalidCredentials}
	r := newTestRouter(svc)

	w := postJSON(r, "/api/auth/login", `{"phone":"13800138000","password":"wrong"}`)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d, want 401", w.Code)
	}
	if svc.gotLoginPassword != "wrong" {
		t.Errorf("服务收到的密码 = %q", svc.gotLoginPassword)
	}
}

func TestLoginRejectsMalformedBody(t *testing.T) {
	r := newTestRouter(&fakeAuthService{})
	if w := postJSON(r, "/api/auth/login", `{"phone":"13800138000"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400", w.Code)
	}
}

// /me 的用户身份只能来自中间件写进 context 的 user_id，不能来自请求里的任何字段。
func TestGetMeUsesTheUserIDFromTheMiddleware(t *testing.T) {
	svc := &fakeAuthService{meRes: &domain.UserInfo{ID: 42, Phone: "13800138000"}}
	r := newTestRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200", w.Code)
	}
	if svc.gotUserID != 42 {
		t.Errorf("服务收到的 user_id = %d, want 42（必须来自中间件）", svc.gotUserID)
	}
}

func TestGetMeReturns404WhenTheAccountIsGone(t *testing.T) {
	svc := &fakeAuthService{meErr: errors.New("not found")}
	r := newTestRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d, want 404", w.Code)
	}
}
