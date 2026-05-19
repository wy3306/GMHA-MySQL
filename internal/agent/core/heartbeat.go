package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
)

// Heartbeat 是 HTTP 心跳客户端，定期向管理端发送心跳请求以报告代理存活状态。
type Heartbeat struct {
	client      *http.Client
	managerAddr string
}

// NewHeartbeat 创建一个新的 HTTP 心跳客户端实例。
func NewHeartbeat(client *http.Client, managerAddr string) *Heartbeat {
	return &Heartbeat{client: client, managerAddr: managerAddr}
}

// Do 发送一次心跳请求，将代理 IP 上报到管理端。
func (h *Heartbeat) Do(ctx context.Context, ip string) error {
	body, _ := json.Marshal(map[string]string{"ip": ip})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.managerAddr+"/api/v1/agents/heartbeat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return err
	}
	return nil
}
