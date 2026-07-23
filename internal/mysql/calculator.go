// calculator.go 实现 MySQL 配置参数的自动计算，根据机器资源和配置档案生成最优的数据库参数。
package mysql

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	collectdomain "gmha/internal/collect"
)

var mysqlSizePattern = regexp.MustCompile(`^[0-9]+(?:[KMGTP])?$`)
var mysqlDecimalPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

// ConfigVars 定义 MySQL 实例的完整配置参数，包括内存分配、连接数、日志、InnoDB 等各项配置。
type ConfigVars struct {
	VersionSpecificOptions []MySQLConfigOption
	Legacy57               bool
	LegacyReplicationNames bool
	LegacyRedoLog          bool
	ServerID               int
	Port                   int
	MySQLUser              string
	InstanceDir            string
	DataDir                string
	BinlogDir              string
	RedoDir                string
	UndoDir                string
	TmpDir                 string
	BaseDir                string
	MyCnfPath              string
	SocketPath             string
	PIDFile                string
	ErrorLog               string
	CharacterSetsDir       string
	PluginDir              string
	OpenFilesLimit         int
	LimitNProc             int
	SysctlSwappiness       int
	CharacterSetServer     string
	CollationServer        string
	Autocommit             int
	TransactionIsolation   string
	InteractiveTimeout     int
	WaitTimeout            int
	LockWaitTimeout        int
	MaxConnectErrors       int
	MaxAllowedPacket       string
	LogTimestamps          string
	SlowQueryLog           int
	SlowQueryLogFile       string
	LongQueryTime          string
	MinExaminedRowLimit    int
	LogSlowAdminStatements int
	LogSlowReplicaStmts    int
	LogThrottleNoIndex     int
	BinlogFormat           string
	SyncBinlog             int
	BinlogExpireSeconds    int
	BinlogExpireDays       int
	BinlogRowsQueryEvents  int
	LogReplicaUpdates      int
	GTIDMode               string
	EnforceGTIDConsistency string
	RelayLogRecovery       int
	ReadOnly               int
	SuperReadOnly          int
	DefaultStorageEngine   string
	InnodbDataFilePath     string
	InnodbTempDataFilePath string
	InnodbFlushLogAtCommit int
	InnodbLockWaitTimeout  int
	InnodbFilePerTable     int
	InnodbFlushMethod      string
	InnodbLogBufferSize    string
	InnodbLogBufferBytes   int64
	InnodbReadIOThreads    int
	InnodbWriteIOThreads   int
	KeyBufferSize          string
	MyISAMSortBufferSize   string
	BufferPoolSize         string
	BufferPoolSizeBytes    int64
	BufferPoolInstances    int
	MaxConnections         int
	RedoLogCapacity        string
	RedoLogCapacityBytes   int64
	InnodbLogFileSize      string
	InnodbLogFilesInGroup  int
	TableOpenCache         int
	ThreadCacheSize        int
	SortBufferSize         string
	SortBufferSizeBytes    int64
	ReadBufferSize         string
	ReadBufferSizeBytes    int64
	ReadRndBufferSize      string
	ReadRndBufferSizeBytes int64
	JoinBufferSize         string
	JoinBufferSizeBytes    int64
	SkipNameResolve        int
	SymbolicLinks          int
}

// Calculator 是 MySQL 配置计算器，根据机器信息和配置档案计算最优的数据库参数。
type Calculator struct{}

// NewCalculator 创建并返回一个新的配置计算器实例。
func NewCalculator() *Calculator {
	return &Calculator{}
}

// ConfigInput 定义配置计算所需的输入参数，包括端口、目录路径等用户可自定义的配置项。
type ConfigInput struct {
	Port             int
	ServerID         int
	MySQLUser        string
	InstanceDir      string
	DataDir          string
	BinlogDir        string
	RedoDir          string
	UndoDir          string
	TmpDir           string
	BaseDir          string
	MyCnfPath        string
	SocketPath       string
	PIDFile          string
	ErrorLog         string
	CharacterSetsDir string
	PluginDir        string
}

