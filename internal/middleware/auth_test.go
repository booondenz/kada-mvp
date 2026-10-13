package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-for-middleware"

func init() { gin.SetMode(gin.TestMode) }

// signToken 在测试里自己签 token。
//
// 这一点很重要：**测试不该依赖被测代码去造输入**。如果这里调用 auth service 的
// generateToken，那么"签发"和"校验"就会一起错、一起对，测试也就什么都证明不了。
func signToken(t *testing.T, secret string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("签发测试 token 失败: %v", err)
	}
	return s
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"user_id": float64(42), // JSON 数字解出来是 float64，这是最常见的踩坑点
		"iat":     time.Now().Unix(),
		"exp":     time.Now().Add(time.Hour).Unix(),
	}
}

// newAuthRouter 挂一个只为测试存在的路由：认证通过时把 user_id 回显出来。
func newAuthRouter(secret string) *gin.Engine {
	r := gin.New()
	h := &Handler{}
	g := r.Group("/api", JWTAuth(secret))
	g.GET("/me", h.Me)
	return r
}

// Handler 是测试用的最小 handler。
type Handler struct{}

func (h *Handler) Me(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"user_id": GetUserID(c)})
}

func doAuthRequest(r *gin.Engine, authHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestJWTAuth 对应练习 3。
func TestJWTAuth(t *testing.T) {
	r := newAuthRouter(testSecret)

	t.Run("缺少 Authorization 头", func(t *testing.T) {
		if w := doAuthRequest(r, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("状态码 = %d, want 401", w.Code)
		}
	})

	t.Run("认证方案不是 Bearer", func(t *testing.T) {
		token := signToken(t, testSecret, jwt.SigningMethodHS256, validClaims())
		if w := doAuthRequest(r, "Token "+token); w.Code != http.StatusUnauthorized {
			t.Errorf("状态码 = %d, want 401", w.Code)
		}
	})

	t.Run("头里没有空格", func(t *testing.T) {
		token := signToken(t, testSecret, jwt.SigningMethodHS256, validClaims())
		if w := doAuthRequest(r, "Bearer"+token); w.Code != http.StatusUnauthorized {
			t.Errorf("状态码 = %d, want 401", w.Code)
		}
	})

	t.Run("合法 token 通过并把 user_id 交给 handler", func(t *testing.T) {
		token := signToken(t, testSecret, jwt.SigningMethodHS256, validClaims())
		w := doAuthRequest(r, "Bearer "+token)
		if w.Code != http.StatusOK {
			t.Fatalf("状态码 = %d, want 200（body=%s）", w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != `{"user_id":42}` {
			t.Errorf("body = %s, want user_id=42", got)
		}
	})

	t.Run("过期的 token", func(t *testing.T) {
		claims := validClaims()
		claims["exp"] = time.Now().Add(-time.Minute).Unix()
		token := signToken(t, testSecret, jwt.SigningMethodHS256, claims)
		if w := doAuthRequest(r, "Bearer "+token); w.Code != http.StatusUnauthorized {
			t.Errorf("状态码 = %d, want 401", w.Code)
		}
	})

	t.Run("用别的密钥签的 token", func(t *testing.T) {
		token := signToken(t, "another-secret", jwt.SigningMethodHS256, validClaims())
		if w := doAuthRequest(r, "Bearer "+token); w.Code != http.StatusUnauthorized {
			t.Errorf("状态码 = %d, want 401", w.Code)
		}
	})

	// 算法混淆：同一个密钥、换个算法签名。不做 WithValidMethods 限制的话它会通过。
	t.Run("算法不是 HS256", func(t *testing.T) {
		token := signToken(t, testSecret, jwt.SigningMethodHS512, validClaims())
		if w := doAuthRequest(r, "Bearer "+token); w.Code != http.StatusUnauthorized {
			t.Errorf("状态码 = %d, want 401（只允许 HS256）", w.Code)
		}
	})
}

// 中间件写了一个"没有校验"的版本时，最容易出现的现象是：401 写进了 body，
// 但 handler 照样执行了（少一个 c.Abort()）。这条测试专门盯住它——
// 如果 handler 被执行，body 里会出现 user_id=0。
func TestJWTAuthAbortsInsteadOfOnlyWriting401(t *testing.T) {
	r := newAuthRouter(testSecret)
	w := doAuthRequest(r, "Bearer not-a-token")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d, want 401", w.Code)
	}
	if body := w.Body.String(); body == `{"user_id":0}` {
		t.Error("handler 在认证失败之后仍然执行了：中间件缺少 c.Abort()")
	}
}
