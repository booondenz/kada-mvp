// Package httpx 放 HTTP 层的小工具。
//
// 目前只有一件东西：骨架用的"尚未实现"响应。它不是业务代码，
// 存在的意义是让没写的接口**明确地**回 501，而不是回 200 + 一个空 body
// （那会让前端和测试都以为它成功了）。等你把对应的 handler 写完，
// 就把那次调用删掉；全部写完时这个文件也可以删。
package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// NotImplemented 回 501，并带上"哪一个 handler 还没写"。
func NotImplemented(c *gin.Context, what string) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "TODO 未实现: " + what})
}