// Calculate 根据机器信息、配置档案和输入参数计算完整的 MySQL 配置，包括内存分配、连接数、日志等参数。
func (c *Calculator) Calculate(info collectdomain.MachineInfo, profile Profile, input ConfigInput) (ConfigVars, error) {
	if info.MemoryGB <= 0 {
		return ConfigVars{}, errors.New("machine memory_gb is required")
	}
	if input.Port <= 0 {
		return ConfigVars{}, errors.New("mysql port is required")
	}
	if profile.InnodbDataFileInitialMB <= 0 {
		profile.InnodbDataFileInitialMB = 128
	}
	if profile.InnodbTempFileInitialMB <= 0 {
		profile.InnodbTempFileInitialMB = 128
	}
	input = NormalizeConfigInput(input)
	if strings.TrimSpace(input.DataDir) == "" || strings.TrimSpace(input.BaseDir) == "" {
		return ConfigVars{}, errors.New("data dir and base dir are required")
	}

	memBytes := int64(info.MemoryGB) * gb
	bufferPoolBytes := int64(float64(memBytes) * profile.BufferPoolRatio)
	if bufferPoolBytes < 512*mb {
		bufferPoolBytes = 512 * mb
	}
	if bufferPoolBytes >= 2*gb {
		bufferPoolBytes = (bufferPoolBytes / gb) * gb
	}

	bufferPoolInstances := 1
	switch {
	case bufferPoolBytes < gb:
		bufferPoolInstances = 1
	case bufferPoolBytes <= 8*gb:
		bufferPoolInstances = 4
	default:
		bufferPoolInstances = 8
	}

	sortBuffer := int64(profile.SortBufferSizeMB) * mb
	readBuffer := int64(profile.ReadBufferSizeMB) * mb
	readRndBuffer := int64(profile.ReadRndBufferMB) * mb
	joinBuffer := int64(profile.JoinBufferSizeMB) * mb
	perConnMem := sortBuffer + readBuffer + readRndBuffer + joinBuffer
	if perConnMem <= 0 {
		return ConfigVars{}, errors.New("per connection memory must be positive")
	}

	redoLogBytes := int64(float64(bufferPoolBytes) * profile.RedoLogRatio)
	if redoLogBytes < 512*mb {
		redoLogBytes = 512 * mb
	}
	redoLogBytes = readableCapacity(redoLogBytes)

	innodbLogBufferBytes := int64(16) * mb
	globalBuffers := bufferPoolBytes + innodbLogBufferBytes + 512*mb
	freeForConn := memBytes - globalBuffers
	if freeForConn <= perConnMem {
		return ConfigVars{}, errors.New("machine memory is too small for selected mysql profile")
	}

	maxByPerConn := int(freeForConn / perConnMem)
	maxConnections := minInt(profile.MaxMaxConnections, maxByPerConn, info.MemoryGB*profile.MaxConnPerGB)
	if maxConnections < 20 {
		maxConnections = 20
	}

	safeLimit := int64(float64(memBytes) * 0.85)
	for {
		mysqlTotal := globalBuffers + int64(maxConnections)*perConnMem
		if mysqlTotal < safeLimit || maxConnections <= 20 {
			break
		}
		maxConnections -= 10
	}
	if maxConnections < 20 {
		maxConnections = 20
	}

	serverID := input.ServerID
	if serverID <= 0 {
		serverID = 1
	}
	return ConfigVars{
		ServerID:               serverID,
		Port:                   input.Port,
		MySQLUser:              input.MySQLUser,
		InstanceDir:            input.InstanceDir,
		DataDir:                input.DataDir,
		BinlogDir:              input.BinlogDir,
		RedoDir:                input.RedoDir,
		UndoDir:                input.UndoDir,
		TmpDir:                 input.TmpDir,
		BaseDir:                input.BaseDir,
		MyCnfPath:              input.MyCnfPath,
		SocketPath:             input.SocketPath,
		PIDFile:                input.PIDFile,
		ErrorLog:               input.ErrorLog,
		CharacterSetsDir:       input.CharacterSetsDir,
		PluginDir:              input.PluginDir,
		OpenFilesLimit:         profile.OpenFilesLimit,
		LimitNProc:             65536,
		SysctlSwappiness:       profile.SysctlSwappiness,
		CharacterSetServer:     "utf8mb4",
		CollationServer:        "utf8mb4_0900_ai_ci",
		Autocommit:             1,
		TransactionIsolation:   "READ-COMMITTED",
		InteractiveTimeout:     1800,
		WaitTimeout:            1800,
		LockWaitTimeout:        1800,
		MaxConnectErrors:       1000,
		MaxAllowedPacket:       "64M",
		LogTimestamps:          "SYSTEM",
		SlowQueryLog:           1,
		SlowQueryLogFile:       input.DataDir + "/slow.log",
		LongQueryTime:          "2",
		MinExaminedRowLimit:    100,
		LogSlowAdminStatements: 1,
		LogSlowReplicaStmts:    1,
		LogThrottleNoIndex:     10,
		BinlogFormat:           "ROW",
		SyncBinlog:             1,
		BinlogExpireSeconds:    604800,
		BinlogRowsQueryEvents:  1,
		LogReplicaUpdates:      1,
		GTIDMode:               "ON",
		EnforceGTIDConsistency: "ON",
		RelayLogRecovery:       1,
		ReadOnly:               1,
		SuperReadOnly:          1,
		DefaultStorageEngine:   "InnoDB",
		InnodbDataFilePath:     fmt.Sprintf("ibdata1:%dM:autoextend", profile.InnodbDataFileInitialMB),
		InnodbTempDataFilePath: fmt.Sprintf("ibtmp1:%dM:autoextend:max:30720M", profile.InnodbTempFileInitialMB),
		InnodbFlushLogAtCommit: 1,
		InnodbLockWaitTimeout:  600,
		InnodbFilePerTable:     1,
		InnodbFlushMethod:      "O_DIRECT",
		InnodbLogBufferSize:    bytesToMySQLSize(innodbLogBufferBytes),
		InnodbLogBufferBytes:   innodbLogBufferBytes,
		InnodbReadIOThreads:    8,
		InnodbWriteIOThreads:   8,
		KeyBufferSize:          "32M",
		MyISAMSortBufferSize:   "64M",
		BufferPoolSize:         bytesToMySQLSize(bufferPoolBytes),
		BufferPoolSizeBytes:    bufferPoolBytes,
		BufferPoolInstances:    bufferPoolInstances,
		MaxConnections:         maxConnections,
		RedoLogCapacity:        bytesToMySQLSize(redoLogBytes),
		RedoLogCapacityBytes:   redoLogBytes,
		InnodbLogFilesInGroup:  2,
		TableOpenCache:         profile.TableOpenCache,
		ThreadCacheSize:        profile.ThreadCacheSize,
		SortBufferSize:         bytesToMySQLSize(sortBuffer),
		SortBufferSizeBytes:    sortBuffer,
		ReadBufferSize:         bytesToMySQLSize(readBuffer),
		ReadBufferSizeBytes:    readBuffer,
		ReadRndBufferSize:      bytesToMySQLSize(readRndBuffer),
		ReadRndBufferSizeBytes: readRndBuffer,
		JoinBufferSize:         bytesToMySQLSize(joinBuffer),
		JoinBufferSizeBytes:    joinBuffer,
		SkipNameResolve:        1,
		SymbolicLinks:          0,
	}, nil
}

