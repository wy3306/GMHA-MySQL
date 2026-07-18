package core

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	taskdomain "gmha/internal/domain/task"
	"golang.org/x/net/websocket"
)

// Receiver 是 WebSocket 任务接收器，通过 WebSocket 长连接从管理端接收任务并交给分发器处理。
type Receiver struct {
	managerHTTPAddrs []string
	agentID          string
	machineID        string
	dispatcher       *Dispatcher
}

// NewReceiver 创建一个新的任务接收器实例。
func NewReceiver(managerHTTPAddr, agentID, machineID string, dispatcher *Dispatcher) *Receiver {
	addrs := make([]string, 0, 2)
	for _, addr := range strings.Split(managerHTTPAddr, ",") {
		if addr = strings.TrimSpace(addr); addr != "" {
			addrs = append(addrs, addr)
		}
	}
	return &Receiver{
		managerHTTPAddrs: addrs,
		agentID:          agentID,
		machineID:        machineID,
		dispatcher:       dispatcher,
	}
}

// Run 启动任务接收循环，自动重连 WebSocket 并接收任务。
func (r *Receiver) Run(ctx context.Context) error {
	index := 0
	for {
		if len(r.managerHTTPAddrs) == 0 {
			return fmt.Errorf("manager_http_addr is required")
		}
		err := r.runOnce(ctx, r.managerHTTPAddrs[index%len(r.managerHTTPAddrs)])
		index++
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(3 * time.Second):
			}
		}
	}
}

func (r *Receiver) runOnce(ctx context.Context, managerHTTPAddr string) error {
	capabilities := append(r.dispatcher.Types(), taskdomain.CapabilityMySQLDefaultsFile)
	wsURL, err := buildTaskWSURL(managerHTTPAddr, r.agentID, r.machineID, capabilities)
	if err != nil {
		return err
	}
	conn, err := websocket.Dial(wsURL, "", managerHTTPAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	reporter := NewReporter(conn, r.agentID, r.machineID)
	for {
		var envelope taskdomain.DispatchEnvelope
		if err := websocket.JSON.Receive(conn, &envelope); err != nil {
			return err
		}
		// 任务中的本地资源下载必须跟随当前实际可用的 Manager 连接。
		// Agent 可能配置了多个地址并从备用地址连接成功，不能继续固定使用首地址。
		dispatchCtx := WithManagerHTTPAddr(ctx, managerHTTPAddr)
		go r.dispatcher.Dispatch(dispatchCtx, envelope, reporter)
	}
}

func buildTaskWSURL(managerHTTPAddr, agentID, machineID string, capabilities []string) (string, error) {
	managerHTTPAddr = strings.TrimSpace(managerHTTPAddr)
	if managerHTTPAddr == "" {
		return "", fmt.Errorf("manager_http_addr is required")
	}
	u, err := url.Parse(managerHTTPAddr)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported manager http scheme %s", u.Scheme)
	}
	u.Path = "/ws/agent/tasks"
	q := u.Query()
	q.Set("agent_id", agentID)
	q.Set("machine_id", machineID)
	if len(capabilities) > 0 {
		q.Set("capabilities", strings.Join(capabilities, ","))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
