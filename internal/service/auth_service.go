package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"kada-mvp/internal/domain"
	"kada-mvp/internal/domain/entity"
	"kada-mvp/internal/todo"
)

// 这个文件是整套服务里最值得慢慢写的一个：它把"花钱的接口"和"不花钱的接口"分开，
// 并且每一个顺序上的取舍都有原因。对照原项目：kada/internal/service/auth_service.go
//
// 登录方式的取舍（原项目为了这两条各自付过代价）：
//   - 注册时用短信验证码证明号码是本人的，之后用**密码**登录。
//     一次注册花一条短信，而不是每次登录花一条——这是密码存在的唯一理由。
//   - login-by-phone（验证码登录）保留着，它不是"方便"，而是**唯一**能进入一个
//     "还没有密码的账号"的方式（老账号就长这样），也是忘记密码后唯一的退路。

const (
	// captchaTTL 图形验证码有效期。
	captchaTTL = 5 * time.Minute
	// smsCodeTTL 短信验证码有效期。
	smsCodeTTL = 5 * time.Minute
	// captchaMaxAttempts / codeMaxAttempts 一个挑战允许答错几次。
	captchaMaxAttempts = 5
	codeMaxAttempts    = 5

	// phoneCooldown 同一个号码两次发码之间的最小间隔。
	phoneCooldown = 60 * time.Second
	// ipHourlySendLimit 同一个 IP 每小时最多触发多少次发送。
	//
	// 两个配额是**互相独立**的，这不是重复：按号码的配额限制的是"一个受害者被骚扰多少次"，
	// 按 IP 的配额才能发现"一个脚本在批量遍历号码"——后者每个号码只用一次，
	// 单靠按号码的配额永远看不出来。IP 的上限刻意放宽，因为学校/公司的 NAT 后面
	// 很多人共用一个出口地址。
	ipHourlySendLimit = 10

	// minPasswordLen 密码最短长度。
	minPasswordLen = 8
	// maxPasswordBytes 密码最长字节数。bcrypt 只处理前 72 字节，
	// 超出部分被静默忽略——那意味着"两个不同的长密码"会互相能登录，
	// 所以宁可显式拒绝。
	maxPasswordBytes = 72
)

// phonePattern 是中国大陆手机号的形状。已给：校验规则本身不是练习内容。
var phonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

// AuthService 负责认证：验证码、短信、注册、登录、签发 token。
type AuthService struct {
	db        *gorm.DB
	jwtSecret string
	// jwtExpire 用 time.Duration 而不是字符串：只解析一次，而不是每个请求解析一次。
	jwtExpire time.Duration
	sms       SMSSender
}

// NewAuthService 构造认证服务。已给（样板）。
//
// 注意两处兜底：jwtExpire 解析失败时退回 720h；sms 为 nil 时换成"把验证码打到日志"的
// 开发实现，这样没有云账号也能把整条链路跑通。
func NewAuthService(db *gorm.DB, jwtSecret, jwtExpire string, sms SMSSender) *AuthService {
	expire, err := time.ParseDuration(jwtExpire)
	if err != nil || expire <= 0 {
		expire = 720 * time.Hour
	}
	if sms == nil {
		sms = LogSMSSender{}
	}
	return &AuthService{db: db, jwtSecret: jwtSecret, jwtExpire: expire, sms: sms}
}

// ==================== 公开方法（要你填的部分） ====================

// GenerateCaptcha 生成图形验证码，并返回它的 id 和 SVG 图片（data URI）。
//
// TODO(练习 7)：实现它。
//
// 要求：
//  1. code := randomDigits(4)（4 位数字，已给）。
//  2. id := randomID()（32 位十六进制，已给）。
//  3. 往 login_captchas 插入一行：ID=id, CodeHash=sha256Hex(code), IP=&ip,
//     ExpiresAt=now+captchaTTL, Used=false, Attempts=0。
//     ⚠ 存的是**哈希**，不是明文：这张表万一泄露，也不能被拿去重放。
//  4. 返回 &domain.CaptchaResponse{ID: id, Image: renderCaptchaSVG(code)}。
//  5. 插入失败要返回带上下文的错误（用 fmt.Errorf("...: %w", err)），
//     因为"验证码发不出去"和"数据库连不上"是两件需要不同处理的事。
//
// 为什么验证码存 PostgreSQL 而不是 Redis？见 entity.LoginCaptcha 的注释：
// Redis 在本项目里是可选的，而一个"悄悄不再生效"的验证码比没有验证码更糟——
// 接口看起来还是受保护的。
//
// 验收：go test ./internal/service/ -run TestGenerateCaptcha
func (s *AuthService) GenerateCaptcha(ctx context.Context, ip string) (*domain.CaptchaResponse, error) {
	return nil, todo.Err("AuthService.GenerateCaptcha")
}

