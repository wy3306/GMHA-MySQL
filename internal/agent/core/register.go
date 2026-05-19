package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// Register 是代理注册客户端，负责向管理端发送注册请求以完成代理上线。
type Register struct {
	client      *http.Client
	managerAddr string
}

// NewRegister 创建一个新的代理注册客户端实例。
func NewRegister(client *http.Client, managerAddr string) *Register {
	return &Register{client: client, managerAddr: managerAddr}
}

// Do 执行代理注册请求，将代理 IP 上报到管理端。
func (r *Register) Do(ctx context.Context, ip string) error {
	body, _ := json.Marshal(map[string]string{"ip": ip})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.managerAddr+"/api/v1/agents/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return err
	}
	return nil
}
