package service

import (
	"context"
	"log"
)

// SMSSender 是"发一条验证码短信"这件事的抽象。
//
// 为什么是一个接口？因为真实的实现要接阿里云 PNVS（原项目：
// kada/internal/infra/sms/aliyun.go，里面有几个反直觉的坑：模板变量必须带 ##code## 占位符、
// 验证码由服务商生成、签名和模板必须成对且是账号专属的）。
// MVP 把外部依赖这一步抽掉，只留下接口 + 一个"把验证码打到日志里"的开发实现，
// 这样你可以先把整条登录链路跑通，之后再换真实实现。
type SMSSender interface {
	// Send 把 code 发给 phone。返回 error 表示没发出去，调用方不该把验证码存下来。
	Send(ctx context.Context, phone, code string) error
}

// LogSMSSender 是开发用的实现：不真发短信，只把验证码写进日志。
//
// 它存在的意义是让"注册 → 登录"这条链路在没有任何云账号的情况下也能跑完整。
// 注意日志里出现验证码在生产环境是危险的，所以真实实现里绝不能这样打日志。
type LogSMSSender struct{}

// Send 把验证码打进日志。
func (LogSMSSender) Send(_ context.Context, phone, code string) error {
	log.Printf("[dev-sms] phone=%s code=%s (没有配置真实短信服务，验证码只写在这里)", phone, code)
	return nil
}
