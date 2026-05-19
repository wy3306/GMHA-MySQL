// Package v1 定义了 GMHA API v1 版本的请求和响应数据结构，提供主机引导、Agent 注册及心跳等核心 API 类型。
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// BootstrapHostRequest 表示引导主机的请求参数，包含主机名、地址、集群及 SSH 连接信息。
type BootstrapHostRequest struct {
	Name        string `json:"name"`
	Address     string `json:"address"`
	Cluster     string `json:"cluster"`
	SSHPort     int    `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
}

// Validate 对引导主机请求参数进行校验，确保必填字段不为空且端口号合法。
func (r BootstrapHostRequest) Validate() error {
	switch {
	case strings.TrimSpace(r.Name) == "":
		return errors.New("name is required")
	case strings.TrimSpace(r.Address) == "":
		return errors.New("address is required")
	case strings.TrimSpace(r.SSHUser) == "":
		return errors.New("ssh_user is required")
	case strings.TrimSpace(r.SSHPassword) == "":
		return errors.New("ssh_password is required")
	case r.SSHPort <= 0:
		return errors.New("ssh_port must be positive")
	default:
		return nil
	}
}

// BootstrapHostResponse 表示引导主机操作的响应结果，包含主机 ID、引导状态及 Agent 注册信息。
type BootstrapHostResponse struct {
	HostID          string `json:"host_id"`
	HostName        string `json:"host_name"`
	BootstrapState  string `json:"bootstrap_state"`
	AgentID         string `json:"agent_id"`
	AgentRegistered bool   `json:"agent_registered"`
}

// AgentRegisterRequest 表示 Agent 注册的请求参数，包含主机 ID、引导令牌及版本信息。
type AgentRegisterRequest struct {
	HostID         string `json:"host_id"`
	BootstrapToken string `json:"bootstrap_token"`
	Hostname       string `json:"hostname"`
	AdvertiseAddr  string `json:"advertise_addr"`
	Version        string `json:"version"`
}

// Validate 对 Agent 注册请求参数进行校验，确保主机 ID 和引导令牌不为空。
func (r AgentRegisterRequest) Validate() error {
	switch {
	case r.HostID == "":
		return errors.New("host_id is required")
	case r.BootstrapToken == "":
		return errors.New("bootstrap_token is required")
	default:
		return nil
	}
}

// AgentHeartbeatRequest 表示 Agent 心跳请求参数，包含主机信息和版本号。
type AgentHeartbeatRequest struct {
	HostID        string `json:"host_id"`
	Hostname      string `json:"hostname"`
	AdvertiseAddr string `json:"advertise_addr"`
	Version       string `json:"version"`
}

// PostJSON 发送 HTTP POST 请求，将请求体序列化为 JSON 格式，并将响应体反序列化到指定结构体中。
func PostJSON(ctx context.Context, client *http.Client, url string, reqBody any, respBody any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err == nil {
			return fmt.Errorf("http %s: %v", resp.Status, body["error"])
		}
		return fmt.Errorf("http %s", resp.Status)
	}
	if respBody == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(respBody)
}
