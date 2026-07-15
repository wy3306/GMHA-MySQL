// profile.go 实现 MySQL 配置档案的加载和解析，定义不同环境下的数据库参数模板。
package mysql

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	configassets "gmha/configs"
)

// Profile 定义 MySQL 配置档案，包含缓冲池比例、连接数限制、缓冲区大小等参数模板。
type Profile struct {
	Name                    string
	BufferPoolRatio         float64
	MaxConnPerGB            int
	MaxMaxConnections       int
	RedoLogRatio            float64
	SortBufferSizeMB        int
	ReadBufferSizeMB        int
	ReadRndBufferMB         int
	JoinBufferSizeMB        int
	TableOpenCache          int
	ThreadCacheSize         int
	SysctlSwappiness        int
	OpenFilesLimit          int
	InnodbDataFileInitialMB int
	InnodbTempFileInitialMB int
}

// LoadProfile 从配置目录加载指定名称的 MySQL 配置档案文件并解析。
func LoadProfile(configRoot, name string) (Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "default"
	}
	path := filepath.Join(configRoot, "profiles", "mysql", name+".yaml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) && filepath.Clean(configRoot) == "configs" {
		data, err = configassets.ReadFile(filepath.Join("profiles", "mysql", name+".yaml"))
	}
	if err != nil {
		return Profile{}, err
	}
	return parseProfile(name, string(data))
}

// parseProfile 解析配置档案的 YAML 内容，将键值对映射到 Profile 结构体字段。
func parseProfile(name, content string) (Profile, error) {
	p := Profile{
		Name:                    name,
		BufferPoolRatio:         0.5,
		MaxConnPerGB:            20,
		MaxMaxConnections:       500,
		RedoLogRatio:            0.25,
		SortBufferSizeMB:        4,
		ReadBufferSizeMB:        2,
		ReadRndBufferMB:         2,
		JoinBufferSizeMB:        2,
		TableOpenCache:          2048,
		ThreadCacheSize:         64,
		SysctlSwappiness:        1,
		OpenFilesLimit:          65535,
		InnodbDataFileInitialMB: 128,
		InnodbTempFileInitialMB: 128,
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		switch key {
		case "buffer_pool_ratio":
			v, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return Profile{}, err
			}
			p.BufferPoolRatio = v
		case "max_conn_per_gb":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.MaxConnPerGB = v
		case "max_max_connections":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.MaxMaxConnections = v
		case "redo_log_ratio":
			v, err := strconv.ParseFloat(val, 64)
			if err != nil {
				return Profile{}, err
			}
			p.RedoLogRatio = v
		case "sort_buffer_size_mb":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.SortBufferSizeMB = v
		case "read_buffer_size_mb":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.ReadBufferSizeMB = v
		case "read_rnd_buffer_size_mb":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.ReadRndBufferMB = v
		case "join_buffer_size_mb":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.JoinBufferSizeMB = v
		case "table_open_cache":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.TableOpenCache = v
		case "thread_cache_size":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.ThreadCacheSize = v
		case "sysctl_swappiness":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.SysctlSwappiness = v
		case "open_files_limit":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.OpenFilesLimit = v
		case "innodb_data_file_initial_mb":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.InnodbDataFileInitialMB = v
		case "innodb_temp_file_initial_mb":
			v, err := strconv.Atoi(val)
			if err != nil {
				return Profile{}, err
			}
			p.InnodbTempFileInitialMB = v
		}
	}
	if p.BufferPoolRatio <= 0 || p.BufferPoolRatio >= 0.85 {
		return Profile{}, errors.New("invalid buffer_pool_ratio")
	}
	if p.MaxConnPerGB <= 0 || p.MaxMaxConnections <= 0 {
		return Profile{}, errors.New("invalid max connection profile")
	}
	if p.InnodbDataFileInitialMB <= 0 || p.InnodbTempFileInitialMB <= 0 {
		return Profile{}, errors.New("invalid innodb initial file size profile")
	}
	return p, nil
}
