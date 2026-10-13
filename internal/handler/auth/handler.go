// Package auth 是认证接口的 HTTP 层。
//
// 这个包是**已实现**的样板：handler 层该长什么样，看它就够了。
// 其余 handler 包（link / redirect / analytics / folder / tag / workspace）
// 都是同一个形状的复制，只是资源不同。
//
// handler 层的三条纪律（原项目 docs/design.md 4.1 节）：
//  1. **handler 不碰数据库**。它只声明自己需要的那一小片服务接口（下面这个 AuthService），
//     所以 handler 的测试不需要数据库、不需要 Redis——塞一个假实现就行。
//  2. **只做三件事**：绑定 + 校验 JSON、调用服务、把服务错误映射成 HTTP 状态码。
//  3. **业务规则不在这里**。判断"这个短码能不能改"是 service 的事；
//     handler 负责的是"这句话翻译成 400 还是 409"。
package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/middleware"
)

// AuthService 是 handler 需要的服务能力（接口由 handler 自己声明，见包注释第 1 条）。
type AuthService interface {
	GenerateCaptcha(ctx context.Context, ip string) (*domain.CaptchaResponse, error)
	SendSMSCode(ctx context.Context, phone, ip, captchaID, captchaAnswer string) error
	Register(ctx context.Context, phone, code, password string) (*domain.AuthResponse, error)
	LoginWithPassword(ctx context.Context, phone, password string) (*domain.AuthResponse, error)
	LoginByPhone(ctx context.Context, phone, code string) (*domain.AuthResponse, error)
	GetUserByID(ctx context.Context, userID int64) (*domain.UserInfo, error)
}

// Handler 持有服务。
type Handler struct {
	svc AuthService
}

// NewHandler 构造 handler（已给）。
func NewHandler(svc AuthService) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes 挂载认证相关路由（已给）。
//
// 两组的区别是这一节的全部内容：
//   - public：不要求登录，但挂 strict 限流（登录/注册/发码是唯一"猜中了就有收益"的地方）。
//   - auth：要求登录（authMW 校验 JWT）。
//
// 注意路由的挂载方式：每个 handler 自己拥有自己的路由表，所以"加一个资源"
// 等于"加一个包 + main.go 里一行"，而不是去改一个中心路由清单（那种清单迟早会冲突）。
func (h *Handler) RegisterRoutes(r *gin.RouterGroup, authMW gin.HandlerFunc, strictMW ...gin.HandlerFunc) {
	public := r.Group("")
	if len(strictMW) > 0 && strictMW[0] != nil {
		public.Use(strictMW[0])
	}
	public.GET("/auth/captcha", h.Captcha)
	public.POST("/auth/send-sms-code", h.SendSMSCode)
	public.POST("/auth/register", h.Register)
	public.POST("/auth/login", h.Login)
	public.POST("/auth/login-by-phone", h.LoginByPhone)

	authed := r.Group("").Use(authMW)
	authed.GET("/me", h.GetMe)
}

// Captcha 发放图形验证码。
//
// 它是一个 GET：对调用方的会话没有副作用、可以安全重试，
// 而它创建的那一行本身就是一次性且 5 分钟过期的。
func (h *Handler) Captcha(c *gin.Context) {
	challenge, err := h.svc.GenerateCaptcha(c.Request.Context(), middleware.RealIP(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 图片是 data URI，挑战是按请求发的：一份被缓存下来的挑战就等于
	// 同一个未解的题被交给了代理后面的所有人。
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"captcha_id": challenge.ID, "image": challenge.Image})
}

// SendSMSCode 发送短信验证码。
func (h *Handler) SendSMSCode(c *gin.Context) {
	var req domain.SendSMSRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a valid phone number and the graphical verification code are required"})
		return
	}

	// IP 取自和限流器同一个地方（X-Real-IP，由 nginx 用 $remote_addr 重写）。
	// 这里如果去读 X-Forwarded-For，调用方就能通过自己发一个头来选择"被算进哪个配额"。
	if err := h.svc.SendSMSCode(c.Request.Context(), req.Phone, middleware.RealIP(c), req.CaptchaID, req.CaptchaCode); err != nil {
		// 配额用尽是 429 + Retry-After；验证码错、号码格式错是 400。
		// 把"请求太多"报成 500 会让客户端重试一个永远失败的请求，
		// 也会让运维去找一个并不存在的服务故障。
		var limited *domain.RateLimitError
		if errors.As(err, &limited) {
			if limited.RetryAfterSeconds > 0 {
				c.Header("Retry-After", strconv.Itoa(limited.RetryAfterSeconds))
			}
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":               limited.Message,
				"retry_after_seconds": limited.RetryAfterSeconds,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "verification code sent", "phone": req.Phone})
}

// Register 注册（或给一个还没有密码的老账号补上密码）。
func (h *Handler) Register(c *gin.Context) {
	var req domain.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "a phone number, the verification code and a password are required"})
		return
	}

	resp, err := h.svc.Register(c.Request.Context(), req.Phone, req.Code, req.Password)
	if err != nil {
		// 唯一值得单独一个状态码的错误：这个号码已经有密码了。
		// 它不是"注册失败"，而是"请去登录"——400 会对一个完全正确的输入说"你的输入有问题"。
		if errors.Is(err, domain.ErrAlreadyRegistered) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": resp.Token, "user": resp.User})
}

// Login 用密码登录。
func (h *Handler) Login(c *gin.Context) {
	var req domain.PasswordLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone number and password are required"})
		return
	}

	resp, err := h.svc.LoginWithPassword(c.Request.Context(), req.Phone, req.Password)
	if err != nil {
		// 凭证不匹配是 401，号码格式不对或服务故障是 400。
		// 注意 401 的文案对三种失败是同一句：接口不能用来"问某个号码有没有账号"。
		if errors.Is(err, domain.ErrInvalidCredentials) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": resp.Token, "user": resp.User})
}

// LoginByPhone 用验证码登录。
func (h *Handler) LoginByPhone(c *gin.Context) {
	var req domain.LoginByPhoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone number and verification code are required"})
		return
	}

	resp, err := h.svc.LoginByPhone(c.Request.Context(), req.Phone, req.Code)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": resp.Token, "user": resp.User})
}

// GetMe 返回当前登录用户的资料。
func (h *Handler) GetMe(c *gin.Context) {
	user, err := h.svc.GetUserByID(c.Request.Context(), middleware.GetUserID(c))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"user": user})
}
