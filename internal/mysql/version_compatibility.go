package mysql

import (
	"errors"
	"fmt"
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
	minimum := mysqlVersion{Major: 8, Minor: 0, Patch: 35}
	if compareMySQLVersion(v, minimum) < 0 {
		return mysqlVersion{}, fmt.Errorf("mysql %s is unsupported; minimum supported version is 8.0.35", raw)
	}
	if v.Major > 9 {
		return mysqlVersion{}, fmt.Errorf("mysql %s is not yet verified; supported major versions are 8 and 9", raw)
	}
	return v, nil
}

func mysqlReleaseTrack(v mysqlVersion) string {
	switch {
	case v.Major == 8 && v.Minor == 0:
		return "8.0 LTS"
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
	if from.Major == 8 && from.Minor == 0 && to.Major > 8 {
		return errors.New("upgrade MySQL 8.0 to the latest 8.4 LTS before entering MySQL 9.x")
	}
	if to.Major-from.Major > 1 {
		return fmt.Errorf("direct upgrade from MySQL %s to %s skips a major release", current, target)
	}
	if from.Major == to.Major && to.Minor-from.Minor > 4 {
		return fmt.Errorf("direct upgrade from MySQL %s to %s skips a supported LTS transition", current, target)
	}
	return nil
}

// SupportsPerconaToolkit reflects the versions handled by the current
// automatic PT installer. MySQL itself remains installable when this is false.
func SupportsPerconaToolkit(raw string) bool {
	v, err := validateSupportedMySQLVersion(raw)
	return err == nil && v.Major == 8
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
	if v.Major == 8 && v.Minor == 0 {
		groups = append(groups, RuntimeParameterGroup{Name: "MySQL 8.0 兼容参数", Fields: []RuntimeParameterField{
			{Key: "default_authentication_plugin", Label: "默认认证插件", Options: []string{"caching_sha2_password", "mysql_native_password"}, Description: "仅 MySQL 8.0；该变量在 8.4 已移除。新部署建议 caching_sha2_password。"},
			{Key: "binlog_transaction_dependency_tracking", Label: "并行复制依赖跟踪", Options: []string{"COMMIT_ORDER", "WRITESET", "WRITESET_SESSION"}, Description: "仅 MySQL 8.0；该变量在 8.4 已移除。"},
			{Key: "transaction_write_set_extraction", Label: "事务写集提取", Options: []string{"XXHASH64", "OFF"}, Description: "仅 MySQL 8.0；该变量从 8.3 起已移除。"},
		}})
	}
	if v.Major == 8 && v.Minor >= 4 {
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
	return ApplyRuntimeParameters(vars, universal)
}
