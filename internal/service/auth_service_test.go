package service

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"kada-mvp/internal/domain"
)

// fakeSMSSender 记录"有没有被要求发短信"。
type fakeSMSSender struct {
	called bool
	phone  string
	code   string
	err    error
}

func (f *fakeSMSSender) Send(_ context.Context, phone, code string) error {
	f.called, f.phone, f.code = true, phone, code
	return f.err
}

// TestSendSMSCodeRequiresASolvedCaptcha 对应练习 8。
//
// 这是全站唯一"能让服务器花钱"的未认证接口，所以最该钉住的是**闸门顺序**：
// 图形验证码没通过，就一步都不许往下走（不查配额、不发短信、不写库）。
func TestSendSMSCodeRequiresASolvedCaptcha(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	sender := &fakeSMSSender{}
	svc := &AuthService{db: db, sms: sender}

	// DryRun 下 consumeCaptcha 的 RowsAffected 恒为 0，也就是"这个验证码没通过"。
	err := svc.SendSMSCode(context.Background(), "13800138000", "203.0.113.9", "some-captcha-id", "0000")
	if err == nil {
		t.Fatal("图形验证码没通过时 SendSMSCode 应该返回错误")
	}
	if sender.called {
		t.Error("图形验证码没通过却发了短信：这是这个接口最不能被绕过的一步")
	}
	if all := strings.Join(*stmts, "\n"); strings.Contains(all, "insert into sms_codes") {
		t.Errorf("图形验证码没通过却写了 sms_codes：\n%s", all)
	}
}

// 号码格式不对时不该碰数据库，也不该消耗任何一张验证码。
func TestSendSMSCodeRejectsAMalformedPhoneBeforeTouchingTheDatabase(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	sender := &fakeSMSSender{}
	svc := &AuthService{db: db, sms: sender}

	for _, phone := range []string{"", "123", "23800138000", "1380013800a"} {
		if err := svc.SendSMSCode(context.Background(), phone, "203.0.113.9", "some-captcha-id", "0000"); err == nil {
			t.Errorf("号码 %q 应该被拒绝", phone)
		}
	}
	if len(*stmts) != 0 {
		t.Errorf("号码格式不对却访问了数据库：%v", *stmts)
	}
	if sender.called {
		t.Error("号码格式不对却发了短信")
	}
}

// TestRegisterRejectsAWeakPasswordBeforeTouchingTheDatabase 对应练习 9。
//
// "在碰数据库之前校验"不是洁癖：一个不合格的请求不该消耗一条已经发出去的短信验证码，
// 也不该占一次数据库往返。
func TestRegisterRejectsAWeakPasswordBeforeTouchingTheDatabase(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	svc := &AuthService{db: db}

	if _, err := svc.Register(context.Background(), "13800138000", "123456", "short"); err == nil {
		t.Fatal("弱密码应该被拒绝")
	}
	if len(*stmts) != 0 {
		t.Errorf("弱密码在碰到数据库之前就该被拒：%v", *stmts)
	}

	if _, err := svc.Register(context.Background(), "123", "123456", "pw12345678"); err == nil {
		t.Fatal("非法号码应该被拒绝")
	}
	if len(*stmts) != 0 {
		t.Errorf("非法号码在碰到数据库之前就该被拒：%v", *stmts)
	}
}