// SendSMSCode 校验图形验证码，检查配额，然后把短信验证码发出去。
//
// TODO(练习 8)：实现它。**顺序就是设计**，每一步都不能挪：
//
//  1. 手机号格式不对 → 直接 400（在碰数据库之前就拒掉）。
//  2. consumeCaptcha(ctx, captchaID, captchaAnswer)：
//     图形验证码是**先校验、而且无论后面成败都被烧掉**。
//     为什么？因为这是全站唯一"能让服务器花钱"的未认证接口。
//     一个"发失败还能复用"的验证码，等于给了自动化调用者一张可以反复刷的卡。
//     客户端每次尝试后都会重新取一张，就是为了这个。
//  3. 号码冷却：60 秒内已经给这个号码发过 → *domain.RateLimitError{RetryAfterSeconds: 剩余秒数}。
//  4. IP 配额：这一小时这个 IP 已经发了 >= ipHourlySendLimit 次 → 同样是 RateLimitError。
//  5. code := randomDigits(6)；s.sms.Send(ctx, phone, code)，失败就返回错误（不要存哈希）。
//  6. 存一行 sms_codes：Phone, CodeHash=sha256Hex(code), IP, ExpiresAt=now+smsCodeTTL。
//     失败要返回错误——注意原项目在这里有过一个真实故障：这一行插入在**短信已经发出去之后**，
//     当时表上残留了一个 NOT NULL 的历史列导致插入失败，用户收到了验证码却看到"发送失败",
//     而因为没写成行，冷却也就永远匹配不到，于是每次重试都真的再发一条短信。
//     教训：发出去之后才写的那一步，失败信息必须非常明确。
//
// 配额怎么查（都是"数几行"）：
//   - 号码冷却：SELECT count(*) FROM sms_codes WHERE phone = ? AND created_at > now()-60s
//   - IP 小时配额：SELECT count(*) FROM sms_codes WHERE ip = ? AND created_at > now()-1h
//     用 Model(&entity.SMSVerificationCode{}).Where(...).Count(&n) 即可。
//
// 注意 COUNT 拿不到"还剩多久"：RetryAfterSeconds 返回整个窗口（60 / 3600 秒）就够用了，
// 想更精确就得改成查最新一行的 created_at 再自己算差值（原项目也是返回整窗口）。
//
// 返回的错误类型：配额用 *domain.RateLimitError（HTTP 层会翻译成 429 + Retry-After），
// 其他失败用普通 error（HTTP 层翻译成 400）。
//
// 验收：go test ./internal/service/ -run TestSendSMSCode
func (s *AuthService) SendSMSCode(ctx context.Context, phone, ip, captchaID, captchaAnswer string) error {
	return todo.Err("AuthService.SendSMSCode")
}

// Register 用验证码 + 密码注册（或给一个没有密码的老账号补上密码）。
//
// TODO(练习 9)：实现它。
//
// 要求：
//  1. 先校验号码格式和密码强度（validatePassword）——**在碰数据库之前**。
//     理由很实际：一个不合格的请求不该消耗一条短信验证码，也不该占一次数据库往返。
//  2. consumeSMSCode(ctx, phone, code)：消耗掉那条待用验证码。失败 → 返回错误（400）。
//  3. userByPhone(ctx, phone)：
//     - 查不到（errors.Is(err, gorm.ErrRecordNotFound)）→ 用 bcrypt 哈希密码，创建账号。
//     - 查到且 PasswordHash == nil → 给它设上密码（这类账号是老数据，验证码是唯一的入口）。
//     - 查到且已有密码 → 返回 domain.ErrAlreadyRegistered（HTTP 409）。
//     为什么拒绝而不是覆盖？因为这个接口是未登录可调的：
//     允许覆盖就等于"拿到一个验证码可以改掉别人的密码"。
//  4. startSession(ctx, user, ip) 签发 token 并返回。
//  5. 创建账号时如果撞唯一约束（isDuplicateKey(err)）：这是"同一号码并发注册"的并发窗口，
//     返回 domain.ErrAlreadyRegistered 而不是 500——对调用方来说答案确实是"去登录"。
//
// 验收：go test ./internal/service/ -run TestRegister
func (s *AuthService) Register(ctx context.Context, phone, code, password string) (*domain.AuthResponse, error) {
	return nil, todo.Err("AuthService.Register")
}

