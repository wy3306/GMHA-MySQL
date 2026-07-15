package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentcore "gmha/internal/agent/core"
	taskdomain "gmha/internal/domain/task"
)

// MySQLTopologyHandler 是 MySQL 拓扑配置任务处理器，负责配置 MySQL 复制拓扑关系。
type MySQLTopologyHandler struct{}

// NewMySQLTopologyHandler 创建一个新的 MySQL 拓扑配置任务处理器实例。
func NewMySQLTopologyHandler() *MySQLTopologyHandler {
	return &MySQLTopologyHandler{}
}

// Type 返回该处理器处理的任务类型。
func (h *MySQLTopologyHandler) Type() string {
	return string(taskdomain.TypeMySQLTopology)
}

// Handle 执行 MySQL 拓扑配置任务，包括检查实例、更新配置、重启、创建账号、Clone 同步、配置复制和验证。
func (h *MySQLTopologyHandler) Handle(ctx context.Context, task taskdomain.DispatchTask, reporter *agentcore.Reporter) error {
	var spec taskdomain.MySQLTopologySpec
	if err := json.Unmarshal(task.Spec, &spec); err != nil {
		return err
	}
	runner := &mysqlInstallRunner{
		ctx:        ctx,
		task:       task,
		reporter:   reporter,
		spec:       mysqlInstallSpecFromTopology(spec),
		stepStarts: make(map[string]time.Time),
		runner:     agentcore.NewCommandRunner(),
		topology:   &spec,
	}
	return runner.runTopology(spec)
}

func mysqlInstallSpecFromTopology(spec taskdomain.MySQLTopologySpec) taskdomain.MySQLInstallSpec {
	node := spec.Node
	return taskdomain.MySQLInstallSpec{
		Port:            spec.Port,
		ServerID:        node.ServerID,
		BaseDir:         node.BaseDir,
		DataDir:         node.DataDir,
		InstanceDir:     node.InstanceDir,
		RootPassword:    spec.RootPassword,
		MyCnfPath:       node.MyCnfPath,
		SocketPath:      node.SocketPath,
		SystemdUnitName: node.SystemdUnitName,
	}
}

func (r *mysqlInstallRunner) runTopology(spec taskdomain.MySQLTopologySpec) error {
	steps := []func(taskdomain.DispatchStep) error{
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "检查 MySQL 实例", topologyCheckMySQLCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "更新 my.cnf 复制参数", topologyConfigureMyCNFCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "重启 MySQL 并刷新 server_uuid", topologyRestartCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "创建复制和 Clone 账号", topologyPrepareAccountsCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "按需执行 Clone 同步", topologyCloneCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "配置并启动复制链路", topologyReplicationCommand(spec))
		},
		func(step taskdomain.DispatchStep) error {
			return r.runShellStep(step, "验证复制状态", topologyVerifyCommand(spec))
		},
	}
	if len(r.task.Steps) != len(steps) {
		return fmt.Errorf("mysql_topology step mismatch: task has %d steps, handler expects %d; recreate task with current manager", len(r.task.Steps), len(steps))
	}
	for i, fn := range steps {
		if err := fn(r.task.Steps[i]); err != nil {
			return err
		}
	}
	return nil
}

func topologyCheckMySQLCommand(spec taskdomain.MySQLTopologySpec) string {
	node := spec.Node
	mysqladmin := node.BaseDir + "/bin/mysqladmin"
	mysql := node.BaseDir + "/bin/mysql"
	return strings.Join([]string{
		`test "$(id -u)" = "0" || { echo 'mysql topology task must run as root'; exit 1; }`,
		fmt.Sprintf(`test -x %s`, shellEscape(mysql)),
		fmt.Sprintf(`test -f %s`, shellEscape(node.MyCnfPath)),
		fmt.Sprintf(`%s --connect-timeout=5 --socket=%s -uroot -p%s ping`, shellEscape(mysqladmin), shellEscape(node.SocketPath), shellEscape(spec.RootPassword)),
		fmt.Sprintf(`%s --socket=%s -uroot -p%s -e %s`, shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape("SELECT @@server_uuid, @@server_id")),
	}, "; ")
}

