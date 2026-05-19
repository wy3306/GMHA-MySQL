// Package mysql 提供 MySQL 实例管理的核心功能，包括账号初始化、配置计算、安装包选择等。
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
)

// MySQL 账号角色常量，定义系统中使用的三种账号角色。
const (
	// AccountRoleMonitor 是监控账号角色，用于数据库健康检查和状态采集。
	AccountRoleMonitor = "monitor"
	// AccountRoleMHA 是 MHA 管理账号角色，用于高可用切换和故障恢复操作。
	AccountRoleMHA = "mha"
	// AccountRoleBackup 是备份账号角色，用于数据库备份操作。
	AccountRoleBackup = "backup"

	// DefaultAccountPassword 是账号的默认密码。
	DefaultAccountPassword = "3306niubi"
	// DefaultAccountHost 是账号的默认主机地址，表示允许所有主机连接。
	DefaultAccountHost = "%"
)

// AccountSpec 定义 MySQL 账号的规格信息，包括角色、用户名、密码、主机等配置。
type AccountSpec struct {
	Role           string `json:"role"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	Host           string `json:"host"`
	Enabled        bool   `json:"enabled"`
	ExtendedBackup bool   `json:"extended_backup,omitempty"`
}

// AccountInitConfig 定义账号初始化的配置，包含是否启用和账号列表。
type AccountInitConfig struct {
	Enabled  bool          `json:"enabled"`
	Accounts []AccountSpec `json:"accounts"`
}

// AccountInitResult 定义账号初始化的整体结果，包含成功状态、汇总信息和各项明细。
type AccountInitResult struct {
	Enabled        bool                    `json:"enabled"`
	Success        bool                    `json:"success"`
	PartialSuccess bool                    `json:"partial_success"`
	Retryable      bool                    `json:"retryable"`
	Summary        string                  `json:"summary"`
	Items          []AccountInitItemResult `json:"items"`
}

// AccountInitItemResult 定义单个账号初始化的结果，包含创建、授权等各步骤的执行状态。
type AccountInitItemResult struct {
	Role            string   `json:"role"`
	Username        string   `json:"username"`
	Host            string   `json:"host"`
	Enabled         bool     `json:"enabled"`
	Skipped         bool     `json:"skipped"`
	UserCreated     bool     `json:"user_created"`
	PasswordUpdated bool     `json:"password_updated"`
	Granted         bool     `json:"granted"`
	Success         bool     `json:"success"`
	Retryable       bool     `json:"retryable"`
	Error           string   `json:"error"`
	ExecutedSteps   []string `json:"executed_steps"`
}

// AccountInitializer 是账号初始化器，负责通过 Unix Socket 连接 MySQL 并执行账号创建和授权操作。
type AccountInitializer struct {
	Socket       string
	RootPassword string
	Timeout      time.Duration
}

// 账号名称和主机地址的正则表达式验证规则。
var (
	accountNameRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}$`)
	accountHostRE = regexp.MustCompile(`^[A-Za-z0-9_%.\-:/]+$`)
)

// DefaultAccountSpecs 返回系统默认的三个账号规格：monitor、mha 和 backup。
func DefaultAccountSpecs() []AccountSpec {
	return []AccountSpec{
		{Role: AccountRoleMonitor, Username: AccountRoleMonitor, Password: DefaultAccountPassword, Host: DefaultAccountHost, Enabled: true},
		{Role: AccountRoleMHA, Username: AccountRoleMHA, Password: DefaultAccountPassword, Host: DefaultAccountHost, Enabled: true},
		{Role: AccountRoleBackup, Username: AccountRoleBackup, Password: DefaultAccountPassword, Host: DefaultAccountHost, Enabled: true},
	}
}

