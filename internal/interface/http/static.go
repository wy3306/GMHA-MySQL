package http

import (
	"embed"
	"io/fs"
	"net/http"
)

// frontendFiles 是由 Vue/Vite 构建出的静态资源。资源嵌入管理端二进制，部署时无需 Node.js 或 Web 服务器。
//
//go:embed frontend/dist/*
var frontendFiles embed.FS

func frontendHandler() http.Handler {
	dist, err := fs.Sub(frontendFiles, "frontend/dist")
	if err != nil {
		panic(err)
	}
	// FileServer 在目录路径 / 上会自动返回 index.html；避免将其改写为
	// /index.html，否则 FileServer 会重定向回 /，造成浏览器循环跳转。
	return http.FileServer(http.FS(dist))
}