// NormalizeConfigInput 对配置输入进行标准化处理，为未设置的字段填充默认值。
func NormalizeConfigInput(input ConfigInput) ConfigInput {
	if input.Port <= 0 {
		input.Port = 3306
	}
	if strings.TrimSpace(input.MySQLUser) == "" {
		input.MySQLUser = "mysql"
	}
	if strings.TrimSpace(input.InstanceDir) == "" {
		input.InstanceDir = fmt.Sprintf("/data/%d", input.Port)
	}
	if strings.TrimSpace(input.DataDir) == "" {
		input.DataDir = input.InstanceDir + "/data"
	}
	if strings.TrimSpace(input.BinlogDir) == "" {
		input.BinlogDir = input.InstanceDir + "/binlog"
	}
	if strings.TrimSpace(input.RedoDir) == "" {
		input.RedoDir = input.InstanceDir + "/redo"
	}
	if strings.TrimSpace(input.UndoDir) == "" {
		input.UndoDir = input.InstanceDir + "/undo"
	}
	if strings.TrimSpace(input.TmpDir) == "" {
		input.TmpDir = input.InstanceDir + "/tmp"
	}
	if strings.TrimSpace(input.BaseDir) == "" {
		input.BaseDir = fmt.Sprintf("/usr/local/mysql-%d", input.Port)
	}
	if strings.TrimSpace(input.MyCnfPath) == "" {
		input.MyCnfPath = input.InstanceDir + "/my.cnf"
	}
	if strings.TrimSpace(input.SocketPath) == "" {
		input.SocketPath = input.DataDir + "/mysql.sock"
	}
	if strings.TrimSpace(input.ErrorLog) == "" {
		input.ErrorLog = input.DataDir + "/mysqld.log"
	}
	if strings.TrimSpace(input.PIDFile) == "" {
		input.PIDFile = input.DataDir + "/mysqld.pid"
	}
	if strings.TrimSpace(input.CharacterSetsDir) == "" {
		input.CharacterSetsDir = input.BaseDir + "/share/charsets"
	}
	if strings.TrimSpace(input.PluginDir) == "" {
		input.PluginDir = input.BaseDir + "/lib/plugin"
	}
	return input
}

