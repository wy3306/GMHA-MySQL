package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	accountdomain "gmha/internal/domain/account"
	sqliteinfra "gmha/internal/infrastructure/persistence/sqlite"
	"golang.org/x/crypto/bcrypt"
)

const PlatformSessionDuration = 2 * time.Hour

type PermissionDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RoleDefinition struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

var platformPermissions = []PermissionDefinition{
	{ID: "overview.view", Name: "运行概览", Description: "查看平台运行状态和统计信息"},
	{ID: "resources.manage", Name: "资源管理", Description: "管理机器、SSH 凭证和 Agent"},
	{ID: "clusters.manage", Name: "集群运维", Description: "管理集群、拓扑、高可用和备份"},
	{ID: "database.manage", Name: "数据库管理", Description: "管理实例、SQL、性能与安装包"},
	{ID: "alerts.manage", Name: "告警管理", Description: "查看和调整告警规则、事件与推送渠道"},
	{ID: "automation.manage", Name: "AI 与自动化", Description: "使用 AI 运维和自动化能力"},
	{ID: "tasks.manage", Name: "任务中心", Description: "查看、控制和清理平台任务"},
	{ID: "platform.manage", Name: "平台运维", Description: "管理 Manager、升级和平台配置"},
}

var platformRoles = []RoleDefinition{
	{ID: "admin", Name: "管理员", Description: "拥有全部平台权限和账号管理能力", Permissions: []string{"*"}},
	{ID: "operator", Name: "运维人员", Description: "负责资源、集群、数据库、告警和任务运维", Permissions: []string{"overview.view", "resources.manage", "clusters.manage", "database.manage", "alerts.manage", "automation.manage", "tasks.manage"}},
	{ID: "dba", Name: "DBA", Description: "负责数据库、集群、性能和告警", Permissions: []string{"overview.view", "clusters.manage", "database.manage", "alerts.manage", "tasks.manage"}},
	{ID: "auditor", Name: "审计人员", Description: "以只读方式查看平台概览和任务", Permissions: []string{"overview.view", "tasks.manage"}},
}

type AccountService struct {
	repo               *sqliteinfra.AccountRepository
	demoAutoLoginAdmin bool
}

