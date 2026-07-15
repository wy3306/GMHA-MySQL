package mysql

import (
	"strings"
	"testing"
)

func TestRuntimeParameterGroupsFollowMySQLReleaseBoundaries(t *testing.T) {
	tests := []struct {
		version string
		want    []string
		reject  []string
	}{
		{version: "8.0.35", want: []string{"default_authentication_plugin", "binlog_transaction_dependency_tracking", "transaction_write_set_extraction"}, reject: []string{"mysql_native_password", "restrict_fk_on_non_standard_key"}},
		{version: "8.4.0", want: []string{"mysql_native_password", "restrict_fk_on_non_standard_key"}, reject: []string{"default_authentication_plugin"}},
		{version: "8.9.0", want: []string{"mysql_native_password", "restrict_fk_on_non_standard_key"}, reject: []string{"default_authentication_plugin"}},
		{version: "9.0.1", want: []string{"restrict_fk_on_non_standard_key"}, reject: []string{"mysql_native_password", "default_authentication_plugin"}},
		{version: "9.7.1", want: []string{"restrict_fk_on_non_standard_key"}, reject: []string{"mysql_native_password"}},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			fields, err := versionSpecificFields(tt.version)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range tt.want {
				if _, ok := fields[name]; !ok {
					t.Errorf("expected %s for MySQL %s", name, tt.version)
				}
			}
			for _, name := range tt.reject {
				if _, ok := fields[name]; ok {
					t.Errorf("did not expect %s for MySQL %s", name, tt.version)
				}
			}
		})
	}
}

func TestApplyRuntimeParametersForVersionRejectsRemovedOptions(t *testing.T) {
	vars := ConfigVars{}
	if err := ApplyRuntimeParametersForVersion(&vars, "8.4.0", map[string]string{"mysql_native_password": "ON"}); err != nil {
		t.Fatalf("8.4 option should be accepted: %v", err)
	}
	if len(vars.VersionSpecificOptions) != 1 || vars.VersionSpecificOptions[0].Name != "mysql_native_password" {
		t.Fatalf("unexpected options: %#v", vars.VersionSpecificOptions)
	}
	for _, version := range []string{"9.0.1", "9.7.1"} {
		err := ApplyRuntimeParametersForVersion(&ConfigVars{}, version, map[string]string{"mysql_native_password": "ON"})
		if err == nil || !strings.Contains(err.Error(), "mysql_native_password") {
			t.Fatalf("MySQL %s should reject removed mysql_native_password, got %v", version, err)
		}
	}
}

func TestMinimumSupportedMySQLVersion(t *testing.T) {
	if _, err := validateSupportedMySQLVersion("8.0.34"); err == nil {
		t.Fatal("8.0.34 should be rejected")
	}
	for _, version := range []string{"8.0.35", "8.9.0", "9.0.0", "9.7.1"} {
		if _, err := validateSupportedMySQLVersion(version); err != nil {
			t.Fatalf("%s should be accepted: %v", version, err)
		}
	}
	if _, err := validateSupportedMySQLVersion("10.0.0"); err == nil {
		t.Fatal("unverified future major versions should be rejected")
	}
}

func TestValidateUpgradeCompatibility(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		valid    bool
	}{
		{from: "8.0.44", to: "8.4.6", valid: true},
		{from: "8.4.6", to: "9.7.0", valid: true},
		{from: "8.0.44", to: "8.0.45", valid: true},
		{from: "8.0.44", to: "8.0.44", valid: false},
		{from: "8.4.6", to: "8.0.44", valid: false},
		{from: "8.0.44", to: "9.7.0", valid: false},
	} {
		err := ValidateUpgradeCompatibility(tc.from, tc.to)
		if tc.valid && err != nil {
			t.Fatalf("%s -> %s should be valid: %v", tc.from, tc.to, err)
		}
		if !tc.valid && err == nil {
			t.Fatalf("%s -> %s should be rejected", tc.from, tc.to)
		}
	}
}
