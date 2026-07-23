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
		{version: "8.2.0", want: []string{"default_authentication_plugin", "binlog_transaction_dependency_tracking", "transaction_write_set_extraction"}, reject: []string{"mysql_native_password", "restrict_fk_on_non_standard_key"}},
		{version: "8.3.0", want: []string{"default_authentication_plugin", "binlog_transaction_dependency_tracking"}, reject: []string{"transaction_write_set_extraction", "mysql_native_password", "restrict_fk_on_non_standard_key"}},
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
	if _, err := validateSupportedMySQLVersion("5.7.8"); err == nil {
		t.Fatal("5.7.8 should be rejected")
	}
	if _, err := validateSupportedMySQLVersion("5.7.45"); err == nil {
		t.Fatal("nonexistent 5.7.45 should be rejected")
	}
	if _, err := validateSupportedMySQLVersion("8.0.10"); err == nil {
		t.Fatal("8.0.10 should be rejected")
	}
	for _, version := range []string{"5.7.9", "5.7.44", "8.0.11", "8.0.46", "8.1.0", "8.4.10", "9.0.0", "9.7.1"} {
		if _, err := validateSupportedMySQLVersion(version); err != nil {
			t.Fatalf("%s should be accepted: %v", version, err)
		}
	}
	if _, err := validateSupportedMySQLVersion("10.0.0"); err == nil {
		t.Fatal("unverified future major versions should be rejected")
	}
	for _, version := range []string{"8.5.0", "9.8.0"} {
		if _, err := validateSupportedMySQLVersion(version); err == nil {
			t.Fatalf("unreleased series %s should be rejected", version)
		}
	}
}

func TestCapabilitiesFollowPatchLevelBoundaries(t *testing.T) {
	tests := []struct {
		version                                 string
		legacyTx, legacyReplication, legacyRedo bool
		clone, dynamic                          bool
	}{
		{version: "5.7.9", legacyTx: true, legacyReplication: true, legacyRedo: true},
		{version: "5.7.44", legacyReplication: true, legacyRedo: true},
		{version: "8.0.11", legacyReplication: true, legacyRedo: true},
		{version: "8.0.17", legacyReplication: true, legacyRedo: true, clone: true, dynamic: true},
		{version: "8.0.26", legacyRedo: true, clone: true, dynamic: true},
		{version: "8.0.30", clone: true, dynamic: true},
		{version: "8.4.10", clone: true, dynamic: true},
		{version: "9.7.1", clone: true, dynamic: true},
	}
	for _, tt := range tests {
		capabilities, err := CapabilitiesForVersion(tt.version)
		if err != nil {
			t.Fatal(err)
		}
		if capabilities.LegacyTransactionVariable != tt.legacyTx || capabilities.LegacyReplicationNames != tt.legacyReplication || capabilities.LegacyRedoLog != tt.legacyRedo || capabilities.SupportsClone != tt.clone || capabilities.SupportsDynamicPrivileges != tt.dynamic {
			t.Fatalf("unexpected capabilities for %s: %+v", tt.version, capabilities)
		}
	}
	if !SupportsPerconaToolkit("9.7.1") {
		t.Fatal("MySQL 9.7 should use the current Percona Toolkit compatibility path")
	}
}

func TestApplyRuntimeParametersForMySQL57UsesLegacyConfigSemantics(t *testing.T) {
	vars := ConfigVars{
		CollationServer:       "utf8mb4_0900_ai_ci",
		BinlogExpireSeconds:   604801,
		RedoLogCapacity:       "4G",
		RedoLogCapacityBytes:  4 * 1024 * 1024 * 1024,
		InnodbLogFilesInGroup: 2,
	}
	if err := ApplyRuntimeParametersForVersion(&vars, "5.7.44", map[string]string{"innodb_redo_log_capacity": "6G"}); err != nil {
		t.Fatal(err)
	}
	if !vars.Legacy57 || vars.CollationServer != "utf8mb4_unicode_ci" || vars.BinlogExpireDays != 8 || vars.InnodbLogFileSize != "3G" {
		t.Fatalf("unexpected MySQL 5.7 compatibility values: %+v", vars)
	}
	if !SupportsPerconaToolkit("5.7.44") {
		t.Fatal("MySQL 5.7 should support automatic Percona Toolkit installation")
	}
}

func TestValidateUpgradeCompatibility(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		valid    bool
	}{
		{from: "5.7.35", to: "5.7.44", valid: true},
		{from: "5.7.35", to: "8.0.44", valid: false},
		{from: "5.7.43", to: "8.0.44", valid: false},
		{from: "5.7.44", to: "8.0.44", valid: true},
		{from: "5.7.44", to: "8.4.6", valid: false},
		{from: "8.0.44", to: "8.4.6", valid: true},
		{from: "8.4.6", to: "9.7.0", valid: true},
		{from: "8.0.44", to: "8.0.45", valid: true},
		{from: "8.0.44", to: "8.0.44", valid: false},
		{from: "8.4.6", to: "8.0.44", valid: false},
		{from: "8.0.44", to: "9.7.0", valid: false},
		{from: "8.3.0", to: "8.4.10", valid: true},
		{from: "8.3.0", to: "9.0.0", valid: false},
		{from: "8.4.10", to: "9.0.0", valid: true},
		{from: "9.0.0", to: "9.7.1", valid: true},
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

func TestValidateUpgradeCompatibilityExplainsMySQL57Bridge(t *testing.T) {
	err := ValidateUpgradeCompatibility("5.7.35", "8.0.44")
	if err == nil {
		t.Fatal("direct MySQL 5.7.35 -> 8.0.44 upgrade should be rejected")
	}
	for _, expected := range []string{"5.7.35", MySQL57UpgradeBridgeVersion, "8.0"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("expected actionable bridge error containing %q, got %v", expected, err)
		}
	}
}
