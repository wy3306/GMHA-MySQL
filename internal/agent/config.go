package agent

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config 是代理进程的配置结构体，包含代理标识、管理端地址、心跳间隔等运行参数。
type Config struct {
	AgentID           string
	MachineID         string
	MachineIP         string
	InstallDir        string
	ManagerMode       string
	ManagerHTTPAddr   string
	ManagerGRPCAddr   string
	ManagerHTTPAddrs  []string
	ManagerGRPCAddrs  []string
	HeartbeatInterval time.Duration
	Token             string
}

// LoadConfig 从指定路径加载代理配置文件，解析 key:value 格式的配置项并返回 Config 结构体。
func LoadConfig(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	cfg := Config{
		ManagerMode:       "grpc",
		HeartbeatInterval: 5 * time.Second,
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		switch key {
		case "agent_id":
			cfg.AgentID = value
		case "machine_id":
			cfg.MachineID = value
		case "machine_ip":
			cfg.MachineIP = value
		case "install_dir":
			cfg.InstallDir = value
		case "manager_mode":
			cfg.ManagerMode = value
		case "manager_http_addr":
			cfg.ManagerHTTPAddr = value
		case "manager_grpc_addr":
			cfg.ManagerGRPCAddr = value
		case "heartbeat_interval":
			d, err := time.ParseDuration(value)
			if err != nil {
				if ms, convErr := strconv.Atoi(value); convErr == nil {
					d = time.Duration(ms) * time.Millisecond
				} else {
					return Config{}, err
				}
			}
			cfg.HeartbeatInterval = d
		case "token":
			cfg.Token = value
		}
	}
	if err := scanner.Err(); err != nil {
		return Config{}, err
	}
	if cfg.ManagerHTTPAddr == "" && cfg.ManagerGRPCAddr != "" {
		host, _, err := net.SplitHostPort(cfg.ManagerGRPCAddr)
		if err == nil && host != "" {
			cfg.ManagerHTTPAddr = fmt.Sprintf("http://%s:8080", host)
		}
	}
	cfg.ManagerHTTPAddrs = splitAddresses(cfg.ManagerHTTPAddr)
	cfg.ManagerGRPCAddrs = splitAddresses(cfg.ManagerGRPCAddr)
	if len(cfg.ManagerHTTPAddrs) > 0 {
		cfg.ManagerHTTPAddr = cfg.ManagerHTTPAddrs[0]
	}
	if len(cfg.ManagerGRPCAddrs) > 0 {
		cfg.ManagerGRPCAddr = cfg.ManagerGRPCAddrs[0]
	}
	if cfg.InstallDir == "" {
		cfg.InstallDir = filepath.Dir(path)
	}
	if cfg.AgentID == "" || cfg.MachineID == "" || cfg.MachineIP == "" || cfg.ManagerGRPCAddr == "" || cfg.ManagerHTTPAddr == "" {
		return Config{}, errors.New("agent config requires agent_id, machine_id, machine_ip, manager_http_addr and manager_grpc_addr")
	}
	return cfg, nil
}

func splitAddresses(value string) []string {
	seen := map[string]bool{}
	items := make([]string, 0, 2)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			items = append(items, part)
		}
	}
	return items
}