func topologyConfigureMyCNFCommand(spec taskdomain.MySQLTopologySpec) string {
	node := spec.Node
	if node.SystemdUnitName == "" {
		node.SystemdUnitName = "mysqld"
	}
	readOnly := "OFF"
	superReadOnly := "OFF"
	if node.ReadOnly {
		readOnly = "ON"
	}
	if node.SuperReadOnly {
		superReadOnly = "ON"
	}
	lines := []string{
		fmt.Sprintf("server_id=%d", node.ServerID),
		"binlog_format=ROW",
		"gtid_mode=ON",
		"enforce_gtid_consistency=ON",
		"log_replica_updates=ON",
		"skip_replica_start=ON",
		fmt.Sprintf("auto_increment_offset=%d", node.AutoIncrementOffset),
		fmt.Sprintf("auto_increment_increment=%d", node.AutoIncrementIncrement),
		fmt.Sprintf("replica_parallel_type=%s", spec.ParallelType),
		fmt.Sprintf("replica_parallel_workers=%d", spec.ParallelWorkers),
		fmt.Sprintf("read_only=%s", readOnly),
		fmt.Sprintf("super_read_only=%s", superReadOnly),
	}
	configLines := make([]string, 0, len(lines))
	for _, line := range lines {
		configLines = append(configLines, line)
	}
	mysqlUser := strings.TrimSpace(node.MySQLUser)
	if mysqlUser == "" {
		mysqlUser = "mysql"
	}
	managedBlock := "# gmha topology managed\n" + strings.Join(configLines, "\n") + "\n"
	managedKeys := "server_id|binlog_format|gtid_mode|enforce_gtid_consistency|log_replica_updates|skip_replica_start|skip_slave_start|auto_increment_offset|auto_increment_increment|replica_parallel_type|replica_parallel_workers|slave_parallel_type|slave_parallel_workers|read_only|super_read_only"
	return fmt.Sprintf(`
set -eu
cnf=%s
managed_keys=%s
[ -n "$cnf" ] || { echo 'my_cnf_path is empty'; exit 1; }
[ -f "$cnf" ] || { echo "my.cnf not found: $cnf"; exit 1; }
backup="${cnf}.gmha.$(date +%%Y%%m%%d%%H%%M%%S).bak"
cp -a "$cnf" "$backup"
awk -v keys="^(${managed_keys})[[:space:]]*=" '
  BEGIN { in_managed=0 }
  /^# gmha topology managed/ { in_managed=1; next }
  in_managed && /^[[:space:]]*$/ { in_managed=0; next }
  in_managed { next }
  $0 ~ "^[[:space:]]*" keys { next }
  { print }
' "$cnf" > "${cnf}.tmp"
mv "${cnf}.tmp" "$cnf"
grep -Eq '^[[:space:]]*\[mysqld\][[:space:]]*$' "$cnf" || printf '\n[mysqld]\n' >> "$cnf"
printf '\n%%s' %s >> "$cnf"
%s --defaults-file="$cnf" --validate-config --user=%s
echo "my.cnf updated, backup=$backup"
`, shellEscape(node.MyCnfPath), shellEscape(managedKeys), shellEscape(managedBlock), shellEscape(node.BaseDir+"/bin/mysqld"), shellEscape(mysqlUser))
}

func topologyRestartCommand(spec taskdomain.MySQLTopologySpec) string {
	node := spec.Node
	unit := node.SystemdUnitName
	if unit == "" {
		unit = "mysqld"
	}
	removeAutoCNF := "true"
	if node.ResetServerUUID {
		removeAutoCNF = fmt.Sprintf(`if [ -f %s ]; then cp -a %s %s.gmha.$(date +%%Y%%m%%d%%H%%M%%S).bak; rm -f %s; fi`,
			shellEscape(node.DataDir+"/auto.cnf"),
			shellEscape(node.DataDir+"/auto.cnf"),
			shellEscape(node.DataDir+"/auto.cnf"),
			shellEscape(node.DataDir+"/auto.cnf"),
		)
	}
	mysqladmin := node.BaseDir + "/bin/mysqladmin"
	return strings.Join([]string{
		fmt.Sprintf("systemctl stop %s", shellEscape(unit)),
		removeAutoCNF,
		fmt.Sprintf("systemctl start %s", shellEscape(unit)),
		fmt.Sprintf("for i in $(seq 1 60); do %s --connect-timeout=2 --socket=%s -uroot -p%s ping >/tmp/gmha-topology-ready.out 2>&1 && cat /tmp/gmha-topology-ready.out && exit 0; sleep 1; done; cat /tmp/gmha-topology-ready.out 2>/dev/null; exit 1", shellEscape(mysqladmin), shellEscape(node.SocketPath), shellEscape(spec.RootPassword)),
	}, "; ")
}