// TestGenerateCaptchaStoresOnlyAHash 对应练习 7。
//
// 这条测试盯的是"一次性凭证的明文绝不落库"：验证码画在 SVG 里（测试先从图里读出来），
// 而落库的那一行必须只有它的 sha256。断言精确到绑定值，是因为 DryRun 渲染出的语句里
// 只有 $1/$2 占位符——只看 SQL 文本的话，"存了明文"和"存了哈希"长得一模一样。
func TestGenerateCaptchaStoresOnlyAHash(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	svc := &AuthService{db: db}

	resp, err := svc.GenerateCaptcha(context.Background(), "203.0.113.9")
	if err != nil {
		t.Fatalf("GenerateCaptcha 返回错误: %v", err)
	}
	if resp == nil || resp.ID == "" {
		t.Fatal("没有返回 captcha_id")
	}
	if len(resp.ID) != 32 {
		t.Errorf("captcha_id 长度 = %d, want 32（crypto/rand 的 16 字节十六进制）", len(resp.ID))
	}

	const prefix = "data:image/svg+xml;base64,"
	if !strings.HasPrefix(resp.Image, prefix) {
		t.Fatalf("image 不是 SVG 的 data URI: %.40s", resp.Image)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(resp.Image, prefix))
	if err != nil {
		t.Fatalf("image 不是合法 base64: %v", err)
	}
	m := regexp.MustCompile(`>(\d{4})<`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("SVG 里读不到 4 位验证码，客户端就没法把它显示给人看: %s", raw)
	}
	code := m[1]

	all := strings.Join(*stmts, "\n")
	if all == "" {
		t.Fatal("GenerateCaptcha 没有写数据库：验证码存 PostgreSQL 而不是 Redis 是刻意的（Redis 挂了，它也必须还在生效）")
	}
	containsAll(t, all, "insert into login_captchas", "code_hash")
	if !strings.Contains(all, sha256Hex(code)) {
		t.Errorf("插入的行里没有 sha256(验证码)。明文入库意味着这张表一旦泄露就能被直接重放：\n%s", all)
	}
	// IP 列必须出现在这条 INSERT 里：按 IP 统计发送次数的那条配额完全依赖它。
	// 不断言它的值：GORM 把 *string 字段写进 Vars 的是指针本身（`0x...`），值不在语句文本里。
	containsAll(t, all, "ip")
}

// TestValidatePassword 对应练习 17。
func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"空密码", "", true},
		{"太短", "1234567", true},
		{"刚好 8 位", "12345678", false},
		{"长密码", strings.Repeat("a", 40), false},
		{"刚好 72 字节", strings.Repeat("a", 72), false},
		{"73 字节", strings.Repeat("a", 73), true},
		// bcrypt 只看前 72 字节：30 个汉字是 90 字节，超出部分会被静默丢弃，
		// 于是"两个不同的长密码"可以互相登录。所以必须显式拒绝。
		{"30 个汉字（90 字节）", strings.Repeat("汉", 30), true},
		{"24 个汉字（72 字节）", strings.Repeat("汉", 24), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePassword(tc.password)
			if tc.wantErr && err == nil {
				t.Errorf("validatePassword(%d 字节的密码) = nil, want error", len(tc.password))
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validatePassword(%d 字节的密码) = %v, want nil", len(tc.password), err)
			}
		})
	}
}

// 已给的哈希函数也钉一下：bcrypt 的行为（同一个密码两次哈希不同、错密码不通过）
// 是上面登录逻辑的前提。
func TestPasswordHashing(t *testing.T) {
	h1, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword 失败: %v", err)
	}
	h2, err := hashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hashPassword 失败: %v", err)
	}
	if h1 == h2 {
		t.Error("同一个密码两次哈希得到相同结果：说明没加盐")
	}
	if !checkPassword(h1, "correct horse battery staple") {
		t.Error("正确密码没有通过校验")
	}
	if checkPassword(h1, "wrong password") {
		t.Error("错误密码通过了校验")
	}
	// 明文绝不能出现在哈希里。
	if strings.Contains(h1, "correct") {
		t.Error("哈希里出现了明文片段")
	}
}

// TestRandomHelpers 盯住验证码和 id 的随机性来源。
func TestRandomHelpers(t *testing.T) {
	id1, err := randomID()
	if err != nil {
		t.Fatalf("randomID 失败: %v", err)
	}
	if len(id1) != 32 {
		t.Errorf("randomID 长度 = %d, want 32", len(id1))
	}
	if _, err := hex.DecodeString(id1); err != nil {
		t.Errorf("randomID 不是十六进制: %v", err)
	}
	id2, _ := randomID()
	if id1 == id2 {
		t.Error("两次 randomID 得到同一个值")
	}

	code, err := randomDigits(6)
	if err != nil {
		t.Fatalf("randomDigits 失败: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("验证码长度 = %d, want 6", len(code))
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Errorf("验证码 %q 里出现了非数字字符", code)
		}
	}
}

