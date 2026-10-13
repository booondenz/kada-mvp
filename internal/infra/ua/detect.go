// Package ua 只做一件事：从 User-Agent 判断"这个请求可能来自哪里"，以及"它是不是人"。
//
// 对照原项目：kada/internal/infra/ua/detect.go（那一份还带引导页、deeplink、平台文案；
// MVP 不做引导页，所以只剩两个函数）。
package ua

import (
	"strings"

	"kada-mvp/internal/domain"
)

// Detect 判断这次请求来自哪个平台的内置浏览器。
//
// TODO(练习 1)：实现平台识别。
//
// 要求：
//  1. 先把 User-Agent 转成小写 —— UA 的大小写并不统一，之后所有匹配都在小写串上做。
//  2. 微信内置浏览器：包含 "micromessenger"。
//  3. QQ 内置浏览器：包含 "qq/" 或 "mqqbrowser"。
//     ⚠ 微信和 QQ 的判断先后值得你想清楚：微信的 UA 里含 "micromessenger"，
//     QQ 的 UA 里含 "mqqbrowser" / "qq/"，两者并不重叠，所以顺序不会互相吃掉；
//     但如果有人（包括你）以后往列表里加一条更宽的规则，顺序就立刻有意义了。
//  4. 微博：包含 "weibo"。
//  5. 小红书：包含 "xhs" 或 "redapp"。
//  6. 都不匹配 → domain.PlatformBrowser。
//
// 验收：go test ./internal/infra/ua/ -run TestDetect
func Detect(userAgent string) domain.Platform {
	ua := strings.ToLower(userAgent)

	switch {
	case strings.Contains(ua, "micromessenger"):
		return domain.PlatformWechat
	case strings.Contains(ua, "qq/") || strings.Contains(ua, "mqqbrowser"):
		return domain.PlatformQQ
	case strings.Contains(ua, "weibo"):
		return domain.PlatformWeibo
	case strings.Contains(ua, "xhs") || strings.Contains(ua, "redapp"):
		return domain.PlatformXiaohongshu
	default:
		return domain.PlatformBrowser
	}
}

// crawlerMarkers 是"浏览器绝不会发送"的 token 列表：搜索引擎爬虫、链接预览抓取器、
// SEO 工具、命令行与无头浏览器。
//
// 这个列表刻意地短、刻意地字面匹配。它不是在"识别所有 bot"，而是在找**一组
// 真实浏览器永远不会出现在 UA 里的字符串** —— 这条约束保证了一次误杀不会发生。
//
// 也正因为这条约束，微信和 QQ 不能靠这个列表来过滤：它们的预览抓取器穿的就是
// App 内置浏览器的 UA，任何能抓住抓取器的模式都会把真实访客一起丢掉。
// 那两个平台只能靠"页面自己来确认"来区分（见 domain.ClickKind 的注释）。
//
// 这一份是**已给**的：列表本身就是题干，不用改。
var crawlerMarkers = []string{
	// 搜索引擎
	"googlebot", "bingbot", "baiduspider", "yandexbot", "duckduckbot", "applebot", "petalbot",
	"bytespider", "sogou web spider", "360spider", "haosouspider",
	// 链接预览抓取器
	"facebookexternalhit", "twitterbot", "telegrambot", "slackbot", "discordbot", "linkedinbot",
	"skypeuripreview", "embedly", "quora link preview", "vkshare", "pinterestbot", "redditbot",
	// SEO 爬虫：拿到链接就抓
	"ahrefsbot", "semrushbot", "mj12bot", "dotbot",
	// 工具与无头浏览器：链接被取走，但没有人在看
	"curl/", "wget/", "python-requests", "python-urllib", "go-http-client", "okhttp",
	"libwww-perl", "apachehttpclient", "postmanruntime", "insomnia", "headlesschrome",
}

// IsCrawler 报告这个 User-Agent 是否自称"不是人在浏览网页"。
//
// TODO(练习 2)：实现它。
//
// 要求：
//  1. 空 User-Agent 直接算爬虫 —— 没有任何浏览器会不带 UA，
//     所以一个不带 UA 的请求不可能来自"有人正在看的页面"。
//  2. 否则转小写，逐个检查 crawlerMarkers，命中任何一个就返回 true。
//  3. 都不命中返回 false。
//
// 验收：go test ./internal/infra/ua/ -run TestIsCrawler
func IsCrawler(userAgent string) bool {
	// 空 UA 直接算爬虫：没有任何浏览器会不带 UA。
	if userAgent == "" {
		return true
	}

	ua := strings.ToLower(userAgent)
	for _, marker := range crawlerMarkers {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return false
}

// NeedsIntermediatePage 报告这个平台是否需要一张"中间页"而不是直接 302（已给）。
//
// 微信、QQ、小红书的内置浏览器会拦截或改写外部跳转，直接 302 的结果经常是
// "点了没反应"。所以这三个平台服务一张引导页（把目标地址显示出来，附一个可点的入口），
// 由页面自己确认这次访问——顺便把"预取还是真人"这个问题一起解决了。
func NeedsIntermediatePage(platform domain.Platform) bool {
	switch platform {
	case domain.PlatformWechat, domain.PlatformQQ, domain.PlatformXiaohongshu:
		return true
	default:
		return false
	}
}

// PlatformName 返回 platform 的展示名（已给，前端文案用）。
func PlatformName(platform domain.Platform) string {
	switch platform {
	case domain.PlatformWechat:
		return "WeChat"
	case domain.PlatformQQ:
		return "QQ"
	case domain.PlatformWeibo:
		return "Weibo"
	case domain.PlatformXiaohongshu:
		return "Xiaohongshu"
	default:
		return "Browser"
	}
}