func topologyPrepareAccountsCommand(spec taskdomain.MySQLTopologySpec) string {
	node := spec.Node
	needsDonor := node.Role == "M" || hasDownstream(spec, node.MachineID)
	if !needsDonor {
		return "echo 'replication account preparation skipped on pure replica'"
	}
	mysql := node.BaseDir + "/bin/mysql"
	sql := fmt.Sprintf(
		"SET SQL_LOG_BIN=0; CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; ALTER USER '%s'@'%%' IDENTIFIED BY '%s'; GRANT REPLICATION SLAVE, REPLICATION CLIENT ON *.* TO '%s'@'%%'; CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; ALTER USER '%s'@'%%' IDENTIFIED BY '%s'; GRANT BACKUP_ADMIN, CLONE_ADMIN ON *.* TO '%s'@'%%'; FLUSH PRIVILEGES; SET SQL_LOG_BIN=1;",
		mysqlSQLEscape(spec.ReplicationUser),
		mysqlSQLEscape(spec.ReplicationPassword),
		mysqlSQLEscape(spec.ReplicationUser),
		mysqlSQLEscape(spec.ReplicationPassword),
		mysqlSQLEscape(spec.ReplicationUser),
		mysqlSQLEscape(spec.CloneUser),
		mysqlSQLEscape(spec.ClonePassword),
		mysqlSQLEscape(spec.CloneUser),
		mysqlSQLEscape(spec.ClonePassword),
		mysqlSQLEscape(spec.CloneUser),
	)
	pluginSQL := "INSTALL PLUGIN clone SONAME 'mysql_clone.so';"
	return fmt.Sprintf("%s --socket=%s -uroot -p%s -e %s; %s --socket=%s -uroot -p%s -e %s || true",
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(sql),
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(pluginSQL),
	)
}

func topologyCloneCommand(spec taskdomain.MySQLTopologySpec) string {
	node := spec.Node
	if !spec.UseClone || !node.RequiresClone || strings.TrimSpace(node.SourceIP) == "" {
		return "echo 'clone skipped'"
	}
	mysql := node.BaseDir + "/bin/mysql"
	mysqladmin := node.BaseDir + "/bin/mysqladmin"
	unit := node.SystemdUnitName
	if unit == "" {
		unit = "mysqld"
	}
	pluginSQL := "INSTALL PLUGIN clone SONAME 'mysql_clone.so';"
	cleanupSQL := "STOP REPLICA; RESET REPLICA ALL;"
	cloneSQL := fmt.Sprintf(
		"SET GLOBAL clone_valid_donor_list='%s:%d'; CLONE INSTANCE FROM '%s'@'%s':%d IDENTIFIED BY '%s';",
		mysqlSQLEscape(node.SourceIP),
		node.SourcePort,
		mysqlSQLEscape(spec.CloneUser),
		mysqlSQLEscape(node.SourceIP),
		node.SourcePort,
		mysqlSQLEscape(spec.ClonePassword),
	)
	return fmt.Sprintf(`%s --socket=%s -uroot -p%s -e %s || true; %s --socket=%s -uroot -p%s -e %s || true; clone_out=$(%s --socket=%s -uroot -p%s -e %s 2>&1); clone_rc=$?; printf '%%s\n' "$clone_out"; if [ "$clone_rc" -ne 0 ] && ! printf '%%s\n' "$clone_out" | grep -q 'ERROR 3707'; then exit "$clone_rc"; fi; systemctl start %s || systemctl restart %s; for i in $(seq 1 90); do %s --connect-timeout=2 --socket=%s -uroot -p%s ping >/tmp/gmha-clone-ready.out 2>&1 && cat /tmp/gmha-clone-ready.out && exit 0; sleep 2; done; cat /tmp/gmha-clone-ready.out 2>/dev/null; exit 1`,
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(pluginSQL),
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(cleanupSQL),
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(cloneSQL),
		shellEscape(unit),
		shellEscape(unit),
		shellEscape(mysqladmin), shellEscape(node.SocketPath), shellEscape(spec.RootPassword),
	)
}