// TestLoginWithPassword 对应练习 10。
//
// 核心断言只有一个：**三种失败必须是同一个错误**。
// 一旦它开始区分"没有这个号码"和"密码不对"，这个接口就变成了
// "查询某个号码在本站有没有账号"的工具。
func TestLoginWithPasswordReportsOneErrorForEveryFailure(t *testing.T) {
	// 一个查不到任何用户的库：覆盖"号码没注册"。
	db, _ := newMissingRowDB(t, 0)
	svc := &AuthService{db: db, jwtSecret: "s", jwtExpire: time.Hour}

	if _, err := svc.LoginWithPassword(context.Background(), "13800138000", "whatever123"); !errors.Is(err, domain.ErrInvalidCredentials) {
		t.Errorf("未注册的号码返回 %v, want domain.ErrInvalidCredentials", err)
	}

	// 号码格式不对也必须是同一个错误：否则它就成了一条"免费"的信息通道。
	for _, phone := range []string{"", "123", "1234567890", "23800138000", "1380013800a"} {
		if _, err := svc.LoginWithPassword(context.Background(), phone, "whatever123"); !errors.Is(err, domain.ErrInvalidCredentials) {
			t.Errorf("号码 %q 返回 %v, want domain.ErrInvalidCredentials", phone, err)
		}
	}
}

// TestConsumeCaptchaCountsFailures 对应练习 13。
//
// 这里能验证的是"两条语句都被发出了"：一条原子消耗，一条失败累加。
// 真正在数据库上验证原子性需要集成测试（见 README 的"验收方式"）。
func TestConsumeCaptchaCountsFailures(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	svc := &AuthService{db: db}

	// DryRun 下 RowsAffected 恒为 0，也就是"没消耗掉"→ 走失败分支。
	err := svc.consumeCaptcha(context.Background(), "captcha-id", "AbCd")
	if err == nil {
		t.Fatal("DryRun 下 RowsAffected=0，consumeCaptcha 应该返回错误")
	}

	all := strings.Join(*stmts, "\n")
	containsAll(t, all, "update login_captchas", "used = false", "expires_at >", "returning id")
	containsAll(t, all, "attempts = attempts + 1")
}

// TestConsumeSMSCodeCountsFailures 对应练习 14。形状和验证码完全一样，
// 唯一的区别是失败累加按 phone 而不是按 id（调用方手里没有那条记录的 id）。
func TestConsumeSMSCodeCountsFailures(t *testing.T) {
	db := newDryRunDB(t)
	stmts := captureSQL(t, db)
	svc := &AuthService{db: db}

	err := svc.consumeSMSCode(context.Background(), "13800138000", "123456")
	if err == nil {
		t.Fatal("DryRun 下 RowsAffected=0，consumeSMSCode 应该返回错误")
	}

	all := strings.Join(*stmts, "\n")
	containsAll(t, all, "update sms_codes", "phone = ", "code_hash", "used = false", "returning id")
	containsAll(t, all, "attempts = attempts + 1")
}

// 这条测试是给"忘记检查数据库错误"的人准备的：userByPhone 必须把
// gorm.ErrRecordNotFound **原样**返回（或者用 %w 包起来），因为调用方靠它分流。
func TestUserByPhoneKeepsRecordNotFound(t *testing.T) {
	db, _ := newMissingRowDB(t, 0)
	svc := &AuthService{db: db}

	_, err := svc.userByPhone(context.Background(), "13800138000")
	if err == nil {
		t.Fatal("查不到用户时 userByPhone 应该返回错误")
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("返回了 %v；调用方需要能用 errors.Is(err, gorm.ErrRecordNotFound) 判断（要用 %%w 包，而不是换一个错误）", err)
	}
}