// NormalizeAccountSpecs 将用户输入的账号规格与默认值合并，填充缺失的字段并保持角色顺序。
func NormalizeAccountSpecs(input []AccountSpec) []AccountSpec {
	defaults := DefaultAccountSpecs()
	byRole := make(map[string]AccountSpec, len(defaults))
	order := make([]string, 0, len(defaults)+len(input))
	for _, item := range defaults {
		role := normalizeRole(item.Role)
		byRole[role] = item
		order = append(order, role)
	}
	for _, item := range input {
		role := normalizeRole(item.Role)
		if role == "" {
			continue
		}
		base, ok := byRole[role]
		if !ok {
			base = AccountSpec{Role: role, Enabled: item.Enabled}
			order = append(order, role)
		}
		base.Role = role
		if strings.TrimSpace(item.Username) != "" {
			base.Username = strings.TrimSpace(item.Username)
		}
		if strings.TrimSpace(item.Password) != "" {
			base.Password = item.Password
		}
		if strings.TrimSpace(item.Host) != "" {
			base.Host = strings.TrimSpace(item.Host)
		}
		base.Enabled = item.Enabled
		base.ExtendedBackup = item.ExtendedBackup
		byRole[role] = base
	}
	out := make([]AccountSpec, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, role := range order {
		if seen[role] {
			continue
		}
		seen[role] = true
		item := byRole[role]
		if strings.TrimSpace(item.Username) == "" {
			item.Username = role
		}
		if strings.TrimSpace(item.Password) == "" {
			item.Password = DefaultAccountPassword
		}
		if strings.TrimSpace(item.Host) == "" {
			item.Host = DefaultAccountHost
		}
		out = append(out, item)
	}
	return out
}

// ValidateAccountSpecs 验证账号规格列表的合法性，检查用户名、主机地址和密码是否符合规范。
func ValidateAccountSpecs(items []AccountSpec) error {
	for _, item := range items {
		if !item.Enabled {
			continue
		}
		if normalizeRole(item.Role) == "" {
			return errors.New("account role is required")
		}
		if !accountNameRE.MatchString(item.Username) {
			return fmt.Errorf("invalid mysql account username for role %s: %s", item.Role, item.Username)
		}
		if !accountHostRE.MatchString(item.Host) {
			return fmt.Errorf("invalid mysql account host for role %s: %s", item.Role, item.Host)
		}
		if strings.TrimSpace(item.Password) == "" {
			return fmt.Errorf("mysql account password is required for role %s", item.Role)
		}
	}
	return nil
}

// WaitReady 等待 MySQL 实例就绪，通过多次尝试连接来检测服务是否可用。
func (i AccountInitializer) WaitReady(ctx context.Context, attempts int, interval time.Duration) error {
	if attempts <= 0 {
		attempts = 30
	}
	if interval <= 0 {
		interval = time.Second
	}
	var last error
	for n := 0; n < attempts; n++ {
		db, err := i.openDB()
		if err == nil {
			pingCtx, cancel := context.WithTimeout(ctx, i.timeout())
			err = db.PingContext(pingCtx)
			cancel()
			_ = db.Close()
		}
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("mysql connection is not ready after %d attempts: %w", attempts, last)
}

// Initialize 执行账号初始化流程，包括验证配置、连接数据库并逐个创建账号和授权。
func (i AccountInitializer) Initialize(ctx context.Context, specs []AccountSpec) AccountInitResult {
	specs = NormalizeAccountSpecs(specs)
	result := AccountInitResult{Enabled: true, Items: make([]AccountInitItemResult, 0, len(specs))}
	if err := ValidateAccountSpecs(specs); err != nil {
		result.Success = false
		result.Retryable = false
		result.Summary = "账号配置非法: " + err.Error()
		result.Items = append(result.Items, AccountInitItemResult{
			Enabled: false,
			Error:   err.Error(),
		})
		return result
	}

	db, err := i.openDB()
	if err != nil {
		return accountInitConnectFailure(err)
	}
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, i.timeout())
	err = db.PingContext(pingCtx)
	cancel()
	if err != nil {
		return accountInitConnectFailure(err)
	}

	for _, spec := range specs {
		item := i.initializeOne(ctx, db, spec)
		result.Items = append(result.Items, item)
	}
	successCount := 0
	enabledCount := 0
	for _, item := range result.Items {
		if !item.Enabled || item.Skipped {
			continue
		}
		enabledCount++
		if item.Success {
			successCount++
		}
		if item.Retryable {
			result.Retryable = true
		}
	}
	result.Success = enabledCount == successCount
	result.PartialSuccess = successCount > 0 && successCount < enabledCount
	result.Summary = fmt.Sprintf("账号初始化完成: success=%d/%d", successCount, enabledCount)
	if !result.Success {
		result.Summary = fmt.Sprintf("账号初始化部分失败: success=%d/%d", successCount, enabledCount)
	}
	return result
}

func (i AccountInitializer) initializeOne(ctx context.Context, db *sql.DB, spec AccountSpec) AccountInitItemResult {
	item := AccountInitItemResult{
		Role:     spec.Role,
		Username: spec.Username,
		Host:     spec.Host,
		Enabled:  spec.Enabled,
	}
	if !spec.Enabled {
		item.Skipped = true
		item.Success = true
		item.ExecutedSteps = append(item.ExecutedSteps, "skipped")
		return item
	}
	steps := accountSQLSteps(spec)
	for _, step := range steps {
		execCtx, cancel := context.WithTimeout(ctx, i.timeout())
		_, err := db.ExecContext(execCtx, step.SQL)
		cancel()
		if err != nil {
			item.Error = classifyAccountStepError(step.Name, err)
			item.Retryable = isRetryableMySQLError(err)
			item.Success = false
			return item
		}
		item.ExecutedSteps = append(item.ExecutedSteps, step.Name)
		switch step.Name {
		case "create_user":
			item.UserCreated = true
		case "alter_user":
			item.PasswordUpdated = true
		case "grant_base", "grant_dynamic":
			item.Granted = true
		}
	}
	item.Success = true
	return item
}

func (i AccountInitializer) openDB() (*sql.DB, error) {
	cfg := mysqlDriver.NewConfig()
	cfg.User = "root"
	cfg.Passwd = i.RootPassword
	cfg.Net = "unix"
	cfg.Addr = i.Socket
	cfg.Timeout = i.timeout()
	cfg.ReadTimeout = i.timeout()
	cfg.WriteTimeout = i.timeout()
	cfg.Params = map[string]string{"multiStatements": "true"}
	return sql.Open("mysql", cfg.FormatDSN())
}

func (i AccountInitializer) timeout() time.Duration {
	if i.Timeout <= 0 {
		return 5 * time.Second
	}
	return i.Timeout
}

// accountSQLStep 定义账号初始化的单个 SQL 步骤，包含步骤名称和对应的 SQL 语句。
type accountSQLStep struct {
	Name string
	SQL  string
}

// accountSQLSteps 根据账号规格生成完整的 SQL 步骤列表，包括创建用户、修改密码、授权等。
func accountSQLSteps(spec AccountSpec) []accountSQLStep {
	user := accountIdent(spec.Username, spec.Host)
	steps := []accountSQLStep{
		{Name: "create_user", SQL: fmt.Sprintf("CREATE USER IF NOT EXISTS %s IDENTIFIED BY %s", user, sqlString(spec.Password))},
		{Name: "alter_user", SQL: fmt.Sprintf("ALTER USER %s IDENTIFIED BY %s", user, sqlString(spec.Password))},
	}
	steps = append(steps, accountSQLStep{Name: "grant_base", SQL: grantSQL(spec, basePrivileges(spec))})
	if dynamic := dynamicPrivileges(spec); len(dynamic) > 0 {
		// MySQL 8.0 dynamic privileges vary by distribution/edition. Keep this as
		// a separate step so failures clearly point to privilege compatibility.
		steps = append(steps, accountSQLStep{Name: "grant_dynamic", SQL: grantSQL(spec, dynamic)})
	}
	return append(steps, accountSQLStep{Name: "flush_privileges", SQL: "FLUSH PRIVILEGES"})
}

// basePrivileges 根据账号角色返回基础权限列表，不同角色拥有不同的数据库操作权限。
func basePrivileges(spec AccountSpec) []string {
	switch spec.Role {
	case AccountRoleMonitor:
		return []string{"SELECT", "PROCESS", "REPLICATION CLIENT"}
	case AccountRoleMHA:
		return []string{"CREATE", "ALTER", "DROP", "INSERT", "UPDATE", "DELETE", "SELECT", "SHOW VIEW", "TRIGGER", "EVENT", "PROCESS", "RELOAD", "LOCK TABLES", "REPLICATION CLIENT", "REPLICATION SLAVE", "CONNECTION_ADMIN"}
	case AccountRoleBackup:
		return []string{"SELECT", "PROCESS", "RELOAD", "LOCK TABLES", "REPLICATION CLIENT"}
	default:
		return []string{"SELECT"}
	}
}

// dynamicPrivileges 根据账号角色返回 MySQL 8.0 动态权限列表，如 BACKUP_ADMIN、CLONE_ADMIN 等。
func dynamicPrivileges(spec AccountSpec) []string {
	switch spec.Role {
	case AccountRoleMHA:
		return []string{"BACKUP_ADMIN", "CLONE_ADMIN"}
	case AccountRoleBackup:
		if spec.ExtendedBackup {
			return []string{"BACKUP_ADMIN", "CLONE_ADMIN"}
		}
	}
	return nil
}

// grantSQL 生成 GRANT 语句，将指定权限授予对应账号。
func grantSQL(spec AccountSpec, privileges []string) string {
	return fmt.Sprintf("GRANT %s ON *.* TO %s", strings.Join(privileges, ", "), accountIdent(spec.Username, spec.Host))
}

// accountIdent 生成 MySQL 账号标识符，格式为 'username'@'host'。
func accountIdent(username, host string) string {
	return sqlString(username) + "@" + sqlString(host)
}

// sqlString 将字符串值转换为 SQL 安全的单引号字符串，处理转义字符。
func sqlString(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// normalizeRole 将角色名称转换为小写并去除首尾空格。
func normalizeRole(role string) string {
	return strings.ToLower(strings.TrimSpace(role))
}

// accountInitConnectFailure 构建连接失败时的初始化结果，标记是否可重试。
func accountInitConnectFailure(err error) AccountInitResult {
	retryable := isRetryableMySQLError(err)
	return AccountInitResult{
		Enabled:   true,
		Success:   false,
		Retryable: retryable,
		Summary:   "连接 MySQL 失败: " + err.Error(),
		Items: []AccountInitItemResult{{
			Enabled:   true,
			Success:   false,
			Retryable: retryable,
			Error:     "连接失败: " + err.Error(),
		}},
	}
}

// classifyAccountStepError 根据执行步骤对错误信息进行分类和格式化。
func classifyAccountStepError(step string, err error) string {
	switch step {
	case "create_user":
		return "创建账号失败: " + err.Error()
	case "alter_user":
		return "更新密码失败: " + err.Error()
	case "grant_base", "grant_dynamic":
		return "授权失败: " + err.Error()
	default:
		return step + "失败: " + err.Error()
	}
}

// isRetryableMySQLError 判断 MySQL 错误是否可重试，如连接超时、连接拒绝等临时性错误。
func isRetryableMySQLError(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if errors.As(err, &mysqlErr) {
		// 1045 means root/admin password or account is wrong; retrying without
		// changing credentials will not help.
		if mysqlErr.Number == 1045 {
			return false
		}
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "connection refused") ||
		strings.Contains(text, "can't connect") ||
		strings.Contains(text, "i/o timeout") ||
		strings.Contains(text, "timeout") ||
		strings.Contains(text, "connection reset")
}
