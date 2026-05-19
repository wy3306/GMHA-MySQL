// Package web 提供静态资源文件的 HTTP 服务。
package web

import "net/http"

// Static 返回静态资源文件的 HTTP 处理器，用于提供前端资源文件的访问。
func Static() http.Handler {
	return http.NewServeMux()
}
