package httpapi

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
)

// templateFS 是嵌入的前端模板文件系统。
//
//go:embed templates/index.html
var templateFS embed.FS

// webPage 是解析后的首页 HTML 模板。
var webPage = template.Must(template.ParseFS(templateFS, "templates/index.html"))

// writeJSON 将任意值以 JSON 格式写入 HTTP 响应，并设置对应的状态码。
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// writeError 以 JSON 格式返回错误信息响应。
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
