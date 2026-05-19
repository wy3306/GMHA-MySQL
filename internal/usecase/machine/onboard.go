package machine

import (
	"context"
	"errors"
	"strings"

	machinedomain "gmha/internal/domain/machine"
)

// SSHClient 定义了 SSH 连接检查接口，用于验证 SSH 信任关系和密码连接。
type SSHClient interface {
	CheckTrustConnection(ctx context.Context, endpoint machinedomain.Endpoint, user string) (bool, error)
	TestConnection(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) error
}

// SSHTrustService 定义了 SSH 免密配置服务接口。
type SSHTrustService interface {
	SetupPasswordless(ctx context.Context, endpoint machinedomain.Endpoint, auth machinedomain.SSHAuth) error
}

// Dependencies 是机器接入用例所需的外部依赖集合。
type Dependencies struct {
	MachineRepo machinedomain.Repository
	SSHClient   SSHClient
	Trust       SSHTrustService
}

// OnboardUsecase 是机器接入的用例，负责验证 SSH 连接并建立免密信任关系。
type OnboardUsecase struct {
	machineRepo machinedomain.Repository
	sshClient   SSHClient
	trust       SSHTrustService
}

// NewOnboardUsecase 创建一个新的机器接入用例实例。
func NewOnboardUsecase(dep Dependencies) *OnboardUsecase {
	return &OnboardUsecase{
		machineRepo: dep.MachineRepo,
		sshClient:   dep.SSHClient,
		trust:       dep.Trust,
	}
}

// OnboardMachineRequest 是机器接入的请求参数，包含机器基本信息和 SSH 凭证。
type OnboardMachineRequest struct {
	Name           string
	IP             string
	SSHPort        int
	SSHUser        string
	SSHPassword    string
	CredentialID   string
	CredentialName string
}

// OnboardMachineResponse 是机器接入的响应结果。
type OnboardMachineResponse struct {
	ID           string
	Name         string
	IP           string
	SSHPort      int
	SSHUser      string
	CredentialID string
	Status       string
	LastError    string
}

// Validate 验证机器接入请求参数的完整性和合法性。
func (r OnboardMachineRequest) Validate() error {
	switch {
	case strings.TrimSpace(r.Name) == "":
		return errors.New("name is required")
	case strings.TrimSpace(r.IP) == "":
		return errors.New("ip is required")
	case r.SSHPort <= 0:
		return errors.New("ssh_port must be positive")
	case strings.TrimSpace(r.SSHUser) == "":
		return errors.New("ssh_user is required")
	default:
		return nil
	}
}

// Execute 执行机器接入的完整流程，包括验证参数、检查 SSH 连接和建立免密信任。
func (u *OnboardUsecase) Execute(ctx context.Context, req OnboardMachineRequest) (OnboardMachineResponse, error) {
	if err := req.Validate(); err != nil {
		return OnboardMachineResponse{}, err
	}

	endpoint := machinedomain.Endpoint{IP: req.IP, SSHPort: req.SSHPort}
	password := strings.TrimSpace(req.SSHPassword)
	trustReady, err := u.sshClient.CheckTrustConnection(ctx, endpoint, req.SSHUser)
	if err != nil {
		return OnboardMachineResponse{}, err
	}
	entity := machinedomain.Machine{
		ID:           machinedomain.NewID(req.Name, req.IP, req.SSHPort),
		Name:         req.Name,
		IP:           req.IP,
		SSHPort:      req.SSHPort,
		SSHUser:      req.SSHUser,
		CredentialID: req.CredentialID,
		Cluster:      "",
		Status:       machinedomain.StatusSSHConnected,
	}
	if trustReady {
		entity.Status = machinedomain.StatusSSHTrustReady
	}
	saved, err := u.machineRepo.Save(ctx, entity)
	if err != nil {
		return OnboardMachineResponse{}, err
	}
	if !trustReady {
		if password == "" {
			return OnboardMachineResponse{}, errors.New("ssh trust is not ready, please provide ssh_password")
		}
		auth := machinedomain.SSHAuth{User: req.SSHUser, Password: password}
		if err := u.sshClient.TestConnection(ctx, endpoint, auth); err != nil {
			_ = u.machineRepo.UpdateStatus(ctx, saved.ID, machinedomain.StatusSSHFailed, err.Error())
			return OnboardMachineResponse{}, err
		}
		if u.trust == nil {
			err := errors.New("ssh trust service is not configured")
			_ = u.machineRepo.UpdateStatus(ctx, saved.ID, machinedomain.StatusSSHFailed, err.Error())
			return OnboardMachineResponse{}, err
		}
		if err := u.trust.SetupPasswordless(ctx, endpoint, auth); err != nil {
			_ = u.machineRepo.UpdateStatus(ctx, saved.ID, machinedomain.StatusSSHFailed, err.Error())
			return OnboardMachineResponse{}, err
		}
		trustReady, err = u.sshClient.CheckTrustConnection(ctx, endpoint, req.SSHUser)
		if err != nil {
			_ = u.machineRepo.UpdateStatus(ctx, saved.ID, machinedomain.StatusSSHFailed, err.Error())
			return OnboardMachineResponse{}, err
		}
		if !trustReady {
			err := errors.New("ssh trust setup finished but verification failed")
			_ = u.machineRepo.UpdateStatus(ctx, saved.ID, machinedomain.StatusSSHFailed, err.Error())
			return OnboardMachineResponse{}, err
		}
		if err := u.machineRepo.UpdateStatus(ctx, saved.ID, machinedomain.StatusSSHTrustReady, ""); err != nil {
			return OnboardMachineResponse{}, err
		}
		saved.Status = machinedomain.StatusSSHTrustReady
		saved.LastError = ""
	}

	return OnboardMachineResponse{
		ID:           saved.ID,
		Name:         saved.Name,
		IP:           saved.IP,
		SSHPort:      saved.SSHPort,
		SSHUser:      saved.SSHUser,
		CredentialID: saved.CredentialID,
		Status:       string(saved.Status),
		LastError:    saved.LastError,
	}, nil
}