type AccountInput struct {
	Username    string   `json:"username"`
	Name        string   `json:"name"`
	Email       string   `json:"email"`
	Phone       string   `json:"phone"`
	Password    string   `json:"password"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
	Enabled     *bool    `json:"enabled"`
}

type AlertRecipientUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	HasEmail bool   `json:"has_email"`
}

type AlertRecipientRole struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	MemberCount          int    `json:"member_count"`
	EmailableMemberCount int    `json:"emailable_member_count"`
}

type AlertRecipientDirectory struct {
	Users []AlertRecipientUser `json:"users"`
	Roles []AlertRecipientRole `json:"roles"`
}

func NewAccountService(repo *sqliteinfra.AccountRepository) (*AccountService, error) {
	password := os.Getenv("GMHA_ADMIN_PASSWORD")
	if password == "" {
		password = "admin"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	if err := repo.EnsureAdmin(context.Background(), string(hash)); err != nil {
		return nil, err
	}
	return &AccountService{
		repo:               repo,
		demoAutoLoginAdmin: strings.EqualFold(strings.TrimSpace(os.Getenv("GMHA_DEMO_AUTO_LOGIN_ADMIN")), "true"),
	}, nil
}

func (s *AccountService) Catalog() map[string]any {
	return map[string]any{"permissions": platformPermissions, "roles": platformRoles, "session_duration_seconds": int(PlatformSessionDuration.Seconds())}
}

// AlertRecipientDirectory exposes only the platform-account metadata needed
// to configure notification routing. Email addresses remain server-side.
func (s *AccountService) AlertRecipientDirectory(ctx context.Context) (AlertRecipientDirectory, error) {
	users, err := s.repo.List(ctx)
	if err != nil {
		return AlertRecipientDirectory{}, err
	}
	directory := AlertRecipientDirectory{Users: []AlertRecipientUser{}, Roles: make([]AlertRecipientRole, 0, len(platformRoles))}
	roleIndex := make(map[string]int, len(platformRoles))
	for _, role := range platformRoles {
		roleIndex[role.ID] = len(directory.Roles)
		directory.Roles = append(directory.Roles, AlertRecipientRole{ID: role.ID, Name: role.Name})
	}
	for _, user := range users {
		if !user.Enabled {
			continue
		}
		hasEmail := validAccountEmail(user.Email)
		directory.Users = append(directory.Users, AlertRecipientUser{ID: user.ID, Username: user.Username, Name: user.Name, Role: user.Role, HasEmail: hasEmail})
		if index, ok := roleIndex[user.Role]; ok {
			directory.Roles[index].MemberCount++
			if hasEmail {
				directory.Roles[index].EmailableMemberCount++
			}
		}
	}
	return directory, nil
}

// ResolveAlertRecipientEmails resolves selected users and roles at delivery
// time so account changes take effect without exposing addresses to browsers.
func (s *AccountService) ResolveAlertRecipientEmails(ctx context.Context, userIDs, roleIDs []string) ([]string, error) {
	selectedUsers, selectedRoles := stringSet(userIDs), stringSet(roleIDs)
	if len(selectedUsers) == 0 && len(selectedRoles) == 0 {
		return nil, errors.New("请至少选择一名推送人员或一个平台角色")
	}
	validRoles := make(map[string]bool, len(platformRoles))
	for _, role := range platformRoles {
		validRoles[role.ID] = true
	}
	for role := range selectedRoles {
		if !validRoles[role] {
			return nil, fmt.Errorf("平台角色 %s 不存在", role)
		}
	}
	users, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]accountdomain.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	for id := range selectedUsers {
		user, ok := byID[id]
		if !ok || !user.Enabled {
			return nil, fmt.Errorf("选择的平台用户不存在或已停用")
		}
		if !validAccountEmail(user.Email) {
			return nil, fmt.Errorf("账号 %s 尚未设置有效邮箱", user.Username)
		}
	}
	emailSet := map[string]bool{}
	for _, user := range users {
		if !user.Enabled || (!selectedUsers[user.ID] && !selectedRoles[user.Role]) || !validAccountEmail(user.Email) {
			continue
		}
		emailSet[strings.ToLower(strings.TrimSpace(user.Email))] = true
	}
	emails := make([]string, 0, len(emailSet))
	for email := range emailSet {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	if len(emails) == 0 {
		return nil, errors.New("所选人员或角色中没有已配置邮箱的启用账号")
	}
	return emails, nil
}

func validAccountEmail(value string) bool {
	value = strings.TrimSpace(value)
	address, err := mail.ParseAddress(value)
	return err == nil && strings.EqualFold(address.Address, value)
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = true
		}
	}
	return out
}

func (s *AccountService) Login(ctx context.Context, username, password string) (string, accountdomain.User, time.Time, error) {
	user, err := s.repo.FindByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		return "", accountdomain.User{}, time.Time{}, errors.New("账号或密码错误")
	}
	if !user.Enabled {
		return "", accountdomain.User{}, time.Time{}, errors.New("账号已停用，请联系管理员")
	}
	return s.createSession(ctx, user)
}

// DemoAutoLoginAdmin creates an admin session only when the public demo
// deployment explicitly opts in. Production deployments keep the normal
// password flow because the switch is disabled by default.
func (s *AccountService) DemoAutoLoginAdmin(ctx context.Context) (string, accountdomain.User, time.Time, error) {
	if !s.demoAutoLoginAdmin {
		return "", accountdomain.User{}, time.Time{}, errors.New("演示环境免密登录未启用")
	}
	user, err := s.repo.FindByUsername(ctx, "admin")
	if err != nil {
		return "", accountdomain.User{}, time.Time{}, err
	}
	if !user.Enabled || user.Role != "admin" {
		return "", accountdomain.User{}, time.Time{}, errors.New("演示环境管理员账号不可用")
	}
	return s.createSession(ctx, user)
}

func (s *AccountService) createSession(ctx context.Context, user accountdomain.User) (string, accountdomain.User, time.Time, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", accountdomain.User{}, time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)
	now := time.Now().UTC()
	expires := now.Add(PlatformSessionDuration)
	if err := s.repo.CleanupSessions(ctx, now); err != nil {
		return "", accountdomain.User{}, time.Time{}, err
	}
	if err := s.repo.SaveSession(ctx, accountdomain.Session{TokenHash: hashSessionToken(token), UserID: user.ID, ExpiresAt: expires, CreatedAt: now}); err != nil {
		return "", accountdomain.User{}, time.Time{}, err
	}
	_ = s.repo.TouchLogin(ctx, user.ID, now)
	user.LastLoginAt = &now
	return token, user, expires, nil
}

func (s *AccountService) Authenticate(ctx context.Context, token string) (accountdomain.User, error) {
	if strings.TrimSpace(token) == "" {
		return accountdomain.User{}, sql.ErrNoRows
	}
	return s.repo.UserForSession(ctx, hashSessionToken(token), time.Now().UTC())
}

func (s *AccountService) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repo.DeleteSession(ctx, hashSessionToken(token))
}

func (s *AccountService) List(ctx context.Context, actor accountdomain.User) ([]accountdomain.User, error) {
	if !CanManageAccounts(actor) {
		return nil, errors.New("无账号管理权限")
	}
	return s.repo.List(ctx)
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_.-]{2,31}$`)

