// Package render 提供 Go 模板渲染引擎，用于生成 MySQL 配置文件、systemd 单元文件等。
package render

import (
	"bytes"
	"text/template"
)

// Engine 是模板渲染引擎，使用 Go 标准库的 text/template 进行模板解析和渲染。
type Engine struct{}

// NewEngine 创建一个新的模板渲染引擎实例。
func NewEngine() *Engine {
	return &Engine{}
}

// Render 使用指定的模板源和数据渲染生成输出内容。
func (e *Engine) Render(name, source string, data any) ([]byte, error) {
	tpl, err := template.New(name).Parse(source)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
