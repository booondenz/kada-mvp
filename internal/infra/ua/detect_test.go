package ua

import (
	"testing"

	"kada-mvp/internal/domain"
)

// 真机 UA 样本（截取到能说明问题的部分即可）。
const (
	uaWeChat = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 " +
		"(KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.40(0x18002832) NetType/WIFI Language/zh_CN"
	uaQQ = "Mozilla/5.0 (Linux; Android 13; SM-G9910 Build/TP1A.220624.014) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Version/4.0 Chrome/107.0.0.0 Mobile Safari/537.36 MQQBrowser/6.2 " +
		"TBS/045818 QQ/8.9.63.10450"
	uaWeibo    = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 weibo__13.8.0"
	uaXHS      = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 XHS/7.60.0 redapp"
	uaChrome   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"
	uaGoogle   = "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)"
	uaWeChatPF = uaWeChat // 微信的预览抓取器穿的就是这个 UA，这正是它没法用 UA 过滤的原因
)

// TestDetect 对应练习 1。
func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want domain.Platform
	}{
		{"微信内置浏览器", uaWeChat, domain.PlatformWechat},
		{"QQ 内置浏览器", uaQQ, domain.PlatformQQ},
		{"微博", uaWeibo, domain.PlatformWeibo},
		{"小红书", uaXHS, domain.PlatformXiaohongshu},
		{"普通浏览器", uaChrome, domain.PlatformBrowser},
		{"空 UA", "", domain.PlatformBrowser},
		{"大小写不敏感", "MICROMESSENGER", domain.PlatformWechat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(tc.ua); got != tc.want {
				t.Errorf("Detect(%q) = %q, want %q", tc.ua, got, tc.want)
			}
		})
	}
}

// TestIsCrawler 对应练习 2。
//
// 最有价值的两条断言不是"能抓到爬虫"，而是：
//   - 空 UA 算爬虫（没有浏览器会不带 UA）；
//   - 微信的 UA **不算**爬虫 —— 判错这里就会把真实访客一起丢掉，
//     而这是这个列表刻意保持短小的全部理由。
func TestIsCrawler(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want bool
	}{
		{"Googlebot", uaGoogle, true},
		{"空 UA", "", true},
		{"curl", "curl/8.4.0", true},
		{"Go 的默认客户端", "Go-http-client/1.1", true},
		{"Facebook 预览抓取器", "facebookexternalhit/1.1", true},
		{"Chrome 是真人", uaChrome, false},
		{"微信内置浏览器是真人", uaWeChatPF, false},
		{"QQ 内置浏览器是真人", uaQQ, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCrawler(tc.ua); got != tc.want {
				t.Errorf("IsCrawler(%q) = %v, want %v", tc.ua, got, tc.want)
			}
		})
	}
}

// NeedsIntermediatePage 是已给的，但它是"哪些平台需要引导页"的唯一定义，
// 所以值得钉住：改了它，跳转路径的 ClickKind 判定就跟着变。
func TestNeedsIntermediatePage(t *testing.T) {
	for _, p := range []domain.Platform{domain.PlatformWechat, domain.PlatformQQ, domain.PlatformXiaohongshu} {
		if !NeedsIntermediatePage(p) {
			t.Errorf("NeedsIntermediatePage(%q) = false, want true", p)
		}
	}
	for _, p := range []domain.Platform{domain.PlatformBrowser, domain.PlatformWeibo} {
		if NeedsIntermediatePage(p) {
			t.Errorf("NeedsIntermediatePage(%q) = true, want false", p)
		}
	}
}