// LoginWithPassword 用密码登录。
//
// TODO(练习 10)：实现它。这里的重点是**失败时不泄露信息**：
//
//  1. 号码格式不对 → 返回 domain.ErrInvalidCredentials（注意：不是"号码格式错误"，
//     接口对外只有一种失败）。
//  2. userByPhone 查不到 → **仍然跑一次 bcrypt 比较**（可以拿一个固定的"假哈希"来比），
//     然后返回 domain.ErrInvalidCredentials。
//     为什么？因为 bcrypt 比较很慢（几十毫秒），而"用户不存在"这条路径如果不做这个运算，
//     就会立刻返回。攻击者用响应时间就能区分"这个号码有账号"和"没有"——
//     而这个接口是每分钟允许 20 次的。
//  3. PasswordHash == nil（还没有密码的账号）→ 同样是 ErrInvalidCredentials。
//  4. checkPassword 失败 → ErrInvalidCredentials。
//     三种失败必须是**同一个错误、同一段耗时**，否则这个接口就在回答
//     "这个号码在本站注册过吗"。
//  5. 成功 → startSession。
//
// 验收：go test ./internal/service/ -run TestLoginWithPassword
func (s *AuthService) LoginWithPassword(ctx context.Context, phone, password string) (*domain.AuthResponse, error) {
	return nil, todo.Err("AuthService.LoginWithPassword")
}

// LoginByPhone 用验证码登录（老账号的唯一入口，也是忘记密码后的退路）。
//
// TODO(练习 11)：实现它。
//
//  1. 校验号码格式 → 不合格返回普通错误。
//  2. consumeSMSCode(ctx, phone, code) → 失败返回错误。
//  3. userByPhone：查不到 → domain.ErrInvalidCredentials
//     （验证码是对的，但号码上确实没有账号：这种情况必须明确拒绝，
//     而不是"顺手给你建一个"，否则这又是一个未认证的可写入口）。
//  4. startSession。
//
// 验收：本函数没有单独的单测。它依赖"验证码已被消耗 + 账号存在"这两件事的组合，
// 请按 README 第 3 节的冒烟流程验证（login-by-phone 能换到 token；未注册的号码必须 401）。
func (s *AuthService) LoginByPhone(ctx context.Context, phone, code string) (*domain.AuthResponse, error) {
	return nil, todo.Err("AuthService.LoginByPhone")
}

// GetUserByID 读取当前登录用户的信息。
//
// TODO(练习 12)：实现它。一条查询 + 映射成 domain.UserInfo（只发布手机号）。
// 查不到返回错误。
//
// 验收：本函数没有单独的单测（一条查询的映射，测试价值低于它需要的数据库）；
// 用 README 第 3 节的冒烟流程验证 GET /api/me 返回的 user 里只有 id 和 phone。
func (s *AuthService) GetUserByID(ctx context.Context, userID int64) (*domain.UserInfo, error) {
	return nil, todo.Err("AuthService.GetUserByID")
}

// ==================== 内部方法（要你填的部分） ====================

