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

import (
	"fmt"
	"log"
)

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

// Stub 用在"没有返回值、又没法返回错误"的地方：写缓存、记录点击、给链接挂标签。
// 它打印一行提醒，然后什么都不做。
//
// 为什么不 panic？因为一个 panic 会**中止整个包的测试**（testing 会直接结束进程），
// 于是你只看到第一个没写的函数，看不到其余测试的状态。
// 骨架要的是"每一处缺失都各自失败一次"——所以这里只留一行痕迹，让缺失可见但不扩散。
func Stub(what string) {
	log.Printf("TODO 未实现（骨架先跳过）: %s", what)
}

// Panic 是 Stub 的"强硬版"，只在少数确实需要立刻停机的地方使用；
// 默认请不要用它（理由见 Stub）。
func Panic(what string) {
	panic("TODO 未实现: " + what)
}
