// Package middleware 是"请求进入业务代码之前"的那一层：认证、限流。
//
// 这一层的代码有一个共同点：它必须**永远给出一个答案**。不能 panic、不能把请求挂在那里，
// 也不能在依赖（Redis）挂掉时把整个服务带走。所以下面几处都会看到"失败即放行"或
// "失败即 401"的取舍，而那些取舍是这个包最值得学的东西。
//
// 对照原项目：kada/internal/middleware/auth.go、ratelimit.go
package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Claims 是 JWT 的载荷：账号身份，没有别的。
//
// 注意它只有一个 user_id 是"事实来源"的字段。为什么不在 token 里带手机号、
// 昵称、权限？因为 token 一旦签发就在客户端手里，改不了也撤不回：
// 任何一个会变、或者需要鉴权的信息，放进去就等于把它冻结到过期为止。
type Claims struct {
	UserID int64 `json:"user_id"`
	jwt.RegisteredClaims
}

// JWTAuth 是校验 Bearer token 的中间件。
//
// TODO(练习 3)：实现它。（原项目这个中间件还多一个"API token"分支：token 以 kada_ 开头时
// 交给 tokenValidator 校验；MVP 没有开放 API 令牌，所以只要处理 JWT。）
//
// 要求（每条都对应一个验收测试）：
//
//  1. 取 Authorization 头。为空 → 401 {"error": "authentication token not provided"}，然后 c.Abort()。
//
//  2. 用 strings.SplitN(header, " ", 2) 切开，必须是 2 段且第一段恰好是 "Bearer"
//     （注意大小写：HTTP 规范要求的是 "Bearer"）。不满足 → 401 {"error": "invalid authorization header format"}。
//
//  3. 用 jwt.ParseWithClaims 解析成 *Claims，并且**必须**显式限制算法与要求过期时间：
//
//     jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
//     jwt.WithExpirationRequired(),
//
//     这两行不是装饰。不限制算法时，攻击者可以把 header 改成 {"alg":"none"} 提交一个
//     无签名 token（"算法混淆攻击"）；不要求过期时间时，一个泄露的 token 就永久有效。
//
//  4. 解析失败或 !token.Valid → 401 {"error": "authentication token is invalid or expired"}。
//
//  5. keyFunc 里返回 []byte(secret)（HS256 是对称密钥）。
//
//  6. 成功时 c.Set("user_id", claims.UserID)，再 c.Next()。
//
// 注意：**任何一条失败路径都必须 c.Abort()**。少了它，后面的 handler 照样会执行——
// 中间件写了 401，但数据已经发出去了，这是最典型的"认证看起来生效了"的假象。
//
// 验收：go test ./internal/middleware/ -run TestJWTAuth
func JWTAuth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "authentication token not provided",
			})
			return
		}

		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "invalid authorization header format",
			})
			return
		}

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(parts[1], claims, func(t *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		},
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithExpirationRequired(),
		)
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "authentication token is invalid or expired",
			})
			return
		}

		c.Set("user_id", claims.UserID)
		c.Next()
	}
}

// GetUserID 从 gin.Context 里取当前用户 ID（已给）。
//
// 没有登录时返回 0。调用方（service）之后会把 user_id 放进 WHERE 子句，
// 所以 0 顶多查不到任何东西，不会"查到别人的"。
func GetUserID(c *gin.Context) int64 {
	v, exists := c.Get("user_id")
	if !exists || v == nil {
		return 0
	}
	id, ok := v.(int64)
	if !ok {
		return 0
	}
	return id
}
