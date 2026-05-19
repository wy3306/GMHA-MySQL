package selfcheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gmha/internal/agent/mysqlcheck"
	hbdomain "gmha/internal/domain/heartbeat"
)

// Checker 是 Agent 自检器，负责检查 Agent 运行时状态、磁盘可写性和 MySQL 心跳健康状态。
type Checker struct {
	installDir string
	mysql      *mysqlcheck.Checker
}

// NewChecker 创建一个新的 Agent 自检器实例，installDir 为 Agent 安装目录。
func NewChecker(installDir string) *Checker {
	return &Checker{
		installDir: installDir,
		mysql:      mysqlcheck.NewChecker(filepath.Join(installDir, mysqlcheck.DefaultConfigFile)),
	}
}

// Run 执行所有自检项目，包括运行时循环、配置加载、磁盘可写性和 MySQL 心跳检查，返回综合健康等级。
func (c *Checker) Run(ctx context.Context) (hbdomain.HealthLevel, string, []hbdomain.HealthCheck) {
	now := time.Now().UTC()
	checks := []hbdomain.HealthCheck{
		{Name: "runtime.loop", Status: hbdomain.CheckOK, Detail: "heartbeat loop running", CheckedAt: now},
		{Name: "config.load", Status: hbdomain.CheckOK, Detail: "config is loaded", CheckedAt: now},
	}
	tmpPath := filepath.Join(c.installDir, ".hb-check")
	if err := os.WriteFile(tmpPath, []byte(now.Format(time.RFC3339)), 0o644); err != nil {
		checks = append(checks, hbdomain.HealthCheck{
			Name:      "disk.write_tmp",
			Status:    hbdomain.CheckFail,
			Detail:    err.Error(),
			CheckedAt: now,
		})
		return hbdomain.HealthUnhealthy, "install dir is not writable", checks
	}
	_ = os.Remove(tmpPath)
	checks = append(checks, hbdomain.HealthCheck{
		Name:      "disk.write_tmp",
		Status:    hbdomain.CheckOK,
		Detail:    "install dir is writable",
		CheckedAt: now,
	})
	checks = append(checks, c.mysql.Check(ctx)...)
	for _, item := range checks {
		if strings.HasPrefix(item.Name, "mysql.heartbeat.") {
			continue
		}
		if item.Status == hbdomain.CheckFail {
			return hbdomain.HealthUnhealthy, "one or more checks failed", checks
		}
		if item.Status == hbdomain.CheckWarn {
			return hbdomain.HealthDegraded, "one or more checks are degraded", checks
		}
	}
	return hbdomain.HealthHealthy, "all quick checks passed", checks
}
