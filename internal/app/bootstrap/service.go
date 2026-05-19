package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"gmha/internal/domain"
	"gmha/internal/platform/config"
	"gmha/internal/platform/system"
	"gmha/internal/ports"
	api "gmha/pkg/api/v1"
)

type Service struct {
	repo     ports.Repository
	ssh      ports.SSHClient
	renderer *system.Renderer
	cfg      config.ManagerConfig
}

func NewService(repo ports.Repository, ssh ports.SSHClient, renderer *system.Renderer, cfg config.ManagerConfig) *Service {
	return &Service{repo: repo, ssh: ssh, renderer: renderer, cfg: cfg}
}

func (s *Service) BootstrapHost(ctx context.Context, req api.BootstrapHostRequest) (api.BootstrapHostResponse, error) {
	if err := req.Validate(); err != nil {
		return api.BootstrapHostResponse{}, err
	}

	host, err := s.repo.UpsertHost(ctx, domain.Host{
		ID:             stableHostID(req.Name, req.Address),
		Name:           req.Name,
		Address:        req.Address,
		Cluster:        req.Cluster,
		SSHPort:        req.SSHPort,
		SSHUser:        req.SSHUser,
		BootstrapState: domain.HostBootstrapPending,
	})
	if err != nil {
		return api.BootstrapHostResponse{}, err
	}

	if err := s.repo.UpdateHostBootstrapState(ctx, host.ID, domain.HostBootstrapRunning, ""); err != nil {
		return api.BootstrapHostResponse{}, err
	}

	token, tokenHash, err := issueBootstrapToken()
	if err != nil {
		return api.BootstrapHostResponse{}, err
	}
	if err := s.repo.SaveBootstrapToken(ctx, domain.BootstrapToken{
		HostID:    host.ID,
		TokenHash: tokenHash,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(s.cfg.BootstrapTimeout),
	}); err != nil {
		return api.BootstrapHostResponse{}, err
	}

	if err := s.pushAndStartAgent(ctx, host, token, req.SSHPassword); err != nil {
		_ = s.repo.UpdateHostBootstrapState(ctx, host.ID, domain.HostBootstrapFailed, err.Error())
		return api.BootstrapHostResponse{}, err
	}

	deadline := time.Now().Add(s.cfg.BootstrapTimeout)
	for time.Now().Before(deadline) {
		agent, ok, err := s.repo.GetAgentByHostID(ctx, host.ID)
		if err != nil {
			return api.BootstrapHostResponse{}, err
		}
		if ok {
			_ = s.repo.UpdateHostBootstrapState(ctx, host.ID, domain.HostBootstrapSucceeded, "")
			return api.BootstrapHostResponse{
				HostID:          host.ID,
				HostName:        host.Name,
				BootstrapState:  domain.HostBootstrapSucceeded,
				AgentID:         agent.ID,
				AgentRegistered: true,
			}, nil
		}
		select {
		case <-ctx.Done():
			return api.BootstrapHostResponse{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	err = errors.New("agent did not register before bootstrap timeout")
	_ = s.repo.UpdateHostBootstrapState(ctx, host.ID, domain.HostBootstrapFailed, err.Error())
	return api.BootstrapHostResponse{}, err
}

func (s *Service) pushAndStartAgent(ctx context.Context, host domain.Host, token, sshPassword string) error {
	binary, err := os.ReadFile(s.cfg.AgentBinaryPath)
	if err != nil {
		return fmt.Errorf("read agent binary: %w", err)
	}

	agentCfg, err := s.renderer.RenderAgentEnv(system.AgentEnvData{
		HostID:            host.ID,
		BootstrapToken:    token,
		ManagerURL:        s.cfg.PublicURL,
		HeartbeatInterval: 15 * time.Second,
		Version:           "0.1.0",
	})
	if err != nil {
		return err
	}

	unit, err := s.renderer.RenderSystemdUnit()
	if err != nil {
		return err
	}

	stagingDir := path.Join("/tmp", "gmha-bootstrap", host.ID)
	files := []ports.UploadInput{
		{
			Address:     host.Address,
			Port:        host.SSHPort,
			User:        host.SSHUser,
			Password:    sshPassword,
			RemotePath:  path.Join(stagingDir, "agent"),
			Permissions: 0o755,
			Content:     binary,
		},
		{
			Address:     host.Address,
			Port:        host.SSHPort,
			User:        host.SSHUser,
			Password:    sshPassword,
			RemotePath:  path.Join(stagingDir, "agent.env"),
			Permissions: 0o644,
			Content:     []byte(agentCfg),
		},
		{
			Address:     host.Address,
			Port:        host.SSHPort,
			User:        host.SSHUser,
			Password:    sshPassword,
			RemotePath:  path.Join(stagingDir, s.cfg.AgentServiceName+".service"),
			Permissions: 0o644,
			Content:     []byte(unit),
		},
	}

	if _, err := s.ssh.Run(ctx, ports.RunInput{
		Address:  host.Address,
		Port:     host.SSHPort,
		User:     host.SSHUser,
		Password: sshPassword,
		Command:  "mkdir -p " + shellQuote(stagingDir),
		Timeout:  30,
	}); err != nil {
		return fmt.Errorf("prepare remote staging dir: %w", err)
	}

	for _, file := range files {
		if err := s.ssh.Upload(ctx, file); err != nil {
			return fmt.Errorf("upload %s: %w", file.RemotePath, err)
		}
	}

	remoteInstall := s.remoteInstallScript(host.SSHUser, sshPassword, stagingDir)
	if _, err := s.ssh.Run(ctx, ports.RunInput{
		Address:  host.Address,
		Port:     host.SSHPort,
		User:     host.SSHUser,
		Password: sshPassword,
		Command:  remoteInstall,
		Timeout:  120,
	}); err != nil {
		return fmt.Errorf("install remote agent: %w", err)
	}

	return nil
}

func (s *Service) remoteInstallScript(sshUser, sshPassword, stagingDir string) string {
	body := strings.Join([]string{
		"set -eu",
		"install -d -m 0755 " + shellQuote(s.cfg.AgentInstallDir),
		"install -d -m 0755 " + shellQuote(s.cfg.AgentConfigDir),
		"install -m 0755 " + shellQuote(path.Join(stagingDir, "agent")) + " " + shellQuote(path.Join(s.cfg.AgentInstallDir, "agent")),
		"install -m 0644 " + shellQuote(path.Join(stagingDir, "agent.env")) + " " + shellQuote(path.Join(s.cfg.AgentConfigDir, "agent.env")),
		"install -m 0644 " + shellQuote(path.Join(stagingDir, s.cfg.AgentServiceName+".service")) + " " + shellQuote(path.Join("/etc/systemd/system", s.cfg.AgentServiceName+".service")),
		"systemctl daemon-reload",
		"systemctl enable --now " + shellQuote(s.cfg.AgentServiceName+".service"),
	}, "\n")

	if sshUser == "root" {
		return body
	}
	return fmt.Sprintf("printf '%%s\\n' %s | sudo -S sh -lc %s", shellQuote(sshPassword), shellQuote(body))
}

func issueBootstrapToken() (string, string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token := hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(hash[:]), nil
}

func stableHostID(name, address string) string {
	sum := sha256.Sum256([]byte(name + "|" + address))
	return "host-" + hex.EncodeToString(sum[:8])
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