// ApplyRuntimeParameters applies user supplied, whitelisted my.cnf overrides to
// values calculated from the machine resources and selected profile. Empty
// values deliberately mean "keep the calculated value".
func ApplyRuntimeParameters(vars *ConfigVars, parameters map[string]string) error {
	if vars == nil {
		return errors.New("mysql config vars are required")
	}
	for rawName, rawValue := range parameters {
		name := strings.ToLower(strings.TrimSpace(rawName))
		value := strings.TrimSpace(rawValue)
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, "\r\n\x00") || len(value) > 256 {
			return fmt.Errorf("invalid mysql runtime parameter %s", name)
		}
		switch name {
		case "character_set_server":
			vars.CharacterSetServer = value
		case "collation_server":
			vars.CollationServer = value
		case "autocommit":
			if err := setBinaryInt(name, value, &vars.Autocommit); err != nil {
				return err
			}
		case "transaction_isolation":
			if err := requireEnum(name, value, "READ-UNCOMMITTED", "READ-COMMITTED", "REPEATABLE-READ", "SERIALIZABLE"); err != nil {
				return err
			}
			vars.TransactionIsolation = strings.ToUpper(value)
		case "interactive_timeout":
			if err := setNonNegativeInt(name, value, &vars.InteractiveTimeout); err != nil {
				return err
			}
		case "wait_timeout":
			if err := setNonNegativeInt(name, value, &vars.WaitTimeout); err != nil {
				return err
			}
		case "lock_wait_timeout":
			if err := setNonNegativeInt(name, value, &vars.LockWaitTimeout); err != nil {
				return err
			}
		case "max_connect_errors":
			if err := setNonNegativeInt(name, value, &vars.MaxConnectErrors); err != nil {
				return err
			}
		case "max_allowed_packet":
			if err := setMySQLSize(name, value, &vars.MaxAllowedPacket); err != nil {
				return err
			}
		case "table_open_cache":
			if err := setNonNegativeInt(name, value, &vars.TableOpenCache); err != nil {
				return err
			}
		case "thread_cache_size":
			if err := setNonNegativeInt(name, value, &vars.ThreadCacheSize); err != nil {
				return err
			}
		case "max_connections":
			if err := setPositiveInt(name, value, &vars.MaxConnections); err != nil {
				return err
			}
		case "log_timestamps":
			if err := requireEnum(name, value, "UTC", "SYSTEM"); err != nil {
				return err
			}
			vars.LogTimestamps = strings.ToUpper(value)
		case "slow_query_log":
			if err := setBinaryInt(name, value, &vars.SlowQueryLog); err != nil {
				return err
			}
		case "slow_query_log_file":
			if !strings.HasPrefix(value, "/") {
				return fmt.Errorf("mysql runtime parameter %s must be an absolute path", name)
			}
			vars.SlowQueryLogFile = value
		case "long_query_time":
			if err := setNonNegativeDecimal(name, value, &vars.LongQueryTime); err != nil {
				return err
			}
		case "min_examined_row_limit":
			if err := setNonNegativeInt(name, value, &vars.MinExaminedRowLimit); err != nil {
				return err
			}
		case "log_slow_admin_statements":
			if err := setBinaryInt(name, value, &vars.LogSlowAdminStatements); err != nil {
				return err
			}
		case "log_slow_replica_statements":
			if err := setBinaryInt(name, value, &vars.LogSlowReplicaStmts); err != nil {
				return err
			}
		case "log_throttle_queries_not_using_indexes":
			if err := setNonNegativeInt(name, value, &vars.LogThrottleNoIndex); err != nil {
				return err
			}
		case "binlog_format":
			if err := requireEnum(name, value, "ROW", "STATEMENT", "MIXED"); err != nil {
				return err
			}
			vars.BinlogFormat = strings.ToUpper(value)
		case "sync_binlog":
			if err := setNonNegativeInt(name, value, &vars.SyncBinlog); err != nil {
				return err
			}
		case "binlog_expire_logs_seconds":
			if err := setNonNegativeInt(name, value, &vars.BinlogExpireSeconds); err != nil {
				return err
			}
		case "binlog_rows_query_log_events":
			if err := setBinaryInt(name, value, &vars.BinlogRowsQueryEvents); err != nil {
				return err
			}
		case "log_replica_updates":
			if err := setBinaryInt(name, value, &vars.LogReplicaUpdates); err != nil {
				return err
			}
		case "gtid_mode":
			if err := requireEnum(name, value, "ON", "OFF", "OFF_PERMISSIVE", "ON_PERMISSIVE"); err != nil {
				return err
			}
			vars.GTIDMode = strings.ToUpper(value)
		case "enforce_gtid_consistency":
			if err := requireEnum(name, value, "ON", "OFF", "WARN"); err != nil {
				return err
			}
			vars.EnforceGTIDConsistency = strings.ToUpper(value)
		case "relay_log_recovery":
			if err := setBinaryInt(name, value, &vars.RelayLogRecovery); err != nil {
				return err
			}
		case "read_only":
			if err := setBinaryInt(name, value, &vars.ReadOnly); err != nil {
				return err
			}
		case "super_read_only":
			if err := setBinaryInt(name, value, &vars.SuperReadOnly); err != nil {
				return err
			}
		case "default_storage_engine":
			if err := requireEnum(name, value, "INNODB", "MYISAM"); err != nil {
				return err
			}
			vars.DefaultStorageEngine = value
		case "innodb_data_file_path":
			vars.InnodbDataFilePath = value
		case "innodb_temp_data_file_path":
			vars.InnodbTempDataFilePath = value
		case "innodb_buffer_pool_size":
			if err := setMySQLSize(name, value, &vars.BufferPoolSize); err != nil {
				return err
			}
		case "innodb_buffer_pool_instances":
			if err := setPositiveInt(name, value, &vars.BufferPoolInstances); err != nil {
				return err
			}
		case "innodb_redo_log_capacity":
			if err := setMySQLSize(name, value, &vars.RedoLogCapacity); err != nil {
				return err
			}
		case "innodb_flush_log_at_trx_commit":
			if err := setRangedInt(name, value, &vars.InnodbFlushLogAtCommit, 0, 2); err != nil {
				return err
			}
		case "innodb_lock_wait_timeout":
			if err := setNonNegativeInt(name, value, &vars.InnodbLockWaitTimeout); err != nil {
				return err
			}
		case "innodb_file_per_table":
			if err := setBinaryInt(name, value, &vars.InnodbFilePerTable); err != nil {
				return err
			}
		case "innodb_flush_method":
			if err := requireEnum(name, value, "FSYNC", "O_DSYNC", "LITTLESYNC", "NOSYNC", "O_DIRECT", "O_DIRECT_NO_FSYNC"); err != nil {
				return err
			}
			vars.InnodbFlushMethod = strings.ToUpper(value)
		case "innodb_log_buffer_size":
			if err := setMySQLSize(name, value, &vars.InnodbLogBufferSize); err != nil {
				return err
			}
		case "innodb_read_io_threads":
			if err := setPositiveInt(name, value, &vars.InnodbReadIOThreads); err != nil {
				return err
			}
		case "innodb_write_io_threads":
			if err := setPositiveInt(name, value, &vars.InnodbWriteIOThreads); err != nil {
				return err
			}
		case "key_buffer_size":
			if err := setMySQLSize(name, value, &vars.KeyBufferSize); err != nil {
				return err
			}
		case "sort_buffer_size":
			if err := setMySQLSize(name, value, &vars.SortBufferSize); err != nil {
				return err
			}
		case "read_buffer_size":
			if err := setMySQLSize(name, value, &vars.ReadBufferSize); err != nil {
				return err
			}
		case "read_rnd_buffer_size":
			if err := setMySQLSize(name, value, &vars.ReadRndBufferSize); err != nil {
				return err
			}
		case "join_buffer_size":
			if err := setMySQLSize(name, value, &vars.JoinBufferSize); err != nil {
				return err
			}
		case "myisam_sort_buffer_size":
			if err := setMySQLSize(name, value, &vars.MyISAMSortBufferSize); err != nil {
				return err
			}
		case "open_files_limit":
			if err := setPositiveInt(name, value, &vars.OpenFilesLimit); err != nil {
				return err
			}
		case "limit_nproc":
			if err := setPositiveInt(name, value, &vars.LimitNProc); err != nil {
				return err
			}
		case "sysctl_swappiness":
			if err := setRangedInt(name, value, &vars.SysctlSwappiness, 0, 100); err != nil {
				return err
			}
		case "skip_name_resolve":
			if err := setBinaryInt(name, value, &vars.SkipNameResolve); err != nil {
				return err
			}
		case "symbolic_links":
			if err := setBinaryInt(name, value, &vars.SymbolicLinks); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported mysql runtime parameter %s", name)
		}
	}
	return nil
}

