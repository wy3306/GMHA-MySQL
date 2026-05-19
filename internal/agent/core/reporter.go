package core

import (
	"sync"
	"time"

	taskdomain "gmha/internal/domain/task"
	"golang.org/x/net/websocket"
)

// Reporter 是任务进度上报器，通过 WebSocket 连接向管理端实时上报任务执行状态和事件。
type Reporter struct {
	conn      *websocket.Conn
	agentID   string
	machineID string
	mu        sync.Mutex
}

// NewReporter 创建一个新的任务进度上报器实例。
func NewReporter(conn *websocket.Conn, agentID, machineID string) *Reporter {
	return &Reporter{conn: conn, agentID: agentID, machineID: machineID}
}

// Report 发送一条任务报告消息到管理端，自动填充代理 ID 和机器 ID。
func (r *Reporter) Report(msg taskdomain.ReportEnvelope) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if msg.Kind == "" {
		msg.Kind = "task_report"
	}
	if msg.AgentID == "" {
		msg.AgentID = r.agentID
	}
	if msg.MachineID == "" {
		msg.MachineID = r.machineID
	}
	if msg.Event != nil && msg.Event.CreatedAt.IsZero() {
		msg.Event.CreatedAt = time.Now().UTC()
	}
	return websocket.JSON.Send(r.conn, msg)
}
