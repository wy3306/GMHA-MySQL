// Package config 提供 GMHA 管理端和 Agent 端的配置加载与校验功能。
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gmha/internal/buildinfo"
)

// ManagerConfig 是管理端（Manager）的配置结构体，包含监听地址、数据库路径、Agent 部署参数等。
type ManagerConfig struct {
	ListenAddr       string
	PublicURL        string
	DBPath           string
	AgentBinaryPath  string
	AgentInstallDir  string
	AgentConfigDir   string
	AgentServiceName string
	BootstrapTimeout time.Duration
}

// Validate 校验 ManagerConfig 中各必填字段是否已正确配置。
func (c ManagerConfig) Validate() error {
	switch {
	case strings.TrimSpace(c.PublicURL) == "":
		return errors.New("public-url is required")
	case strings.TrimSpace(c.DBPath) == "":
		return errors.New("db is required")
	case strings.TrimSpace(c.AgentBinaryPath) == "":
		return errors.New("agent-binary is required")
	case strings.TrimSpace(c.AgentInstallDir) == "":
		return errors.New("agent-install-dir is required")
	case strings.TrimSpace(c.AgentConfigDir) == "":
		return errors.New("agent-config-dir is required")
	case strings.TrimSpace(c.AgentServiceName) == "":
		return errors.New("agent-service-name is required")
	case c.BootstrapTimeout <= 0:
		return errors.New("bootstrap-timeout must be positive")
	default:
		return nil
	}
}

// AgentConfig 是 Agent 端的配置结构体，包含主机标识、引导令牌、管理端地址及心跳间隔等。
type AgentConfig struct {
	HostID            string
	BootstrapToken    string
	ManagerURL        string
	Hostname          string
	AdvertiseAddr     string
	Version           string
	HeartbeatInterval time.Duration
}

// LoadAgentConfig 从指定的环境变量风格配置文件中加载 Agent 配置。
func LoadAgentConfig(path string) (AgentConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return AgentConfig{}, err
	}
	defer file.Close()

	cfg := AgentConfig{
		Version:           buildinfo.CurrentVersion(),
		HeartbeatInterval: 15 * time.Second,
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return AgentConfig{}, fmt.Errorf("invalid env line: %s", line)
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch strings.TrimSpace(key) {
		case "HOST_ID":
			cfg.HostID = value
		case "BOOTSTRAP_TOKEN":
			cfg.BootstrapToken = value
		case "MANAGER_URL":
			cfg.ManagerURL = value
		case "HOSTNAME":
			cfg.Hostname = value
		case "ADVERTISE_ADDR":
			cfg.AdvertiseAddr = value
		case "VERSION":
			cfg.Version = value
		case "HEARTBEAT_INTERVAL":
			d, err := time.ParseDuration(value)
			if err != nil {
				return AgentConfig{}, err
			}
			cfg.HeartbeatInterval = d
		}
	}
	if err := scanner.Err(); err != nil {
		return AgentConfig{}, err
	}
	if cfg.HostID == "" || cfg.BootstrapToken == "" || cfg.ManagerURL == "" {
		return AgentConfig{}, errors.New("agent config requires HOST_ID, BOOTSTRAP_TOKEN and MANAGER_URL")
	}
	if cfg.Hostname == "" {
		hostname, _ := os.Hostname()
		cfg.Hostname = hostname
	}
	return cfg, nil
}
