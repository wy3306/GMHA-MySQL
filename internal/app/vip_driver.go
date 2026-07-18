package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CommandExecutor 定义了远程命令执行接口，VIP 驱动通过它在目标机器上执行 Shell 命令。
type CommandExecutor interface {
	Run(ctx context.Context, command string, timeout time.Duration) CommandResult
}

// CommandResult 是远程命令执行结果。
type CommandResult struct {
	Success      bool      `json:"success"`
	ExitCode     int       `json:"exit_code"`
	Stdout       string    `json:"stdout"`
	Stderr       string    `json:"stderr"`
	ErrorMessage string    `json:"error_message"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at"`
	DurationMS   int64     `json:"duration_ms"`
}

// CheckVipRequest 是 VIP 检查请求。
type CheckVipRequest struct {
	VIP       string
	Prefix    int
	Interface string
}

// AddVipRequest 是添加 VIP 绑定请求。
type AddVipRequest struct {
	VIP       string
	Prefix    int
	Interface string
}

// DeleteVipRequest 是删除 VIP 绑定请求。
type DeleteVipRequest struct {
	VIP       string
	Prefix    int
	Interface string
}

// MoveVipRequest 是 VIP 迁移请求（从一个网卡迁移到另一个网卡）。
type MoveVipRequest struct {
	VIP           string
	Prefix        int
	FromInterface string
	ToInterface   string
}

// ValidateVipRequest 是 VIP 验证请求。
type ValidateVipRequest struct {
	VIP       string
	Interface string
}

// CheckVipResult 是 VIP 检查结果，包含是否绑定、所在网卡和持有者列表。
type CheckVipResult struct {
	Bound     bool          `json:"bound"`
	Interface string        `json:"interface"`
	Raw       CommandResult `json:"raw"`
	Holders   []DetectedVIP `json:"holders,omitempty"`
}

// DetectedVIP 是检测到的 VIP 持有者信息。
type DetectedVIP struct {
	MachineID string `json:"machine_id"`
	Interface string `json:"interface"`
	IP        string `json:"ip"`
}

// VipOperationResult 是 VIP 操作（添加/删除/迁移）的结果。
type VipOperationResult struct {
	Success bool          `json:"success"`
	Command string        `json:"command"`
	Raw     CommandResult `json:"raw"`
	Plan    []string      `json:"plan,omitempty"`
}

// ValidateVipResult 是 VIP 验证结果。
type ValidateVipResult struct {
	Valid   bool          `json:"valid"`
	Message string        `json:"message"`
	Raw     CommandResult `json:"raw"`
}

// VipDriver 是 VIP 操作驱动接口，定义了检查、添加、删除、迁移和验证 VIP 的方法。
// 不同的 VIP 路由模式（L2_ARP、BGP、CLOUD_API、KEEPALIVED、MANUAL）有不同的驱动实现。
type VipDriver interface {
	Check(ctx context.Context, req CheckVipRequest) (*CheckVipResult, error)
	Add(ctx context.Context, req AddVipRequest) (*VipOperationResult, error)
	Delete(ctx context.Context, req DeleteVipRequest) (*VipOperationResult, error)
	Move(ctx context.Context, req MoveVipRequest) (*VipOperationResult, error)
	Validate(ctx context.Context, req ValidateVipRequest) (*ValidateVipResult, error)
}

// L2ARPVipDriver 是基于 L2 ARP 的 VIP 驱动，通过 ip addr 命令管理 VIP 绑定，
// 并使用 arping 发送免费 ARP 通告 VIP 变更。
type L2ARPVipDriver struct {
	exec CommandExecutor
}

// NewL2ARPVipDriver 创建 L2 ARP VIP 驱动实例。
func NewL2ARPVipDriver(exec CommandExecutor) *L2ARPVipDriver {
	return &L2ARPVipDriver{exec: exec}
}

func (d *L2ARPVipDriver) Check(ctx context.Context, req CheckVipRequest) (*CheckVipResult, error) {
	cmd := fmt.Sprintf("ip addr show dev %s to %s", shellQuote(req.Interface), shellQuote(req.VIP))
	res := d.run(ctx, cmd, 10*time.Second)
	return &CheckVipResult{Bound: res.Success && strings.Contains(res.Stdout, req.VIP), Interface: req.Interface, Raw: res}, nil
}

func (d *L2ARPVipDriver) Add(ctx context.Context, req AddVipRequest) (*VipOperationResult, error) {
	if req.Interface == "" {
		return nil, errors.New("vip interface is required")
	}
	if req.Prefix == 0 {
		req.Prefix = 24
	}
	cmd := fmt.Sprintf("ip addr add %s/%d dev %s", shellQuote(req.VIP), req.Prefix, shellQuote(req.Interface))
	res := d.run(ctx, cmd, 10*time.Second)
	return &VipOperationResult{Success: res.Success, Command: cmd, Raw: res}, commandErr(res)
}