func setPositiveInt(name, value string, target *int) error {
	return setRangedInt(name, value, target, 1, int(^uint(0)>>1))
}
func setNonNegativeInt(name, value string, target *int) error {
	return setRangedInt(name, value, target, 0, int(^uint(0)>>1))
}
func setBinaryInt(name, value string, target *int) error {
	return setRangedInt(name, value, target, 0, 1)
}
func setRangedInt(name, value string, target *int, min, max int) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < min || n > max {
		return fmt.Errorf("invalid value %q for mysql runtime parameter %s", value, name)
	}
	*target = n
	return nil
}
func setMySQLSize(name, value string, target *string) error {
	upper := strings.ToUpper(value)
	if !mysqlSizePattern.MatchString(upper) {
		return fmt.Errorf("invalid size %q for mysql runtime parameter %s", value, name)
	}
	*target = upper
	return nil
}
func setNonNegativeDecimal(name, value string, target *string) error {
	if !mysqlDecimalPattern.MatchString(value) {
		return fmt.Errorf("invalid value %q for mysql runtime parameter %s", value, name)
	}
	*target = value
	return nil
}
func requireEnum(name, value string, allowed ...string) error {
	upper := strings.ToUpper(value)
	for _, item := range allowed {
		if upper == item {
			return nil
		}
	}
	return fmt.Errorf("invalid value %q for mysql runtime parameter %s", value, name)
}

