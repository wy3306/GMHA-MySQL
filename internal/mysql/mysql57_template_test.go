package mysql

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"text/template"
)

func TestMySQL57TemplateExcludesMySQL8OnlyOptions(t *testing.T) {
	source, err := os.ReadFile("../../configs/templates/mysql/my.cnf.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := template.New("my.cnf").Parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	vars := ConfigVars{Legacy57: true, LegacyReplicationNames: true, LegacyRedoLog: true, BinlogExpireDays: 7, InnodbLogFileSize: "2G", InnodbLogFilesInGroup: 2, TransactionIsolation: "READ-COMMITTED"}
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, vars); err != nil {
		t.Fatal(err)
	}
	config := rendered.String()
	for _, expected := range []string{"transaction_isolation = READ-COMMITTED", "expire_logs_days = 7", "log_slave_updates", "log_slow_slave_statements", "innodb_log_file_size = 2G", "innodb_log_files_in_group = 2"} {
		if !strings.Contains(config, expected) {
			t.Fatalf("MySQL 5.7 config missing %q:\n%s", expected, config)
		}
	}
	for _, forbidden := range []string{"binlog_expire_logs_seconds", "log_replica_updates", "log_slow_replica_statements", "innodb_redo_log_capacity"} {
		if strings.Contains(config, forbidden) {
			t.Fatalf("MySQL 5.7 config contains MySQL 8 option %q:\n%s", forbidden, config)
		}
	}
}

func TestMySQLTemplateUsesPatchLevelOptionNames(t *testing.T) {
	source, err := os.ReadFile("../../configs/templates/mysql/my.cnf.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	tpl, err := template.New("my.cnf").Parse(string(source))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		version string
		want    []string
		reject  []string
	}{
		{version: "5.7.9", want: []string{"transaction_isolation = READ-COMMITTED", "log_slave_updates", "innodb_log_file_size"}, reject: []string{"tx_isolation =", "log_replica_updates", "innodb_redo_log_capacity"}},
		{version: "5.7.44", want: []string{"transaction_isolation = READ-COMMITTED", "log_slave_updates", "innodb_log_file_size"}, reject: []string{"tx_isolation =", "log_replica_updates", "innodb_redo_log_capacity"}},
		{version: "8.0.11", want: []string{"transaction_isolation = READ-COMMITTED", "log_slave_updates", "innodb_log_file_size"}, reject: []string{"tx_isolation =", "log_replica_updates", "innodb_redo_log_capacity"}},
		{version: "8.0.26", want: []string{"transaction_isolation = READ-COMMITTED", "log_replica_updates", "innodb_log_file_size"}, reject: []string{"tx_isolation =", "log_slave_updates", "innodb_redo_log_capacity"}},
		{version: "8.0.30", want: []string{"transaction_isolation = READ-COMMITTED", "log_replica_updates", "innodb_redo_log_capacity"}, reject: []string{"tx_isolation =", "log_slave_updates", "innodb_log_file_size"}},
		{version: "8.4.10", want: []string{"transaction_isolation = READ-COMMITTED", "log_replica_updates", "innodb_redo_log_capacity"}, reject: []string{"tx_isolation =", "log_slave_updates", "innodb_log_file_size"}},
		{version: "9.7.1", want: []string{"transaction_isolation = READ-COMMITTED", "log_replica_updates", "innodb_redo_log_capacity"}, reject: []string{"tx_isolation =", "log_slave_updates", "innodb_log_file_size"}},
	}
	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			vars := ConfigVars{TransactionIsolation: "READ-COMMITTED", BinlogExpireSeconds: 604800, RedoLogCapacity: "4G", RedoLogCapacityBytes: 4 * 1024 * 1024 * 1024, InnodbLogFilesInGroup: 2}
			if err := ApplyRuntimeParametersForVersion(&vars, tt.version, nil); err != nil {
				t.Fatal(err)
			}
			var rendered bytes.Buffer
			if err := tpl.Execute(&rendered, vars); err != nil {
				t.Fatal(err)
			}
			config := rendered.String()
			for _, wanted := range tt.want {
				if !strings.Contains(config, wanted) {
					t.Errorf("MySQL %s config missing %q", tt.version, wanted)
				}
			}
			for _, rejected := range tt.reject {
				if strings.Contains(config, rejected) {
					t.Errorf("MySQL %s config unexpectedly contains %q", tt.version, rejected)
				}
			}
		})
	}
}