func (s *AccountService) Create(ctx context.Context, actor accountdomain.User, input AccountInput) (accountdomain.User, error) {
	if !CanManageAccounts(actor) {
		return accountdomain.User{}, errors.New("只有管理员可以创建账号")
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !usernamePattern.MatchString(input.Username) {
		return accountdomain.User{}, errors.New("账号需以字母开头，仅含字母、数字、点、下划线或短横线，长度 3-32 位")
	}
	if len(input.Password) < 8 {
		return accountdomain.User{}, errors.New("密码至少需要 8 位")
	}
	if strings.TrimSpace(input.Name) == "" {
		return accountdomain.User{}, errors.New("请填写姓名")
	}
	if err := validateAccountRole(input.Role); err != nil {
		return accountdomain.User{}, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		return accountdomain.User{}, err
	}
	now := time.Now().UTC()
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	user := accountdomain.User{ID: newAccountID(), Username: input.Username, Name: strings.TrimSpace(input.Name), Email: strings.TrimSpace(input.Email), Phone: strings.TrimSpace(input.Phone), PasswordHash: string(hash), Role: input.Role, Permissions: normalizeAccountPermissions(input.Role, input.Permissions), Enabled: enabled, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.Create(ctx, user); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return accountdomain.User{}, errors.New("账号已存在")
		}
		return accountdomain.User{}, err
	}
	return user, nil
}

func (s *AccountService) Update(ctx context.Context, actor accountdomain.User, id string, input AccountInput) (accountdomain.User, error) {
	if !CanManageAccounts(actor) {
		return accountdomain.User{}, errors.New("无账号管理权限")
	}
	user, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return accountdomain.User{}, err
	}
	if user.Username == "admin" && actor.Username != "admin" {
		return accountdomain.User{}, errors.New("默认 admin 账号只能由其自身调整")
	}
	if strings.TrimSpace(input.Name) == "" {
		return accountdomain.User{}, errors.New("请填写姓名")
	}
	if err := validateAccountRole(input.Role); err != nil {
		return accountdomain.User{}, err
	}
	user.Name, user.Email, user.Phone = strings.TrimSpace(input.Name), strings.TrimSpace(input.Email), strings.TrimSpace(input.Phone)
	user.Role, user.Permissions = input.Role, normalizeAccountPermissions(input.Role, input.Permissions)
	if input.Enabled != nil {
		user.Enabled = *input.Enabled
	}
	if input.Password != "" {
		if len(input.Password) < 8 && !(user.Username == "admin" && input.Password == "admin") {
			return accountdomain.User{}, errors.New("新密码至少需要 8 位")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
		if err != nil {
			return accountdomain.User{}, err
		}
		user.PasswordHash = string(hash)
	}
	if user.Username == "admin" {
		user.Role, user.Permissions, user.Enabled = "admin", []string{"*"}, true
	}
	user.UpdatedAt = time.Now().UTC()
	if err := s.repo.Update(ctx, user); err != nil {
		return accountdomain.User{}, err
	}
	if !user.Enabled || input.Password != "" {
		_ = s.repo.DeleteSessionsForUser(ctx, user.ID)
	}
	return user, nil
}

func (s *AccountService) Delete(ctx context.Context, actor accountdomain.User, id string) error {
	if !CanManageAccounts(actor) {
		return errors.New("无账号管理权限")
	}
	user, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if user.Username == "admin" {
		return errors.New("默认 admin 账号不能删除")
	}
	if user.ID == actor.ID {
		return errors.New("不能删除当前登录账号")
	}
	return s.repo.Delete(ctx, id)
}

func CanManageAccounts(user accountdomain.User) bool {
	return user.Username == "admin" || user.Role == "admin"
}

func HasPlatformPermission(user accountdomain.User, permission string) bool {
	if user.Username == "admin" || user.Role == "admin" {
		return true
	}
	for _, item := range user.Permissions {
		if item == "*" || item == permission {
			return true
		}
	}
	return false
}

func validateAccountRole(role string) error {
	for _, item := range platformRoles {
		if item.ID == role {
			return nil
		}
	}
	return fmt.Errorf("未知角色：%s", role)
}

func normalizeAccountPermissions(role string, permissions []string) []string {
	if role == "admin" {
		return []string{"*"}
	}
	allowed := map[string]bool{}
	for _, item := range platformPermissions {
		allowed[item.ID] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, item := range permissions {
		if allowed[item] && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}

func hashSessionToken(token string) string {
	value := sha256.Sum256([]byte(token))
	return hex.EncodeToString(value[:])
}

func newAccountID() string {
	value := make([]byte, 12)
	_, _ = rand.Read(value)
	return "user-" + hex.EncodeToString(value)
}