// consumeCaptcha 原子地消耗一张图形验证码。
//
// TODO(练习 13)：实现它。这是本文件里最值得写对的 SQL，因为"先查再改"是错的：
//
//	-- 一条 UPDATE 同时完成"校验"和"消耗"，靠 WHERE 里的全部条件
//	UPDATE login_captchas
//	   SET used = true
//	 WHERE id = ?
//	   AND used = false            -- 没用过
//	   AND expires_at > now()      -- 没过期
//	   AND attempts < 5            -- 没被暴力试过
//	   AND code_hash = ?           -- 答案对（注意：答案要先 upper + trim 再 sha256，见下）
//	RETURNING id
//
//	要求：
//	1. 先 normalize：strings.ToUpper(strings.TrimSpace(answer))，再 sha256Hex。
//	   为什么？因为验证码是画在图片里的字符，用户看到的大小写和前后空格不该决定成败，
//	   而数据库里存的必须是规范化之后的哈希——否则同一个人"正确地"输入 a 和 A 会得到不同结果。
//	2. 用 s.db.WithContext(ctx).Raw(...).Scan(&id) 执行；RowsAffected == 0 表示失败。
//	3. 失败时**累加 attempts**（一次 UPDATE ... SET attempts = attempts + 1
//	   WHERE id = ? AND used = false），然后返回错误。
//	   累加是必须的：没有它，"攻击者可以无限次猜同一个验证码"，
//	   因为失败的消耗不改变任何状态。
//	4. 成功返回 nil。
//
// 验收：go test ./internal/service/ -run TestConsumeCaptcha
func (s *AuthService) consumeCaptcha(ctx context.Context, id, answer string) error {
	return todo.Err("AuthService.consumeCaptcha")
}

// consumeSMSCode 原子地消耗一条短信验证码。形状和 consumeCaptcha 完全一样。
//
// TODO(练习 14)：实现它。
//
//	UPDATE sms_codes
//	   SET used = true
//	 WHERE phone = ?
//	   AND code_hash = ?
//	   AND used = false
//	   AND expires_at > now()
//	   AND attempts < 5
//	RETURNING id
//
//	失败时同样累加 attempts：UPDATE sms_codes SET attempts = attempts + 1
//	                       WHERE phone = ? AND used = false
//	（注意这里按 phone 累加而不是按 id：调用方只知道号码和用户输入的验证码。）
//
// 注意：为什么用原生 SQL 而不是 GORM 的链式调用？因为这里要的是
// **一条语句里的原子性**——"先 SELECT 判断、再 UPDATE 消耗"之间存在并发窗口，
// 两个请求可以同时通过检查。原项目里凡是这种地方都是手写 SQL 并注明原因。
//
// 验收：go test ./internal/service/ -run TestConsumeSMSCode
func (s *AuthService) consumeSMSCode(ctx context.Context, phone, code string) error {
	return todo.Err("AuthService.consumeSMSCode")
}

// startSession 签发 token 并更新登录痕迹。
//
// TODO(练习 15)：实现它。
//
//  1. token, err := s.generateToken(user.ID)（已给）。
//  2. 顺手更新 last_login_at / last_login_ip：
//     s.db.WithContext(ctx).Model(&entity.User{}).Where("id = ?", user.ID).
//     Updates(map[string]any{"last_login_at": time.Now(), "last_login_ip": ip})
//     这一步失败**不应该**让登录失败（返回 token 才是用户要的东西），
//     所以错误只记日志。
//  3. 返回 &domain.AuthResponse{Token: token, User: domain.UserInfo{ID: user.ID, Phone: user.Phone}}。
//
// 验收：本函数没有单独的单测（签发的 token 能不能用，由 middleware.JWTAuth 的测试和冒烟流程一起覆盖）。
// 注意 ip：MVP 里三个调用方（Register / LoginWithPassword / LoginByPhone）的签名里都没有 IP，
// 只能传空串，于是 users.last_login_ip 是空的。想补齐这条路，就从 handler 往下加一个参数——
// 但先想清楚：一个"可空的 IP"和"没有 IP"在数据库里是同一个值，而登录风控恰恰依赖这个区别。
func (s *AuthService) startSession(ctx context.Context, user *entity.User, ip string) (*domain.AuthResponse, error) {
	return nil, todo.Err("AuthService.startSession")
}

// userByPhone 按号码查账号。
//
// TODO(练习 16)：实现它。用 GORM 的 First 查 entity.User，
// 查不到时把 gorm.ErrRecordNotFound 原样返回（调用方要用 errors.Is 判断，
// 所以**不要**把它替换成自己的错误，除非你也用 %w 包起来）。
func (s *AuthService) userByPhone(ctx context.Context, phone string) (*entity.User, error) {
	return nil, todo.Err("AuthService.userByPhone")
}

