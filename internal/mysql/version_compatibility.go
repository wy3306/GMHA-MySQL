package mysql

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// MySQLConfigOption is a version-specific option rendered into my.cnf.
type MySQLConfigOption struct {
	Name  string
	Value string
}

// RuntimeParameterField describes an installation input supported by a
// specific MySQL release. The Manager returns this metadata with each package
// so the UI and the server-side validator use the same compatibility rules.
type RuntimeParameterField struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Default     string   `json:"default,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Options     []string `json:"options,omitempty"`
	Description string   `json:"description,omitempty"`
}

type RuntimeParameterGroup struct {
	Name   string                  `json:"name"`
	Fields []RuntimeParameterField `json:"fields"`
}

type mysqlVersion struct {
	Major int
	Minor int
	Patch int
}

// MySQL57UpgradeBridgeVersion is the last MySQL 5.7 release and the required
// bridge before GMHA permits an in-place upgrade into the 8.0 release family.
// Keeping the value in one place prevents package filtering and API validation
// from quietly using different boundaries.
const MySQL57UpgradeBridgeVersion = "5.7.44"

// VersionCapabilities centralizes every server-version boundary used by
// installation, topology, account, parameter and tool workflows. Keeping
// these decisions together prevents an option accepted by one workflow from
// producing an invalid command in another one.
type VersionCapabilities struct {
	Version                   string
	ReleaseTrack              string
	Legacy57                  bool
	LegacyTransactionVariable bool
	LegacyReplicationNames    bool
	LegacyRedoLog             bool
	SupportsClone             bool
	SupportsHistograms        bool
	SupportsSetPersist        bool
	SupportsDynamicPrivileges bool
	SupportsPerconaToolkit    bool
	MinimumPerconaToolkit     string
	XtraBackupSeries          string
}

// CapabilitiesForVersion returns the verified feature boundaries for a MySQL
// Community Server release supported by GMHA.
func CapabilitiesForVersion(raw string) (VersionCapabilities, error) {
	v, err := validateSupportedMySQLVersion(raw)
	if err != nil {
		return VersionCapabilities{}, err
	}
	legacy57 := v.Major == 5
	capabilities := VersionCapabilities{
		Version:                   strings.TrimSpace(raw),
		ReleaseTrack:              mysqlReleaseTrack(v),
		Legacy57:                  legacy57,
		LegacyTransactionVariable: legacy57 && compareMySQLVersion(v, mysqlVersion{Major: 5, Minor: 7, Patch: 20}) < 0,
		LegacyReplicationNames:    compareMySQLVersion(v, mysqlVersion{Major: 8, Minor: 0, Patch: 26}) < 0,
		LegacyRedoLog:             compareMySQLVersion(v, mysqlVersion{Major: 8, Minor: 0, Patch: 30}) < 0,
		SupportsClone:             compareMySQLVersion(v, mysqlVersion{Major: 8, Minor: 0, Patch: 17}) >= 0,
		SupportsHistograms:        v.Major >= 8,
		SupportsSetPersist:        v.Major >= 8,
		SupportsDynamicPrivileges: compareMySQLVersion(v, mysqlVersion{Major: 8, Minor: 0, Patch: 17}) >= 0,
		SupportsPerconaToolkit:    true,
		MinimumPerconaToolkit:     "3.7.1",
		XtraBackupSeries:          fmt.Sprintf("%d.%d", v.Major, v.Minor),
	}
	if legacy57 {
		capabilities.MinimumPerconaToolkit = "3.5.0"
		capabilities.XtraBackupSeries = "2.4"
	}
	return capabilities, nil
}

// SupportsHistogramForVersion reports whether GMHA may manage optimizer
// histograms on the server. MySQL 5.7 has no COLUMN_STATISTICS table or
// ANALYZE TABLE ... HISTOGRAM syntax, so this boundary must stay explicit.
// Server version strings may contain a vendor suffix (for example
// "8.0.40-0ubuntu0.22.04.1"), which is ignored after the numeric prefix.
func SupportsHistogramForVersion(raw string) bool {
	match := regexp.MustCompile(`^\s*(\d+\.\d+(?:\.\d+)?)`).FindStringSubmatch(raw)
	if len(match) != 2 {
		return false
	}
	parts := strings.Split(match[1], ".")
	if len(parts) == 2 {
		major, _ := strconv.Atoi(parts[0])
		minor, _ := strconv.Atoi(parts[1])
		return (major == 8 && minor <= 4) || (major == 9 && minor <= 7)
	}
	capabilities, err := CapabilitiesForVersion(match[1])
	return err == nil && capabilities.SupportsHistograms
}

// SupportsCloneForVersion reports whether the server includes the Clone
// plugin. It was introduced in MySQL 8.0.17.
func SupportsCloneForVersion(raw string) bool {
	capabilities, err := CapabilitiesForVersion(raw)
	return err == nil && capabilities.SupportsClone
}

// SupportsDynamicPrivilegeForVersion applies a conservative boundary shared
// by all platform workflows. Before 8.0.17 GMHA uses SUPER instead, avoiding
// partial grants across the early 8.0 dynamic-privilege rollout.
func SupportsDynamicPrivilegeForVersion(raw, privilege string) bool {
	capabilities, err := CapabilitiesForVersion(raw)
	if err != nil || !capabilities.SupportsDynamicPrivileges {
		return false
	}
	privilege = strings.ToUpper(strings.TrimSpace(privilege))
	for _, item := range []string{"CONNECTION_ADMIN", "SYSTEM_VARIABLES_ADMIN", "REPLICATION_SLAVE_ADMIN", "BACKUP_ADMIN", "CLONE_ADMIN"} {
		if privilege == item {
			return true
		}
	}
	return false
}

// IsMySQL57 reports whether raw identifies a supported MySQL 5.7 release.
func IsMySQL57(raw string) bool {
	v, err := validateSupportedMySQLVersion(raw)
	return err == nil && v.Major == 5 && v.Minor == 7
}

func parseMySQLVersion(raw string) (mysqlVersion, error) {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) < 2 || len(parts) > 3 {
		return mysqlVersion{}, errors.New("invalid mysql version: " + raw)
	}
	values := [3]int{}
	for i, part := range parts {
		if part == "" {
			return mysqlVersion{}, errors.New("invalid mysql version: " + raw)
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return mysqlVersion{}, errors.New("invalid mysql version: " + raw)
		}
		values[i] = value
	}
	return mysqlVersion{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

func compareMySQLVersion(a, b mysqlVersion) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch != b.Patch {
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	return 0
}

func validateSupportedMySQLVersion(raw string) (mysqlVersion, error) {
	v, err := parseMySQLVersion(raw)
	if err != nil {
		return mysqlVersion{}, err
	}
	switch {
	case v.Major == 5 && v.Minor == 7:
		if v.Patch < 9 {
			return mysqlVersion{}, fmt.Errorf("mysql %s is unsupported; minimum supported 5.7 version is 5.7.9", raw)
		}
		if v.Patch > 44 {
			return mysqlVersion{}, fmt.Errorf("mysql %s does not exist in the completed MySQL 5.7 release line; maximum version is 5.7.44", raw)
		}
	case v.Major == 8:
		if v.Minor > 4 {
			return mysqlVersion{}, fmt.Errorf("mysql %s is not a released MySQL 8 series; supported 8.x series are 8.0 through 8.4", raw)
		}
		minimum := mysqlVersion{Major: 8, Minor: 0, Patch: 11}
		if compareMySQLVersion(v, minimum) < 0 {
			return mysqlVersion{}, fmt.Errorf("mysql %s is unsupported; minimum generally available 8.0 version is 8.0.11", raw)
		}
	case v.Major == 9:
		if v.Minor > 7 {
			return mysqlVersion{}, fmt.Errorf("mysql %s is not a released MySQL 9 series; supported 9.x series are 9.0 through 9.7", raw)
		}
	default:
		return mysqlVersion{}, fmt.Errorf("mysql %s is not verified; supported release families are 5.7, 8.x and 9.x", raw)
	}
	return v, nil
}

func mysqlReleaseTrack(v mysqlVersion) string {
	switch {
	case v.Major == 5 && v.Minor == 7:
		return "5.7 Legacy"
	case v.Major == 8 && v.Minor == 0:
		return "8.0 Bugfix"
	case v.Major == 8 && v.Minor == 4:
		return "8.4 LTS"
	case v.Major == 9 && v.Minor == 7:
		return "9.7 LTS"
	case v.Major >= 9:
		return "9.x Innovation"
	default:
		return "8.x Innovation"
	}
}

// ValidateUpgradeCompatibility rejects downgrades, no-op upgrades and unsafe
// direct release jumps. Major release transitions must pass through the latest
// LTS series so the server can perform its supported data-dictionary upgrade.
func ValidateUpgradeCompatibility(current, target string) error {
	from, err := validateSupportedMySQLVersion(current)
	if err != nil {
		return fmt.Errorf("current version: %w", err)
	}
	to, err := validateSupportedMySQLVersion(target)
	if err != nil {
		return fmt.Errorf("target version: %w", err)
	}
	if compareMySQLVersion(to, from) <= 0 {
		return fmt.Errorf("target MySQL %s must be newer than current %s; downgrade and same-version replacement are not supported", target, current)
	}
	if from.Major == 5 {
		if to.Major == 5 {
			return nil
		}
		bridge, _ := parseMySQLVersion(MySQL57UpgradeBridgeVersion)
		if from.Minor != 7 || compareMySQLVersion(from, bridge) < 0 {
			return fmt.Errorf("MySQL %s 不能直接升级到 %s：请先升级到 MySQL %s，预检通过后再分阶段升级到 MySQL 8.0", current, target, MySQL57UpgradeBridgeVersion)
		}
		if to.Major != 8 || to.Minor != 0 {
			return fmt.Errorf("MySQL %s 只能先升级到 MySQL 8.0；不能直接跨到 %s", current, target)
		}
		return nil
	}
	if from.Major == 8 && from.Minor < 4 && to.Major > 8 {
		return errors.New("upgrade the current MySQL 8.x release to the latest 8.4 LTS before entering MySQL 9.x")
	}
	return nil
}

// SupportsPerconaToolkit reflects the versions handled by the current
// automatic PT installer. MySQL itself remains installable when this is false.
func SupportsPerconaToolkit(raw string) bool {
	capabilities, err := CapabilitiesForVersion(raw)
	return err == nil && capabilities.SupportsPerconaToolkit
}

// RuntimeParameterGroupsForVersion returns only parameters whose startup
// option exists in the selected server release. These boundaries follow the
// MySQL added/deprecated/removed variable tables.
func RuntimeParameterGroupsForVersion(raw string) ([]RuntimeParameterGroup, error) {
	v, err := validateSupportedMySQLVersion(raw)
	if err != nil {
		return nil, err
	}
	groups := make([]RuntimeParameterGroup, 0, 2)
	if v.Major == 5 {
		groups = append(groups, RuntimeParameterGroup{Name: "MySQL 5.7 兼容参数", Fields: []RuntimeParameterField{
			{Key: "default_authentication_plugin", Label: "默认认证插件", Options: []string{"mysql_native_password"}, Description: "MySQL 5.7 使用 mysql_native_password 认证。"},
		}})
	}
	if v.Major == 8 && v.Minor < 4 {
		groups = append(groups, RuntimeParameterGroup{Name: "MySQL 8.0–8.3 兼容参数", Fields: []RuntimeParameterField{
			{Key: "default_authentication_plugin", Label: "默认认证插件", Options: []string{"caching_sha2_password", "mysql_native_password"}, Description: "MySQL 8.0–8.3 提供；该变量在 8.4 已移除。新部署建议 caching_sha2_password。"},
			{Key: "binlog_transaction_dependency_tracking", Label: "并行复制依赖跟踪", Options: []string{"COMMIT_ORDER", "WRITESET", "WRITESET_SESSION"}, Description: "MySQL 8.0–8.3 提供；该变量在 8.4 已移除。"},
		}})
		if v.Minor < 3 {
			groups[len(groups)-1].Fields = append(groups[len(groups)-1].Fields, RuntimeParameterField{Key: "transaction_write_set_extraction", Label: "事务写集提取", Options: []string{"XXHASH64", "OFF"}, Description: "MySQL 8.0–8.2 提供；该变量从 8.3 起已移除。"})
		}
	}
	if v.Major == 8 && v.Minor == 4 {
		groups = append(groups, RuntimeParameterGroup{Name: "MySQL 8.4+ 外键兼容", Fields: []RuntimeParameterField{
			{Key: "restrict_fk_on_non_standard_key", Label: "限制非标准外键", Options: []string{"ON", "OFF"}, Description: "8.4 起提供，默认 ON；OFF 仅用于迁移引用非唯一键的旧表结构。"},
		}})
		groups = append(groups, RuntimeParameterGroup{Name: "MySQL 8.4 认证兼容", Fields: []RuntimeParameterField{
			{Key: "mysql_native_password", Label: "启用旧版认证插件", Options: []string{"OFF", "ON"}, Description: "仅 8.4–8.x；mysql_native_password 在 MySQL 9.0 已移除。"},
		}})
	}
	if v.Major >= 9 {
		groups = append(groups, RuntimeParameterGroup{Name: "MySQL 9.x 外键兼容", Fields: []RuntimeParameterField{
			{Key: "restrict_fk_on_non_standard_key", Label: "限制非标准外键", Options: []string{"ON", "OFF"}, Description: "默认 ON；OFF 为迁移旧式非唯一外键提供临时兼容。"},
		}})
	}
	return groups, nil
}

func versionSpecificFields(raw string) (map[string]RuntimeParameterField, error) {
	groups, err := RuntimeParameterGroupsForVersion(raw)
	if err != nil {
		return nil, err
	}
	fields := make(map[string]RuntimeParameterField)
	for _, group := range groups {
		for _, field := range group.Fields {
			fields[field.Key] = field
		}
	}
	return fields, nil
}

// ApplyRuntimeParametersForVersion validates both universal and release-only
// overrides. Removed options are rejected before a task can be dispatched.
func ApplyRuntimeParametersForVersion(vars *ConfigVars, rawVersion string, parameters map[string]string) error {
	v, err := validateSupportedMySQLVersion(rawVersion)
	if err != nil {
		return err
	}
	fields, err := versionSpecificFields(rawVersion)
	if err != nil {
		return err
	}
	universal := make(map[string]string)
	vars.VersionSpecificOptions = nil
	for rawName, rawValue := range parameters {
		name := strings.ToLower(strings.TrimSpace(rawName))
		value := strings.TrimSpace(rawValue)
		if value == "" {
			continue
		}
		field, versionSpecific := fields[name]
		if !versionSpecific {
			universal[name] = value
			continue
		}
		if strings.ContainsAny(value, "\r\n\x00") || len(value) > 256 {
			return fmt.Errorf("invalid mysql runtime parameter %s", name)
		}
		allowed := false
		for _, option := range field.Options {
			if strings.EqualFold(value, option) {
				value = option
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("invalid mysql runtime parameter %s=%s", name, value)
		}
		vars.VersionSpecificOptions = append(vars.VersionSpecificOptions, MySQLConfigOption{Name: name, Value: value})
	}
	sort.Slice(vars.VersionSpecificOptions, func(i, j int) bool {
		return vars.VersionSpecificOptions[i].Name < vars.VersionSpecificOptions[j].Name
	})
	if err := ApplyRuntimeParameters(vars, universal); err != nil {
		return err
	}
	applyConfigCompatibility(vars, v)
	return nil
}

// applyConfigCompatibility converts the platform's canonical MySQL 8-style
// settings into equivalent MySQL 5.7 startup options before rendering.
func applyConfigCompatibility(vars *ConfigVars, v mysqlVersion) {
	vars.Legacy57 = v.Major == 5 && v.Minor == 7
	vars.LegacyReplicationNames = compareMySQLVersion(v, mysqlVersion{Major: 8, Minor: 0, Patch: 26}) < 0
	vars.LegacyRedoLog = compareMySQLVersion(v, mysqlVersion{Major: 8, Minor: 0, Patch: 30}) < 0
	if strings.EqualFold(vars.CollationServer, "utf8mb4_0900_ai_ci") {
		if vars.Legacy57 {
			vars.CollationServer = "utf8mb4_unicode_ci"
		}
	}
	if vars.Legacy57 {
		vars.BinlogExpireDays = vars.BinlogExpireSeconds / 86400
		if vars.BinlogExpireSeconds%86400 != 0 {
			vars.BinlogExpireDays++
		}
		if vars.BinlogExpireDays < 1 && vars.BinlogExpireSeconds > 0 {
			vars.BinlogExpireDays = 1
		}
	}
	if !vars.LegacyRedoLog {
		return
	}
	files := vars.InnodbLogFilesInGroup
	if files <= 0 {
		files = 2
		vars.InnodbLogFilesInGroup = files
	}
	total := parseMySQLSizeOrZero(vars.RedoLogCapacity)
	if total <= 0 {
		total = vars.RedoLogCapacityBytes
	}
	if total > 0 {
		vars.InnodbLogFileSize = bytesToMySQLSize(total / int64(files))
	}
	if vars.InnodbLogFileSize == "" {
		vars.InnodbLogFileSize = "256M"
	}
}

func parseMySQLSizeOrZero(raw string) int64 {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if raw == "" {
		return 0
	}
	multiplier := int64(1)
	switch raw[len(raw)-1] {
	case 'K':
		multiplier = 1024
	case 'M':
		multiplier = 1024 * 1024
	case 'G':
		multiplier = 1024 * 1024 * 1024
	case 'T':
		multiplier = 1024 * 1024 * 1024 * 1024
	default:
		value, _ := strconv.ParseInt(raw, 10, 64)
		return value
	}
	value, _ := strconv.ParseInt(raw[:len(raw)-1], 10, 64)
	return value * multiplier
}