// 内存大小常量定义。
const (
	// mb 表示 1MB 的字节数。
	mb int64 = 1024 * 1024
	// gb 表示 1GB 的字节数。
	gb int64 = 1024 * 1024 * 1024
)

// bytesToMySQLSize 将字节数转换为 MySQL 可识别的大小字符串格式（如 1G、256M）。
func bytesToMySQLSize(v int64) string {
	switch {
	case v%gb == 0:
		return fmt.Sprintf("%dG", v/gb)
	case v%mb == 0:
		return fmt.Sprintf("%dM", v/mb)
	default:
		return fmt.Sprintf("%d", v)
	}
}

// readableCapacity 将字节数向上取整为人类可读的容量值（整 GB 或整 MB）。
func readableCapacity(v int64) int64 {
	if v >= gb {
		return ((v + gb - 1) / gb) * gb
	}
	if v >= mb {
		return ((v + mb - 1) / mb) * mb
	}
	return v
}

// atoiSafe 安全地将数字字符串转换为整数，遇到非数字字符时返回 0。
func atoiSafe(v string) int {
	n := 0
	for _, ch := range v {
		if ch < '0' || ch > '9' {
			return 0
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// minInt 返回多个整数中的最小值。
func minInt(values ...int) int {
	if len(values) == 0 {
		return 0
	}
	out := values[0]
	for _, item := range values[1:] {
		if item < out {
			out = item
		}
	}
	return out
}