// validatePassword 校验密码强度。
//
// TODO(练习 17)：实现它。三条规则，都是返回一个能直接给用户看的错误：
//  1. 长度（按**字符**计）< minPasswordLen → fmt.Errorf("password must be at least %d characters", minPasswordLen)
//  2. 字节数 > maxPasswordBytes → fmt.Errorf("password must be at most %d bytes", maxPasswordBytes)
//  3. 空密码？第一条已经覆盖了。
//
// 为什么"字符数"和"字节数"两个都要查？因为 bcrypt 的 72 是**字节**上限：
// 一个 30 个汉字的密码是 90 字节，超出的部分会被静默丢弃，
// 于是"两个不同的密码"可以互相登录。这是这个函数存在的理由。
//
// 验收：go test ./internal/service/ -run TestValidatePassword
func validatePassword(plain string) error {
	return todo.Err("validatePassword")
}

// validatePhone 校验号码格式（已给）。
func validatePhone(phone string) bool {
	return phonePattern.MatchString(strings.TrimSpace(phone))
}

// ==================== 已给的辅助函数（不用你写，但值得读） ====================

// hashPassword 用 bcrypt 哈希密码（已给）。
//
// 为什么是 bcrypt 而不是 sha256？因为密码是**低熵**的：一个 sha256 值可以被
// 用显卡每秒算几十亿次，而 bcrypt 故意慢，且每个密码带自己的盐。
// 注意：短信验证码、图形验证码、API token 用的是 sha256（它们本身是随机的高熵串，
// 慢哈希没有必要）——"什么该用哪种哈希"是这里唯一需要记住的判断。
func hashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkPassword 校验明文密码是否匹配哈希（已给）。
//
// bcrypt.CompareHashAndPassword 内部是常数时间比较；它同时也接受
// 用更高 cost 生成的哈希，所以调大 cost 不需要迁移数据。
func checkPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// generateToken 签发 HS256 的 JWT（已给）。
//
// 只有到期时间这一个声明，没有别的：token 一旦发出去就改不了、撤不回，
// 所以任何会变的信息放进去都等于把它冻结到过期为止。
func (s *AuthService) generateToken(userID int64) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"user_id": userID,
		"iat":     now.Unix(),
		"exp":     now.Add(s.jwtExpire).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.jwtSecret))
}

// randomID 返回 32 个十六进制字符（16 字节 crypto/rand）。已给。
//
// 为什么不用 UUID？这里不需要版本位，而 crypto/rand 的失败是必须检查的
// （返回 error 而不是"退化成一个可预测的值"）。
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomDigits 返回 n 位数字验证码（已给）。
//
// 用 crypto/rand + math/big 而不是 math/rand：math/rand 的序列是可以被推出来的，
// 而验证码被推出来就等于没有验证码。
// 注意 big.Int 的值是均匀分布的（mod 取模会让低位数字偏多，这里 n 很小所以影响可忽略，
// 但你要知道这是"为什么不用 % 10"的原因）。
func randomDigits(n int) (string, error) {
	var sb strings.Builder
	max := big.NewInt(10)
	for i := 0; i < n; i++ {
		d, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		sb.WriteString(d.String())
	}
	return sb.String(), nil
}

// sha256Hex 返回字符串的 sha256 十六进制表示（已给）。
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// renderCaptchaSVG 把 4 个字符画成一张小 SVG，并以 data URI 返回（已给）。
//
// 为什么自己画 SVG 而不引入图像库和字体？因为原项目就是这么做的：
// 一张验证码图只需要"人能读出这 4 个字符"，不需要任何渲染引擎，
// 而少一个依赖就少一份部署风险（字体、CGO、中文字体缺失都会让图片变形）。
func renderCaptchaSVG(code string) string {
	var sb strings.Builder
	sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" width="120" height="40" viewBox="0 0 120 40">`)
	sb.WriteString(`<rect width="120" height="40" fill="#f3f4f6"/>`)
	// 几条干扰线：让"用 OCR 直接读"比"老老实实调用接口"更麻烦一点。
	for i := 0; i < 4; i++ {
		y := 6 + i*9
		sb.WriteString(fmt.Sprintf(`<line x1="0" y1="%d" x2="120" y2="%d" stroke="#9ca3af" stroke-width="1" opacity="0.5"/>`, y, y+4))
	}
	sb.WriteString(fmt.Sprintf(
		`<text x="12" y="30" font-family="monospace" font-size="26" letter-spacing="6" fill="#111827">%s</text>`,
		code))
	sb.WriteString(`</svg>`)

	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(sb.String()))
}

// isNotFound 报告这个错误是不是"没有这一行"（已给）。
func isNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