func (d *L2ARPVipDriver) Delete(ctx context.Context, req DeleteVipRequest) (*VipOperationResult, error) {
	if req.Interface == "" {
		return nil, errors.New("vip interface is required")
	}
	if req.Prefix == 0 {
		req.Prefix = 24
	}
	cmd := fmt.Sprintf("ip addr del %s/%d dev %s", shellQuote(req.VIP), req.Prefix, shellQuote(req.Interface))
	res := d.run(ctx, cmd, 10*time.Second)
	return &VipOperationResult{Success: res.Success, Command: cmd, Raw: res}, commandErr(res)
}

func (d *L2ARPVipDriver) Move(ctx context.Context, req MoveVipRequest) (*VipOperationResult, error) {
	if req.Prefix == 0 {
		req.Prefix = 24
	}
	plan := []string{}
	if req.FromInterface != "" {
		plan = append(plan, fmt.Sprintf("ip addr del %s/%d dev %s", req.VIP, req.Prefix, req.FromInterface))
	}
	plan = append(plan, fmt.Sprintf("ip addr add %s/%d dev %s", req.VIP, req.Prefix, req.ToInterface))
	plan = append(plan, fmt.Sprintf("arping -A -c 3 -I %s %s", req.ToInterface, req.VIP))
	if d.exec == nil {
		return &VipOperationResult{Success: false, Plan: plan}, errors.New("command executor is not configured for L2_ARP VIP move")
	}
	for _, command := range plan {
		res := d.run(ctx, command, 10*time.Second)
		if !res.Success {
			return &VipOperationResult{Success: false, Command: command, Raw: res, Plan: plan}, commandErr(res)
		}
	}
	return &VipOperationResult{Success: true, Plan: plan}, nil
}

func (d *L2ARPVipDriver) Validate(ctx context.Context, req ValidateVipRequest) (*ValidateVipResult, error) {
	check, err := d.Check(ctx, CheckVipRequest{VIP: req.VIP, Interface: req.Interface})
	if err != nil {
		return nil, err
	}
	return &ValidateVipResult{Valid: check.Bound, Message: "VIP address presence checked on selected interface", Raw: check.Raw}, nil
}

func (d *L2ARPVipDriver) run(ctx context.Context, command string, timeout time.Duration) CommandResult {
	if d.exec == nil {
		now := time.Now().UTC()
		return CommandResult{Success: false, ExitCode: -1, ErrorMessage: "command executor is not configured", StartedAt: now, FinishedAt: now}
	}
	return d.exec.Run(ctx, command, timeout)
}

// NotImplementedVipDriver 是未实现的 VIP 驱动占位符。
// 当 VIP 路由模式不支持自动操作时使用，所有方法调用都会返回错误。
type NotImplementedVipDriver struct {
	Mode    string
	Message string
}

// ArchitectureManagedVIPDriver prevents a caller from bypassing the ordered
// architecture state machine. BGP requires cluster-wide remote
// target context, fencing and single-holder verification that the local
// VipDriver interface intentionally does not carry.
type ArchitectureManagedVIPDriver struct{ Mode string }

func (d ArchitectureManagedVIPDriver) err() error {
	return fmt.Errorf("%s VIP changes must run through the architecture adjustment state machine", d.Mode)
}
func (d ArchitectureManagedVIPDriver) Check(context.Context, CheckVipRequest) (*CheckVipResult, error) {
	return nil, d.err()
}
func (d ArchitectureManagedVIPDriver) Add(context.Context, AddVipRequest) (*VipOperationResult, error) {
	return nil, d.err()
}
func (d ArchitectureManagedVIPDriver) Delete(context.Context, DeleteVipRequest) (*VipOperationResult, error) {
	return nil, d.err()
}
func (d ArchitectureManagedVIPDriver) Move(context.Context, MoveVipRequest) (*VipOperationResult, error) {
	return nil, d.err()
}
func (d ArchitectureManagedVIPDriver) Validate(context.Context, ValidateVipRequest) (*ValidateVipResult, error) {
	return nil, d.err()
}

func (d NotImplementedVipDriver) Check(context.Context, CheckVipRequest) (*CheckVipResult, error) {
	return nil, d.err()
}

func (d NotImplementedVipDriver) Add(context.Context, AddVipRequest) (*VipOperationResult, error) {
	return nil, d.err()
}

func (d NotImplementedVipDriver) Delete(context.Context, DeleteVipRequest) (*VipOperationResult, error) {
	return nil, d.err()
}

func (d NotImplementedVipDriver) Move(context.Context, MoveVipRequest) (*VipOperationResult, error) {
	return nil, d.err()
}

func (d NotImplementedVipDriver) Validate(context.Context, ValidateVipRequest) (*ValidateVipResult, error) {
	return nil, d.err()
}

func (d NotImplementedVipDriver) err() error {
	if d.Message != "" {
		return errors.New(d.Message)
	}
	return fmt.Errorf("%s VIP driver is not implemented", d.Mode)
}

func commandErr(res CommandResult) error {
	if res.Success {
		return nil
	}
	if strings.TrimSpace(res.ErrorMessage) != "" {
		return errors.New(res.ErrorMessage)
	}
	if strings.TrimSpace(res.Stderr) != "" {
		return errors.New(res.Stderr)
	}
	return errors.New("VIP command failed")
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'"
}