func topologyReplicationCommand(spec taskdomain.MySQLTopologySpec) string {
	node := spec.Node
	mysql := node.BaseDir + "/bin/mysql"
	stopNewSQL := "SET GLOBAL super_read_only=0; STOP REPLICA; RESET REPLICA ALL;"
	stopOldSQL := "SET GLOBAL super_read_only=0; STOP SLAVE; RESET SLAVE ALL;"
	if !node.RequiresReplicationSetup || strings.TrimSpace(node.SourceIP) == "" {
		resetSQL := fmt.Sprintf("SET GLOBAL super_read_only=0; STOP REPLICA; RESET REPLICA ALL; SET GLOBAL read_only=%s; SET GLOBAL super_read_only=%s;",
			mysqlBool(node.ReadOnly),
			mysqlBool(node.SuperReadOnly),
		)
		resetOldSQL := fmt.Sprintf("SET GLOBAL super_read_only=0; STOP SLAVE; RESET SLAVE ALL; SET GLOBAL read_only=%s; SET GLOBAL super_read_only=%s;",
			mysqlBool(node.ReadOnly),
			mysqlBool(node.SuperReadOnly),
		)
		return fmt.Sprintf("%s --socket=%s -uroot -p%s -e %s || true; %s --socket=%s -uroot -p%s -e %s || true; echo 'source-only master replication reset and readonly state applied'",
			shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(resetSQL),
			shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(resetOldSQL),
		)
	}
	newSQL := fmt.Sprintf(
		"CHANGE REPLICATION SOURCE TO SOURCE_HOST='%s', SOURCE_PORT=%d, SOURCE_USER='%s', SOURCE_PASSWORD='%s', SOURCE_AUTO_POSITION=1, SOURCE_CONNECT_RETRY=2, SOURCE_RETRY_COUNT=30, SOURCE_DELAY=%d; START REPLICA; SET GLOBAL read_only=%s; SET GLOBAL super_read_only=%s;",
		mysqlSQLEscape(node.SourceIP),
		node.SourcePort,
		mysqlSQLEscape(spec.ReplicationUser),
		mysqlSQLEscape(spec.ReplicationPassword),
		node.ReplicationDelaySeconds,
		mysqlBool(node.ReadOnly),
		mysqlBool(node.SuperReadOnly),
	)
	oldSQL := fmt.Sprintf(
		"CHANGE MASTER TO MASTER_HOST='%s', MASTER_PORT=%d, MASTER_USER='%s', MASTER_PASSWORD='%s', MASTER_AUTO_POSITION=1, MASTER_CONNECT_RETRY=2, MASTER_RETRY_COUNT=30, MASTER_DELAY=%d; START SLAVE; SET GLOBAL read_only=%s; SET GLOBAL super_read_only=%s;",
		mysqlSQLEscape(node.SourceIP),
		node.SourcePort,
		mysqlSQLEscape(spec.ReplicationUser),
		mysqlSQLEscape(spec.ReplicationPassword),
		node.ReplicationDelaySeconds,
		mysqlBool(node.ReadOnly),
		mysqlBool(node.SuperReadOnly),
	)
	return fmt.Sprintf("%s --socket=%s -uroot -p%s -e %s || true; %s --socket=%s -uroot -p%s -e %s || true; %s --socket=%s -uroot -p%s -e %s || %s --socket=%s -uroot -p%s -e %s",
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(stopNewSQL),
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(stopOldSQL),
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(newSQL),
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape(oldSQL),
	)
}

func topologyVerifyCommand(spec taskdomain.MySQLTopologySpec) string {
	node := spec.Node
	if !node.RequiresReplicationSetup || strings.TrimSpace(node.SourceIP) == "" {
		return "echo 'source-only master verified'"
	}
	mysql := node.BaseDir + "/bin/mysql"
	return fmt.Sprintf(`set -e; for i in $(seq 1 30); do out=$(%s --socket=%s -uroot -p%s -e %s 2>/tmp/gmha-repl-status.err || %s --socket=%s -uroot -p%s -e %s 2>>/tmp/gmha-repl-status.err || true); echo "$out"; echo "$out" | grep -Eq '(Replica_IO_Running|Slave_IO_Running): Yes' && echo "$out" | grep -Eq '(Replica_SQL_Running|Slave_SQL_Running): Yes' && exit 0; sleep 2; done; cat /tmp/gmha-repl-status.err 2>/dev/null || true; exit 1`,
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape("SHOW REPLICA STATUS\\G"),
		shellEscape(mysql), shellEscape(node.SocketPath), shellEscape(spec.RootPassword), shellEscape("SHOW SLAVE STATUS\\G"),
	)
}

func hasDownstream(spec taskdomain.MySQLTopologySpec, machineID string) bool {
	for _, node := range spec.Nodes {
		if node.SourceMachineID == machineID {
			return true
		}
	}
	return false
}

func mysqlBool(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}
