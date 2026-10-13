// Package todo 用来标记"留给你填空"的代码。
//
// 骨架里每个空函数的函数体都返回这里的零值错误，而不是静默返回零值。原因是零值会骗人：
// 一个 nil 的 *LinkInfo 会被调用方当成"链接查到了"，一个 false 的 bool 会被当成"密码不对"。
// 带上函数名的错误会在你第一次跑测试、第一次 curl 的时候就告诉你"这里还没写"。
//
// 它还给你一个进度条：
//
//	grep -rn "todo\." --include=*.go .   # 还剩多少空没填
//
// 用法：写完一个函数体，把里面的 todo.Err(...) / todo.Bool(...) 删掉即可。
// 判据：`go test ./...` 里对应的验收测试从红变绿。
package todo

import "fmt"

// Err 返回一个"尚未实现"的错误，带出函数名，方便一眼看出还缺哪一块。
func Err(what string) error {
	return fmt.Errorf("TODO 未实现: %s", what)
}

// Bool 是 bool 返回值函数的占位。没有它的话，空函数只能写 `return false`，
// 而 `false` 在 "IsCrawler" 这种语义下看起来像一个合法答案。
func Bool(what string) bool {
	_ = what
	return false
}

// String 是 string 返回值函数的占位。
func String(what string) string {
	_ = what
	return ""
}

// Int64 是整型返回值函数的占位。
func Int64(what string) int64 {
	_ = what
	return 0
}

// Panic 用在没有返回值、又无法"返回错误"的地方（例如中间件里的分支、
// 或者为了满足接口而必须存在的方法）。它会让问题立刻可见，而不是被悄悄跳过。
func Panic(what string) {
	panic("TODO 未实现: " + what)
}
