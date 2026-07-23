import { computed, onMounted, onUnmounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'
import IndexManagement from './index-management.js'
import HistogramManagement from './histogram-management.js'
import ExecutionPlan from './execution-plan.js'
import { createParameterWorkbook } from './parameter-export.js'
import OnlineDDLManagement from './online-ddl-management.js'
import DatabaseInspection from './database-inspection.js'
import ArchiveManagement from './archive-management.js'
import ClusterRollingUpgrade from './cluster-rolling-upgrade.js'
import BinlogAnalysis from './binlog-analysis.js'

const request = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, { headers: { 'Content-Type': 'application/json' }, ...options })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(payload.error || '请求失败')
  return payload
}

const value = (item, upper, lower) => item?.[upper] ?? item?.[lower]
const lower = value => String(value || '').toLowerCase()
const normalizeArchitecture = architecture => {
  const arch = lower(architecture)
  if (['amd64', 'x64', 'x86-64', 'x86_64'].includes(arch)) return 'x86_64'
  if (['arm64', 'armv8', 'aarch64'].includes(arch)) return 'aarch64'
  return arch
}
const compareMySQLVersions = (left, right) => {
  const a = String(left || '').split('.').map(Number), b = String(right || '').split('.').map(Number)
  if (a.some(Number.isNaN) || b.some(Number.isNaN)) return 0
  for (let index = 0; index < Math.max(a.length, b.length, 3); index++) {
    if ((a[index] || 0) !== (b[index] || 0)) return (a[index] || 0) - (b[index] || 0)
  }
  return 0
}
const mysql57UpgradeBridgeVersion = '5.7.44'
const parseMySQLVersion = raw => {
  const parts = String(raw || '').trim().split('.')
  if (parts.length < 2 || parts.length > 3 || parts.some(part => !/^\d+$/.test(part))) return null
  return { major: Number(parts[0]), minor: Number(parts[1]), patch: Number(parts[2] || 0) }
}
// Mirrors the API's direct-upgrade gate so the selector only offers a safe
// next hop. The API remains authoritative and validates the same rule again.
const mysqlDirectUpgradeDecision = (current, target) => {
  const from = parseMySQLVersion(current), to = parseMySQLVersion(target)
  if (!from || !to) return { allowed: false, reason: '版本格式无法识别' }
  if (compareMySQLVersions(target, current) <= 0) return { allowed: false, reason: '目标版本必须高于当前版本' }
  if (from.major === 5) {
    if (to.major === 5) return { allowed: true }
    if (compareMySQLVersions(current, mysql57UpgradeBridgeVersion) < 0) return { allowed: false, bridge: mysql57UpgradeBridgeVersion, reason: `必须先升级到 MySQL ${mysql57UpgradeBridgeVersion}` }
    if (to.major !== 8 || to.minor !== 0) return { allowed: false, bridge: '8.0', reason: 'MySQL 5.7 只能先升级到 MySQL 8.0' }
    return { allowed: true }
  }
  if (from.major === 8 && from.minor < 4 && to.major > 8) return { allowed: false, bridge: '8.4', reason: '必须先升级到最新 MySQL 8.4 LTS' }
  return { allowed: true }
}
const mysqlPrivilegeOptions = [
  'SELECT', 'INSERT', 'UPDATE', 'DELETE', 'CREATE', 'CREATE USER', 'ALTER', 'DROP', 'SHOW VIEW', 'TRIGGER', 'EVENT',
  'PROCESS', 'RELOAD', 'LOCK TABLES', 'REPLICATION CLIENT', 'REPLICATION SLAVE', 'SUPER', 'CONNECTION_ADMIN', 'SYSTEM_VARIABLES_ADMIN', 'REPLICATION_SLAVE_ADMIN', 'BACKUP_ADMIN', 'CLONE_ADMIN'
]
const mysqlDynamicPrivileges = new Set(['CONNECTION_ADMIN', 'SYSTEM_VARIABLES_ADMIN', 'REPLICATION_SLAVE_ADMIN', 'BACKUP_ADMIN', 'CLONE_ADMIN'])
const isMySQL57Version = version => /(?:^|[^0-9])5\.7(?:\.|[^0-9]|$)/.test(String(version || ''))
const mysqlVersionCapabilities = version => {
  const match = String(version || '').match(/(?:^|[^0-9])(5\.7|8\.[0-4]|9\.[0-7])(?:\.([0-9]+))?/)
  if (!match) return { legacy57: false, legacyTransactionVariable: false, legacyReplication: false, legacyRedo: false, dynamicPrivileges: true, clone: true }
  const [major, minor] = match[1].split('.').map(Number), patch = Number(match[2] || 0)
  const number = major * 10000 + minor * 100 + patch
  return { legacy57: major === 5, legacyTransactionVariable: major === 5 && patch < 20, legacyReplication: number < 80026, legacyRedo: number < 80030, dynamicPrivileges: number >= 80017, clone: number >= 80017 }
}
const privilegesForMySQLVersion = version => !mysqlVersionCapabilities(version).dynamicPrivileges
  ? mysqlPrivilegeOptions.filter(privilege => !mysqlDynamicPrivileges.has(privilege))
  : mysqlPrivilegeOptions.filter(privilege => privilege !== 'SUPER')
const mysqlRuntimeParameterGroups = [
  { name: '连接、字符集与缓存', fields: [
    { key: 'character_set_server', label: 'character_set_server', default: 'utf8mb4' }, { key: 'collation_server', label: 'collation_server', default: 'utf8mb4_0900_ai_ci' },
    { key: 'skip_name_resolve', label: 'skip_name_resolve', default: '1', options: ['1','0'] }, { key: 'symbolic_links', label: 'symbolic_links', default: '0', options: ['0','1'] }, { key: 'autocommit', label: 'autocommit', default: '1', options: ['1','0'] },
    { key: 'transaction_isolation', label: 'transaction_isolation', default: 'READ-COMMITTED', options: ['READ-COMMITTED','REPEATABLE-READ','READ-UNCOMMITTED','SERIALIZABLE'] },
    { key: 'max_connections', label: 'max_connections', placeholder: '按机器内存与 Profile 自动计算' }, { key: 'max_connect_errors', label: 'max_connect_errors', default: '1000' }, { key: 'max_allowed_packet', label: 'max_allowed_packet', default: '64M' },
    { key: 'interactive_timeout', label: 'interactive_timeout', default: '1800' }, { key: 'wait_timeout', label: 'wait_timeout', default: '1800' }, { key: 'lock_wait_timeout', label: 'lock_wait_timeout', default: '1800' }, { key: 'table_open_cache', label: 'table_open_cache', placeholder: '由 Profile 自动计算' }, { key: 'thread_cache_size', label: 'thread_cache_size', placeholder: '由 Profile 自动计算' }
  ]},
  { name: '慢查询与日志', fields: [
    { key: 'log_timestamps', label: 'log_timestamps', default: 'SYSTEM', options: ['SYSTEM','UTC'] }, { key: 'slow_query_log', label: 'slow_query_log', default: '1', options: ['1','0'] },
    { key: 'slow_query_log_file', label: 'slow_query_log_file', placeholder: '数据目录/slow.log' }, { key: 'long_query_time', label: 'long_query_time', default: '2' },
    { key: 'min_examined_row_limit', label: 'min_examined_row_limit', default: '100' }, { key: 'log_slow_admin_statements', label: 'log_slow_admin_statements', default: '1', options: ['1','0'] },
    { key: 'log_slow_replica_statements', label: 'log_slow_replica_statements', default: '1', options: ['1','0'] }, { key: 'log_throttle_queries_not_using_indexes', label: '无索引日志限流', default: '10' }
  ]},
  { name: 'Binlog、GTID 与只读', fields: [
    { key: 'binlog_format', label: 'binlog_format', default: 'ROW', options: ['ROW','MIXED','STATEMENT'] }, { key: 'sync_binlog', label: 'sync_binlog', default: '1' },
    { key: 'binlog_expire_logs_seconds', label: 'binlog 保留秒数', default: '604800' }, { key: 'binlog_rows_query_log_events', label: 'binlog_rows_query_log_events', default: '1', options: ['1','0'] }, { key: 'log_replica_updates', label: 'log_replica_updates', default: '1', options: ['1','0'] },
    { key: 'gtid_mode', label: 'gtid_mode', default: 'ON', options: ['ON','OFF','ON_PERMISSIVE','OFF_PERMISSIVE'] }, { key: 'enforce_gtid_consistency', label: 'enforce_gtid_consistency', default: 'ON', options: ['ON','WARN','OFF'] },
    { key: 'relay_log_recovery', label: 'relay_log_recovery', default: '1', options: ['1','0'] }, { key: 'read_only', label: 'read_only', default: '1', options: ['1','0'] }, { key: 'super_read_only', label: 'super_read_only', default: '1', options: ['1','0'] }
  ]},
  { name: 'InnoDB', fields: [
    { key: 'default_storage_engine', label: 'default_storage_engine', default: 'InnoDB', options: ['InnoDB','MyISAM'] }, { key: 'innodb_data_file_path', label: 'innodb_data_file_path', default: 'ibdata1:128M:autoextend' }, { key: 'innodb_temp_data_file_path', label: 'innodb_temp_data_file_path', default: 'ibtmp1:128M:autoextend:max:30720M' },
    { key: 'innodb_buffer_pool_size', label: 'innodb_buffer_pool_size', placeholder: '按机器内存与 Profile 自动计算' }, { key: 'innodb_buffer_pool_instances', label: 'buffer_pool_instances', placeholder: '按 Buffer Pool 自动计算' }, { key: 'innodb_redo_log_capacity', label: 'innodb_redo_log_capacity', placeholder: '按 Buffer Pool 自动计算' },
    { key: 'innodb_flush_log_at_trx_commit', label: 'flush_log_at_trx_commit', default: '1', options: ['1','2','0'] }, { key: 'innodb_lock_wait_timeout', label: 'innodb_lock_wait_timeout', default: '600' },
    { key: 'innodb_file_per_table', label: 'innodb_file_per_table', default: '1', options: ['1','0'] }, { key: 'innodb_flush_method', label: 'innodb_flush_method', default: 'O_DIRECT', options: ['O_DIRECT','O_DIRECT_NO_FSYNC','FSYNC','O_DSYNC'] }, { key: 'innodb_log_buffer_size', label: 'innodb_log_buffer_size', default: '16M' },
    { key: 'innodb_read_io_threads', label: 'innodb_read_io_threads', default: '8' }, { key: 'innodb_write_io_threads', label: 'innodb_write_io_threads', default: '8' }
  ]},
  { name: '会话缓冲与系统限制', fields: [
    { key: 'key_buffer_size', label: 'key_buffer_size', default: '32M' }, { key: 'myisam_sort_buffer_size', label: 'myisam_sort_buffer_size', default: '64M' }, { key: 'sort_buffer_size', label: 'sort_buffer_size', placeholder: '由 Profile 自动计算' }, { key: 'read_buffer_size', label: 'read_buffer_size', placeholder: '由 Profile 自动计算' },
    { key: 'read_rnd_buffer_size', label: 'read_rnd_buffer_size', placeholder: '由 Profile 自动计算' }, { key: 'join_buffer_size', label: 'join_buffer_size', placeholder: '由 Profile 自动计算' }, { key: 'open_files_limit', label: 'open_files_limit', placeholder: '由 Profile 自动计算' },
    { key: 'limit_nproc', label: 'systemd LimitNPROC', default: '65536' }, { key: 'sysctl_swappiness', label: 'vm.swappiness', placeholder: '由 Profile 自动计算' }
  ]}
]
const directoryFields = [
  ['instance_dir','实例根目录','/data/3306'], ['data_dir','数据目录','实例根目录/data'], ['binlog_dir','binlog 目录','实例根目录/binlog'],
  ['redo_dir','redo 目录','实例根目录/redo'], ['undo_dir','undo 目录','实例根目录/undo'], ['tmp_dir','tmp 目录','实例根目录/tmp'],
  ['base_dir','安装目录','/usr/local/mysql'], ['my_cnf_path','my.cnf 路径','实例根目录/my.cnf'], ['socket_path','Socket 文件','数据目录/mysql.sock'],
  ['error_log','错误日志','数据目录/mysqld.log'], ['pid_file','PID 文件','数据目录/mysqld.pid'], ['character_sets_dir','字符集目录','安装目录/share/charsets'], ['plugin_dir','插件目录','安装目录/lib/plugin']
]
const installStages = ['环境与兼容性预检', '创建目录和运行用户', '分发并解压安装包', '渲染 my.cnf 与 systemd', '初始化数据库与账号', '启动服务并验证心跳']
const dynamicMySQLParameters = new Set([
  'autocommit','binlog_expire_logs_seconds','binlog_format','connect_timeout','event_scheduler','general_log','general_log_file','group_concat_max_len','innodb_buffer_pool_size','innodb_flush_log_at_trx_commit','innodb_io_capacity','innodb_io_capacity_max','innodb_lock_wait_timeout','innodb_max_dirty_pages_pct','innodb_old_blocks_time','innodb_online_alter_log_max_size','innodb_print_all_deadlocks','innodb_purge_threads','innodb_read_io_threads','innodb_stats_on_metadata','innodb_write_io_threads','interactive_timeout','join_buffer_size','lock_wait_timeout','log_output','long_query_time','max_allowed_packet','max_connect_errors','max_connections','max_execution_time','max_heap_table_size','max_prepared_stmt_count','net_read_timeout','net_write_timeout','optimizer_switch','read_buffer_size','read_only','read_rnd_buffer_size','slow_query_log','sort_buffer_size','sql_mode','super_read_only','sync_binlog','table_definition_cache','table_open_cache','thread_cache_size','tmp_table_size','transaction_isolation','wait_timeout'
])
const parameterCategory = name => {
  if (/^innodb_/.test(name)) return 'InnoDB'
  if (/^(binlog_|log_|gtid_|relay_)/.test(name)) return '日志与复制'
  if (/^(max_|thread_|table_|open_files|back_log|connect_|wait_|interactive_)/.test(name)) return '连接与缓存'
  if (/^(character_|collation_|sql_mode|time_zone|lc_)/.test(name)) return '字符集与 SQL'
  if (/^(performance_schema|optimizer_|join_|sort_|read_|tmp_)/.test(name)) return '性能与优化器'
  return '其他'
}
const baseMySQLParameterCatalogNames = [...new Set([
  ...mysqlRuntimeParameterGroups.flatMap(group => group.fields.map(field => field.key)).filter(name => !/^(limit_|sysctl_)/.test(name)),
  ...dynamicMySQLParameters
])].sort()
const summary = (input, limit = 180) => {
  const text = String(input || '').replace(/((?:root_)?password["']?\s*[:=]\s*)[^\s,;}]+/gi, '$1******').replace(/(-p)([^\s]+)/g, '$1******').replace(/\s+/g, ' ').trim()
  if (!text) return ''
  const first = text.split(/\s+\|\s+/).find(part => part.trim()) || '操作未完成'
  return first.length > limit ? `${first.slice(0, Math.max(1, limit - 1))}…` : first
}

export default {
  name: 'InstanceManagement',
  components: { IndexManagement, HistogramManagement, ExecutionPlan, OnlineDDLManagement, DatabaseInspection, ArchiveManagement, ClusterRollingUpgrade, BinlogAnalysis },
  props: {
    cluster: { type: Object, required: true }, machines: { type: Array, default: () => [] },
    instances: { type: Array, default: () => [] }, packages: { type: Array, default: () => [] },
    topology: { type: Object, default: () => ({ nodes: [], edges: [] }) },
    accountPresets: { type: Array, default: () => [] },
    initialView: { type: String, default: 'instances' }
  },
  emits: ['close', 'refresh', 'open-task', 'view-change'],
  setup(props, { emit }) {
    const allowedViews = new Set(['instances', 'inspection', 'execution-plan', 'online-ddl', 'indexes', 'histograms', 'archive', 'binlog-analysis', 'install', 'users', 'accounts', 'params', 'agent-collect', 'upgrade'])
    const view = ref(allowedViews.has(props.initialView) ? props.initialView : 'instances'), busy = ref(false), error = ref(''), notice = ref('')
    const liveInstances = ref(props.instances), instancesRefreshing = ref(false), instancesUpdatedAt = ref('')
    const instanceKeyword = ref(''), instanceStatus = ref('all'), instancePage = ref(1), instancePageSize = ref(20), expandedInstance = ref('')
    const selectedMachines = ref([]), targetConfigs = ref({}), accountsInitialized = ref(false)
    const presetAccounts = ref([]), presetAccountsDirty = ref(false)
    const vipInterfaceOptions = ref([]), vipInterfaceLoading = ref(false), vipInterfaceError = ref('')
    const install = ref({
      mysql_user: 'mysql', root_password: '', profile: 'default', install_pt_tools: false, install_xtrabackup: false, memory_allocator: 'system', base_dir: '/usr/local/mysql', accounts: [], independent_base_config: false,
      configure_topology: true, architecture: 'master_slave', primary_machine_id: '', secondary_master_machine_id: '',
      enable_vip: false,
      vip: { vip_name: '业务 VIP', vip_address: '', vip_prefix: 24, vip_route_mode: 'AUTO', default_interface: '', arping_count: 3 }
    })
    const showInstallRootPassword = ref(false)
    const targetRootPasswordVisibility = ref({})
    const collectConfig = ref({ enabled: true, version: '', tasks: [] })
    const paramKeyword = ref(''), paramCategory = ref('all')
    const liveParameters = ref([]), parameterTask = ref(null), parametersCollected = ref(false)
    const parameterAuth = ref({ instance: '' })
    const parameterChanges = ref([]), parameterApplyScope = ref('instance')
    const parameterRestartDialog = ref({ open: false, scope: 'instance' })
    const lifecycleDialog = ref({ open: false, item: null, action: 'restart', riskAcknowledged: false, primaryAcknowledged: false, deepDataCheck: true, confirmation: '' })
    const upgrade = ref({ instance: '', version: '', architecture: '', force: false, risk_acknowledged: false })
    const upgradePrecheck = ref({ status: 'idle', taskID: '', report: '', checker: '', checkedAt: '' })
    const mysqlUsers = ref([]), usersLoaded = ref(false), userKeyword = ref(''), userPage = ref(1), userPageSize = 8
    const userDialog = ref({ open: false, mode: 'create', username: '', host: '%', password: '', privileges: [] })
    const deleteUserDialog = ref({ open: false, user: null, confirmation: '' })
    const clusterName = computed(() => value(props.cluster, 'Name', 'name'))
    const machineIDs = computed(() => new Set(props.machines.map(item => value(item, 'ID', 'id'))))
    const machineIPs = computed(() => new Set(props.machines.map(item => value(item, 'IP', 'ip'))))
    const clusterInstances = computed(() => liveInstances.value.filter(item => {
      const explicit = value(item, 'Cluster', 'cluster')
      return explicit ? explicit === clusterName.value : machineIDs.value.has(value(item, 'MachineID', 'machine_id')) || machineIPs.value.has(value(item, 'MachineIP', 'machine_ip'))
    }))
    watch(() => props.instances, items => { liveInstances.value = items || [] }, { immediate: true })
    const refreshInstances = async (showError = false) => {
      if (instancesRefreshing.value) return
      instancesRefreshing.value = true
      try {
        const items = await request('/mysql/instances')
        const nextItems = Array.isArray(items) ? items : (items?.instances || items?.items || [])
        const fingerprint = rows => rows.map(item => [value(item, 'MachineID', 'machine_id'), value(item, 'Port', 'port'), value(item, 'Status', 'status'), value(item, 'HeartbeatStatus', 'heartbeat_status')].join(':')).sort().join('|')
        const changed = fingerprint(liveInstances.value) !== fingerprint(nextItems)
        liveInstances.value = nextItems
        instancesUpdatedAt.value = new Date().toLocaleTimeString('zh-CN', { hour12: false })
        if (changed) emit('refresh')
      } catch (err) {
        if (showError) error.value = `刷新实例失败：${err.message}`
      } finally { instancesRefreshing.value = false }
    }
    const instanceKey = item => `${value(item, 'MachineID', 'machine_id') || value(item, 'MachineIP', 'machine_ip')}:${value(item, 'Port', 'port')}`
    const instanceHealthCode = item => {
      const heartbeat = lower(value(item, 'HeartbeatStatus', 'heartbeat_status'))
      const status = lower(value(item, 'Status', 'status'))
      if (['stopped', 'shutdown'].includes(status)) return 'stopped'
      if (/(fail|error|offline|critical)/.test(`${heartbeat} ${status}`)) return 'error'
      if (['ok', 'success', 'healthy', 'online', 'running'].includes(heartbeat) || ['ok', 'success', 'healthy', 'online', 'running'].includes(status)) return 'healthy'
      return 'unknown'
    }
    const instanceHealthLabel = item => ({ healthy: '运行正常', stopped: '已安全关闭', error: '状态异常', unknown: '等待上报' })[instanceHealthCode(item)]
    const failures = computed(() => clusterInstances.value.filter(item => instanceHealthCode(item) === 'error'))
    const filteredInstances = computed(() => {
      const keyword = lower(instanceKeyword.value).trim()
      return clusterInstances.value.filter(item => {
        if (instanceStatus.value !== 'all' && instanceHealthCode(item) !== instanceStatus.value) return false
        if (!keyword) return true
        return [
          value(item, 'MachineName', 'machine_name'), value(item, 'MachineIP', 'machine_ip'), value(item, 'MachineID', 'machine_id'),
          value(item, 'Port', 'port'), value(item, 'ServerID', 'server_id'), value(item, 'Version', 'version'),
          value(item, 'PackageName', 'package_name'), value(item, 'Architecture', 'architecture'), value(item, 'Profile', 'profile')
        ].some(field => lower(field).includes(keyword))
      })
    })
    const instancePageCount = computed(() => Math.max(1, Math.ceil(filteredInstances.value.length / Number(instancePageSize.value || 20))))
    const pagedInstances = computed(() => {
      const start = (instancePage.value - 1) * Number(instancePageSize.value)
      return filteredInstances.value.slice(start, start + Number(instancePageSize.value))
    })
    watch([instanceKeyword, instanceStatus, instancePageSize], () => { instancePage.value = 1 })
    watch(instancePageCount, total => { if (instancePage.value > total) instancePage.value = total })
    const toggleInstanceDetails = item => { expandedInstance.value = expandedInstance.value === instanceKey(item) ? '' : instanceKey(item) }
    const parameterCatalogDefinitions = () => {
      const names = new Set(baseMySQLParameterCatalogNames)
      for (const pkg of props.packages || []) {
        for (const group of pkg.runtime_parameter_groups || []) {
          for (const field of group.fields || []) if (field.key) names.add(String(field.key).toLowerCase())
        }
      }
      return [...names].sort().map(name => ({
        name, value: '', editValue: '', dynamic: dynamicMySQLParameters.has(name), category: parameterCategory(name), collected: false, compatible: null
      }))
    }
    const resetParameterCatalog = () => {
      liveParameters.value = parameterCatalogDefinitions()
      parametersCollected.value = false
      parameterChanges.value = []
    }
    resetParameterCatalog()
    const paramCategories = computed(() => [...new Set(liveParameters.value.map(item => item.category))])
    const filteredParams = computed(() => liveParameters.value.filter(item => {
      const categoryOK = paramCategory.value === 'all' || item.category === paramCategory.value
      const text = [item.name, item.category, item.value].filter(Boolean).join(' ').toLowerCase()
      return categoryOK && (!paramKeyword.value || text.includes(paramKeyword.value.toLowerCase()))
    }))
    const parameterGroups = computed(() => paramCategories.value.map(category => ({
      category,
      items: filteredParams.value.filter(item => item.category === category)
    })).filter(group => group.items.length))
    const collectedParameterCount = computed(() => liveParameters.value.filter(item => item.collected).length)
    const parameterDynamicCount = computed(() => liveParameters.value.filter(item => item.collected && item.dynamic).length)
    const parameterRestartCount = computed(() => liveParameters.value.filter(item => item.collected && !item.dynamic).length)
    const stagedDynamicCount = computed(() => parameterChanges.value.filter(item => item.dynamic).length)
    const stagedRestartCount = computed(() => parameterChanges.value.filter(item => !item.dynamic).length)
    const compatiblePackages = machine => {
      const arch = normalizeArchitecture(value(machine, 'Architecture', 'architecture') || value(machine, 'Arch', 'arch'))
      return props.packages.filter(pkg => !arch || normalizeArchitecture(pkg.arch || pkg.Arch) === arch)
    }
    const architecturesFor = machine => [...new Set(compatiblePackages(machine).map(pkg => normalizeArchitecture(pkg.arch || pkg.Arch)).filter(Boolean))]
    const versionsFor = (machine, config) => [...new Map(compatiblePackages(machine)
      .filter(pkg => !config.architecture || normalizeArchitecture(pkg.arch || pkg.Arch) === normalizeArchitecture(config.architecture))
      .map(pkg => [pkg.version || pkg.Version, pkg])).values()]
    const ensureTarget = (machine, index) => {
      const id = value(machine, 'ID', 'id')
      if (!targetConfigs.value[id]) targetConfigs.value[id] = { version: '', architecture: '', port: 3306, server_id: index + 1, runtime_parameters: {}, base_config: null }
      const config = targetConfigs.value[id]
      if (!config.architecture) config.architecture = normalizeArchitecture(value(machine, 'Architecture', 'architecture') || value(machine, 'Arch', 'arch')) || architecturesFor(machine)[0] || ''
      if (!config.version) config.version = versionsFor(machine, config)[0]?.version || ''
      return config
    }
    const installPackageFor = (machine, config) => compatiblePackages(machine).find(pkg =>
      (!config.version || (pkg.version || pkg.Version) === config.version) &&
      (!config.architecture || normalizeArchitecture(pkg.arch || pkg.Arch) === normalizeArchitecture(config.architecture))) || null
    const targetArchitectureChanged = (machine, index) => {
      const config = ensureTarget(machine, index)
      if (!versionsFor(machine, config).some(pkg => (pkg.version || pkg.Version) === config.version)) config.version = versionsFor(machine, config)[0]?.version || ''
    }
    const commonBaseConfig = () => ({ mysql_user:install.value.mysql_user, profile:install.value.profile, ...Object.fromEntries(directoryFields.map(([key]) => [key, install.value[key] || ''])) })
    const targetBaseConfig = (machine, index) => {
      const config = ensureTarget(machine, index)
      if (!config.base_config) config.base_config = { ...commonBaseConfig(), root_password: '' }
      if (typeof config.base_config.root_password !== 'string') config.base_config.root_password = ''
      return config.base_config
    }
    const targetPasswordKey = machine => String(value(machine, 'ID', 'id') || value(machine, 'IP', 'ip'))
    const isTargetRootPasswordVisible = machine => !!targetRootPasswordVisibility.value[targetPasswordKey(machine)]
    const toggleTargetRootPassword = machine => {
      const key = targetPasswordKey(machine)
      targetRootPasswordVisibility.value = { ...targetRootPasswordVisibility.value, [key]: !targetRootPasswordVisibility.value[key] }
    }
    const toggleIndependentBaseConfig = () => {
      showInstallRootPassword.value = false
      targetRootPasswordVisibility.value = {}
      if (!install.value.independent_base_config) return
      selectedTopologyMachines.value.forEach((machine, index) => targetBaseConfig(machine, index))
    }
    const clearInstallRootPasswords = () => {
      install.value.root_password = ''
      showInstallRootPassword.value = false
      targetRootPasswordVisibility.value = {}
      Object.values(targetConfigs.value).forEach(config => {
        if (config?.base_config) config.base_config.root_password = ''
      })
    }
    const selectedInstallTargets = computed(() => props.machines.filter(machine => selectedMachines.value.includes(value(machine, 'ID', 'id'))).map((machine, index) => {
      const config = ensureTarget(machine, index)
      return { machine, config, pkg: installPackageFor(machine, config) }
    }))
    const mysqlInstallPrivilegeOptions = computed(() => {
	  const legacy = selectedInstallTargets.value.some(target => !mysqlVersionCapabilities(target.pkg?.version || target.config.version).dynamicPrivileges)
	  return privilegesForMySQLVersion(legacy ? '8.0.11' : '8.0.17')
    })
    const selectedTopologyMachines = computed(() => props.machines.filter(machine => selectedMachines.value.includes(value(machine, 'ID', 'id'))))
    const ipv4Subnet = (input, fallbackPrefix = 24) => {
      const [rawIP, rawPrefix] = String(input || '').split('/'), parts = rawIP.split('.').map(Number), prefix = Math.min(32, Math.max(1, Number(rawPrefix || fallbackPrefix || 24)))
      if (parts.length !== 4 || parts.some(part => !Number.isInteger(part) || part < 0 || part > 255)) return ''
      const address = (((parts[0] << 24) >>> 0) + (parts[1] << 16) + (parts[2] << 8) + parts[3]) >>> 0
      const mask = prefix === 32 ? 0xffffffff : (0xffffffff << (32 - prefix)) >>> 0, network = (address & mask) >>> 0
      return `${[(network >>> 24) & 255, (network >>> 16) & 255, (network >>> 8) & 255, network & 255].join('.')}/${prefix}`
    }
    const vipInterfaceLabel = item => {
      const subnets = [...new Set(item.ips.map(ip => ipv4Subnet(ip, install.value.vip.vip_prefix)).filter(Boolean))]
      return `${item.name}${item.recommended ? ' · 推荐' : ''} · ${item.coverage}/${item.total} 台${subnets.length ? ` · ${subnets.join('、')}` : ''}`
    }
    const selectedVIPInterface = computed(() => vipInterfaceOptions.value.find(item => item.name === install.value.vip.default_interface) || null)
    const scanVIPInterfaces = async (refresh = false) => {
      const targets = selectedTopologyMachines.value
      vipInterfaceError.value = ''
      if (!targets.length) { vipInterfaceOptions.value = []; install.value.vip.default_interface = ''; vipInterfaceError.value = '请先选择目标机器。'; return }
      vipInterfaceLoading.value = true
      try {
        const results = await Promise.allSettled(targets.map(machine => request(`/machines/${encodeURIComponent(value(machine,'ID','id'))}/static-info`, refresh ? { method: 'POST' } : {})))
        const groups = new Map()
        results.forEach((result, index) => {
          if (result.status !== 'fulfilled') return
          const machine = targets[index], host = result.value?.host || result.value?.Host || {}, interfaces = host.interfaces || host.Interfaces || []
          interfaces.forEach(item => {
            const name = item.name || item.Name, ips = item.ips || item.IPs || []
            if (!name || name === 'lo' || !ips.length) return
            if (!groups.has(name)) groups.set(name, { name, members: new Set(), ips: [], managementMatches: 0 })
            const group = groups.get(name); group.members.add(value(machine,'ID','id')); group.ips.push(...ips)
            if (ips.some(ip => String(ip).split('/')[0] === value(machine,'IP','ip'))) group.managementMatches++
          })
        })
        const options = [...groups.values()].map(item => ({ name:item.name, coverage:item.members.size, total:targets.length, ips:[...new Set(item.ips)], managementMatches:item.managementMatches })).sort((a,b) => (b.coverage===b.total)-(a.coverage===a.total) || b.coverage-a.coverage || b.managementMatches-a.managementMatches || a.name.localeCompare(b.name))
        if (options.length) options[0].recommended = true
        vipInterfaceOptions.value = options
        if (!options.some(item => item.name === install.value.vip.default_interface)) install.value.vip.default_interface = options[0]?.name || ''
        if (!options.length) vipInterfaceError.value = '目标机器尚未上报可用 IPv4 网卡，请点击重新扫描。'
        else if (results.some(result => result.status === 'rejected')) vipInterfaceError.value = '部分目标机器扫描失败，推荐结果可能不完整。'
      } finally { vipInterfaceLoading.value = false }
    }
    Object.defineProperty(targetConfigs.value, '_selected_install_targets', { enumerable: false, get: () => selectedInstallTargets.value })
    watch(() => props.machines, items => items.forEach(ensureTarget), { immediate: true, deep: true })
    watch(selectedMachines, ids => {
      if (!ids.includes(install.value.primary_machine_id)) install.value.primary_machine_id = ids[0] || ''
      const secondaryCandidates = ids.filter(id => id !== install.value.primary_machine_id)
      if (!secondaryCandidates.includes(install.value.secondary_master_machine_id)) install.value.secondary_master_machine_id = secondaryCandidates[0] || ''
      if (install.value.independent_base_config) selectedTopologyMachines.value.forEach((machine, index) => targetBaseConfig(machine, index))
      if (install.value.enable_vip) scanVIPInterfaces(false)
    }, { deep: true })
    watch(() => install.value.enable_vip, enabled => { if (enabled) scanVIPInterfaces(false); else vipInterfaceError.value = '' })
    const cloneAccounts = accounts => (accounts || []).map(item => ({ ...item, privileges: [...(item.privileges || [])] }))
    watch(() => props.accountPresets, presets => {
      if (!presetAccountsDirty.value) presetAccounts.value = cloneAccounts(presets)
      if (accountsInitialized.value) return
      if ((presets || []).length) {
        install.value.accounts = cloneAccounts(presets)
        accountsInitialized.value = true
        return
      }
      install.value.accounts = ['monitor','mha','backup'].map(role => ({ role, username: '', password: '', host: '', enabled: true, privileges: [] }))
    }, { immediate: true, deep: true })
    const parameterGroupsFor = target => {
	  const capabilities = mysqlVersionCapabilities(target.pkg?.version || target.config.version)
      const aliases = {
        collation_server: { label: 'collation_server（5.7）', default: 'utf8mb4_unicode_ci' },
        transaction_isolation: { label: 'tx_isolation（平台自动映射）' },
        log_slow_replica_statements: { label: 'log_slow_slave_statements（平台自动映射）' },
        binlog_expire_logs_seconds: { label: 'binlog 保留秒数（自动换算 expire_logs_days）' },
        log_replica_updates: { label: 'log_slave_updates（平台自动映射）' },
        innodb_redo_log_capacity: { label: 'Redo 总容量（自动拆分为 2 个 innodb_log_file_size）' }
      }
	  const useAlias = key => (capabilities.legacy57 && ['collation_server','binlog_expire_logs_seconds'].includes(key)) || (capabilities.legacyTransactionVariable && key === 'transaction_isolation') || (capabilities.legacyReplication && ['log_slow_replica_statements','log_replica_updates'].includes(key)) || (capabilities.legacyRedo && key === 'innodb_redo_log_capacity')
	  const universal = mysqlRuntimeParameterGroups.map(group => ({ ...group, fields: group.fields.map(field => useAlias(field.key) && aliases[field.key] ? { ...field, ...aliases[field.key] } : field) }))
      return [...universal, ...(target.pkg?.runtime_parameter_groups || [])]
    }
    const addCustomAccount = () => install.value.accounts.push({ role: `custom-${Date.now()}`, username: '', password: '', host: '%', enabled: true, privileges: [] })
    const removeCustomAccount = index => install.value.accounts.splice(index, 1)
    const isCustomAccount = account => String(account.role || '').startsWith('custom-')
    const selectedTargetIssues = computed(() => {
      const issues = []
      if (!selectedMachines.value.length) issues.push('请至少选择一台目标机器')
      if (install.value.independent_base_config) selectedInstallTargets.value.forEach((target, index) => {
        const base = targetBaseConfig(target.machine, index), name = value(target.machine, 'Name', 'name')
        if (!String(base.root_password || '').trim()) issues.push(`${name} 未填写 root 密码`)
        if (!String(base.mysql_user || '').trim()) issues.push(`${name} 未填写 MySQL 运行用户`)
        if (!String(base.profile || '').trim()) issues.push(`${name} 未填写参数 Profile`)
      })
      else if (!String(install.value.root_password || '').trim()) issues.push('请填写公共 root 密码')
      if (install.value.configure_topology) {
        if (selectedMachines.value.length < 2) issues.push('配置复制架构至少需要两台目标机器')
        if (!selectedMachines.value.includes(install.value.primary_machine_id)) issues.push('请选择主库机器')
        if (install.value.architecture === 'dual_master' && (!selectedMachines.value.includes(install.value.secondary_master_machine_id) || install.value.secondary_master_machine_id === install.value.primary_machine_id)) issues.push('双主架构需要选择不同的第二主库')
        const mhaAccount = install.value.accounts.find(account => account.role === 'mha' && account.enabled)
        if (!mhaAccount || !String(mhaAccount.username || '').trim() || !String(mhaAccount.password || '').trim()) issues.push('请在初始化账号与权限中启用并完整配置 MHA 管理账号')
        if (install.value.enable_vip) {
          if (!String(install.value.vip.vip_address || '').trim()) issues.push('启用 VIP 后必须填写 VIP 地址')
          if (!Number(install.value.vip.vip_prefix) || Number(install.value.vip.vip_prefix) > 32) issues.push('VIP 前缀必须为 1–32')
          if (!String(install.value.vip.default_interface || '').trim()) issues.push('请选择业务 VIP 使用的目标网卡')
          if (selectedVIPInterface.value && selectedVIPInterface.value.coverage < selectedVIPInterface.value.total) issues.push(`网卡 ${selectedVIPInterface.value.name} 未覆盖全部目标机器`)
        }
      }
      issues.push(...selectedInstallTargets.value.flatMap(target => {
        const targetIssues = []
        if (!target.config.port || target.config.port > 65535) targetIssues.push(`${value(target.machine,'Name','name')} 端口无效`)
        if (!target.config.server_id) targetIssues.push(`${value(target.machine,'Name','name')} server_id 无效`)
        return targetIssues
      }))
      return issues
    })
    const run = async fn => { busy.value = true; error.value = ''; notice.value = ''; try { await fn() } catch (err) { error.value = err.message } finally { busy.value = false } }
    watch(view, next => emit('view-change', next))
    const installPayload = (machine, index) => {
      const cfg = ensureTarget(machine, index)
      const pkg = installPackageFor(machine, cfg)
      const allowed = new Set([...mysqlRuntimeParameterGroups, ...(pkg?.runtime_parameter_groups || [])].flatMap(group => group.fields || []).map(field => field.key))
      const runtimeParameters = Object.fromEntries(Object.entries(cfg.runtime_parameters || {}).filter(([key, parameterValue]) => allowed.has(key) && String(parameterValue || '').trim() !== ''))
      const allowedPrivileges = new Set(privilegesForMySQLVersion(pkg?.version || cfg.version))
      const base = install.value.independent_base_config ? targetBaseConfig(machine, index) : commonBaseConfig()
      const paths = Object.fromEntries(directoryFields.map(([key]) => [key, base[key] || '']))
      const accounts = install.value.accounts.map(account => ({ ...account, privileges: (account.privileges || []).filter(privilege => allowedPrivileges.has(privilege)) }))
      return { machine: value(machine, 'IP', 'ip'), machine_id: value(machine, 'ID', 'id'), version: cfg.version, architecture: cfg.architecture, port: Number(cfg.port), server_id: Number(cfg.server_id), mysql_user: base.mysql_user, root_password: install.value.independent_base_config ? base.root_password : install.value.root_password, profile: base.profile, ...paths, install_pt_tools: (!pkg || pkg.pt_tools_supported) && install.value.install_pt_tools, install_xtrabackup: install.value.install_xtrabackup, memory_allocator: install.value.memory_allocator || 'system', runtime_parameters: runtimeParameters, accounts }
    }
    const createInstallTasks = () => run(async () => {
      if (selectedTargetIssues.value.length) throw new Error(selectedTargetIssues.value[0])
      const selected = props.machines.filter(item => selectedMachines.value.includes(value(item, 'ID', 'id')))
      const payloads = selected.map(installPayload)
      if (install.value.configure_topology) {
        const result = await request(`/clusters/${encodeURIComponent(clusterName.value)}/bootstrap`, { method: 'POST', body: JSON.stringify({ architecture: install.value.architecture, primary_machine_id: install.value.primary_machine_id, secondary_master_machine_id: install.value.architecture === 'dual_master' ? install.value.secondary_master_machine_id : '', enable_vip: install.value.enable_vip, vip: install.value.vip, installs: payloads }) })
        clearInstallRootPasswords()
        notice.value = '批量安装与架构初始化任务已创建，正在展示完整执行流程'
        emit('refresh'); emit('open-task', result)
        return
      }
      const results = await Promise.allSettled(payloads.map(payload => request('/tasks/mysql-install', { method: 'POST', body: JSON.stringify(payload) })))
      const failed = results.filter(item => item.status === 'rejected')
      const succeeded = results.filter(item => item.status === 'fulfilled')
      clearInstallRootPasswords(); emit('refresh')
      if (failed.length) {
        notice.value = `已创建 ${succeeded.length}/${results.length} 个安装任务`
        error.value = summary(failed[0].reason?.message || '部分任务创建失败')
        if (succeeded.length) emit('open-task', succeeded[0].value)
        return
      }
      notice.value = results.length === 1 ? '安装任务已创建，正在展示执行流程' : `已并发创建 ${results.length} 个安装任务，正在展示第一个任务的执行流程`
      if (succeeded.length) emit('open-task', succeeded[0].value)
    })
    const uninstall = instance => run(async () => {
      const ip = value(instance, 'MachineIP', 'machine_ip'), port = Number(value(instance, 'Port', 'port'))
      if (!confirm(`确认卸载 ${ip}:${port} 的 MySQL？该操作会删除实例数据。`)) return
      const result = await request('/tasks/mysql-uninstall', { method: 'POST', body: JSON.stringify({ machine: ip, port }) })
      notice.value = '卸载任务已创建，正在展示执行流程'; emit('refresh'); emit('open-task', result)
    })
    const forget = instance => run(async () => {
      const ip = value(instance, 'MachineIP', 'machine_ip'), port = Number(value(instance, 'Port', 'port'))
      if (!confirm(`仅删除 ${ip}:${port} 的 Manager 实例记录？`)) return
      await request('/mysql/instances', { method: 'DELETE', body: JSON.stringify({ machine: ip, port }) }); notice.value = '实例记录已删除'; emit('refresh')
    })
    const loadParams = () => run(async () => { collectConfig.value = await request('/mysql-dynamic-collect/config') })
    const saveParams = () => run(async () => {
      for (const task of collectConfig.value.tasks || []) {
        if (Number(task.interval_seconds) < 5) throw new Error(`${task.labels?.display_name || task.name} 的采集间隔不能小于 5 秒`)
        if (Number(task.timeout_seconds) < 1 || Number(task.timeout_seconds) > 10) throw new Error(`${task.labels?.display_name || task.name} 的超时时间必须为 1–10 秒`)
        if (Number(task.timeout_seconds) > Number(task.interval_seconds)) throw new Error(`${task.labels?.display_name || task.name} 的超时时间不能大于采集间隔`)
      }
      collectConfig.value = await request('/mysql-dynamic-collect/config', { method: 'PUT', body: JSON.stringify(collectConfig.value) })
      notice.value = 'Agent 采集配置已保存，并将通过心跳下发到集群内 Agent'
    })
    const markPresetAccountsDirty = () => { presetAccountsDirty.value = true }
    const saveAccountPresets = () => run(async () => {
      for (const account of presetAccounts.value) {
        if (!account.enabled) continue
        if (!String(account.username || '').trim()) throw new Error(`${account.role} 的账号名称不能为空`)
        if (!String(account.password || '').trim()) throw new Error(`${account.role} 的密码不能为空`)
        if (!String(account.host || '').trim()) throw new Error(`${account.role} 的允许来源 Host 不能为空`)
        if (!(account.privileges || []).length) throw new Error(`${account.role} 至少需要选择一项权限`)
      }
      const allowedPrivileges = new Set(mysqlPrivilegeOptions)
      const payload = presetAccounts.value.map(account => ({
        ...account,
        username: String(account.username || '').trim(),
        host: String(account.host || '').trim(),
        privileges: (account.privileges || []).filter(privilege => allowedPrivileges.has(privilege))
      }))
      const items = await request('/mysql/account-presets', { method: 'PUT', body: JSON.stringify(payload) })
      presetAccounts.value = cloneAccounts(items)
      presetAccountsDirty.value = false
      const customAccounts = install.value.accounts.filter(isCustomAccount)
      install.value.accounts = [...cloneAccounts(items), ...customAccounts]
      accountsInitialized.value = true
      notice.value = '预设账号已保存，后续创建 MySQL 实例时会自动带入'
      emit('refresh')
    })
    const selectedParameterInstance = computed(() => clusterInstances.value.find(item => `${value(item,'MachineIP','machine_ip')}:${value(item,'Port','port')}` === parameterAuth.value.instance))
    watch(() => parameterAuth.value.instance, resetParameterCatalog)
    const waitTask = async taskID => {
      for (let i = 0; i < 120; i++) {
        const detail = await request(`/tasks?id=${encodeURIComponent(taskID)}`)
        const status = lower(value(detail.task, 'Status', 'status'))
        if (['success','failed'].includes(status)) return detail
        await new Promise(resolve => setTimeout(resolve, 1000))
      }
      throw new Error('等待任务执行超时，请到任务中心查看进度')
    }
    const parameterTaskFailure = detail => {
      const failedEvent = [...(detail?.events || [])]
        .filter(event => {
          const isError = lower(value(event, 'EventType', 'event_type')) === 'error'
          const content = String(value(event, 'Content', 'content') || '').trim()
          return isError && content
        })
        .sort((left, right) => {
          const rightCreatedAt = String(value(right, 'CreatedAt', 'created_at') || '')
          const leftCreatedAt = String(value(left, 'CreatedAt', 'created_at') || '')
          return rightCreatedAt.localeCompare(leftCreatedAt)
        })[0]
      if (failedEvent) return String(value(failedEvent, 'Content', 'content')).trim()
      const failedStep = (detail?.steps || []).find(step => lower(value(step, 'Status', 'status')) === 'failed')
      if (failedStep && String(value(failedStep, 'Message', 'message') || '').trim()) return String(value(failedStep, 'Message', 'message')).trim()
      return value(detail?.task, 'CurrentStep', 'current_step') || '参数采集失败'
    }
    const collectParameters = () => run(async () => {
      if (!parameterAuth.value.instance) throw new Error('请选择需要采集的实例')
      const [machine, port] = parameterAuth.value.instance.split(':')
      const created = await request('/tasks/mysql-parameters', { method: 'POST', body: JSON.stringify({ machine, port: Number(port), action: 'collect' }) })
      parameterTask.value = created
      const detail = await waitTask(value(created.task, 'ID', 'id'))
      if (lower(value(detail.task, 'Status', 'status')) !== 'success') throw new Error(parameterTaskFailure(detail))
      const rows = []
      for (const event of detail.events || []) {
        for (const line of String(value(event,'Content','content') || '').split('\n')) {
          if (!line.startsWith('GMHA_MYSQL_PARAMETER\t')) continue
          const [, name, currentValue = '', applyMode = ''] = line.split('\t')
          const normalizedName = String(name || '').toLowerCase()
          rows.push({ name: normalizedName, value: currentValue, editValue: currentValue, dynamic: applyMode ? applyMode === 'dynamic' : dynamicMySQLParameters.has(normalizedName), category: parameterCategory(normalizedName) })
        }
      }
      const collectedByName = new Map(rows.map(item => [item.name, item]))
      const stableRows = liveParameters.value.map(item => {
        const collected = collectedByName.get(item.name)
        return collected ? { ...item, ...collected, collected: true, compatible: true } : { ...item, value: '', editValue: '', collected: false, compatible: false }
      })
      liveParameters.value = stableRows
      parametersCollected.value = true
      parameterChanges.value = []
      notice.value = `已采集 ${rows.length} 个 MySQL 运行参数，当前管理目录匹配 ${stableRows.filter(item=>item.collected).length} 项`
    })
    const exportParameters = () => {
      if (!parametersCollected.value) { error.value = '请先采集实例参数，再导出 Excel'; return }
      if (!filteredParams.value.length) { error.value = '当前筛选条件下没有可导出的参数'; return }
      const selected = selectedParameterInstance.value
      const workbook = createParameterWorkbook({
        parameters: filteredParams.value,
        changes: parameterChanges.value,
        cluster: clusterName.value,
        instance: parameterAuth.value.instance,
        version: value(selected, 'Version', 'version')
      })
      const url = URL.createObjectURL(new Blob([workbook.bytes], { type: workbook.mimeType }))
      const link = document.createElement('a')
      link.href = url
      link.download = workbook.filename
      document.body.appendChild(link)
      link.click()
      link.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 0)
      notice.value = `已导出 ${filteredParams.value.length} 项运行参数：${workbook.filename}`
    }
    const stageParameter = (item, action = 'update') => {
      if (!item.collected || item.compatible === false) { error.value = `${item.name} 在当前实例版本中不可修改`; return }
      const next = { action, name: item.name, value: action === 'delete' ? '' : String(item.editValue ?? '').trim(), dynamic: item.dynamic }
      if (action === 'update' && !next.value) { error.value = `${item.name} 的参数值不能为空`; return }
      const index = parameterChanges.value.findIndex(item => item.name === next.name)
      if (index >= 0) parameterChanges.value.splice(index, 1, next)
      else parameterChanges.value.push(next)
      notice.value = `${next.name} 已加入待应用清单`
    }
    const removeParameterChange = name => {
      parameterChanges.value = parameterChanges.value.filter(item => item.name !== name)
      const row = liveParameters.value.find(item => item.name === name)
      if (row) row.editValue = row.value
    }
    const isParameterChanged = item => String(item.editValue ?? '') !== String(item.value ?? '')
    const parameterTarget = item => ({ machine: value(item,'MachineIP','machine_ip'), port: Number(value(item,'Port','port')), config_path: value(item,'MyCnfPath','my_cnf_path'), systemd_unit: value(item,'SystemdUnit','systemd_unit') })
    const applyParameterChanges = () => {
      if (!selectedParameterInstance.value) { error.value = '请先选择并采集一个实例'; return }
      if (!parameterChanges.value.length) { error.value = '请至少加入一项参数修改'; return }
      if (stagedRestartCount.value > 0) {
        parameterRestartDialog.value = { open: true, scope: parameterApplyScope.value === 'cluster' ? 'cluster' : 'instance' }
        return
      }
      executeParameterChanges([])
    }
    const executeParameterChanges = restartItems => run(async () => {
      const changeTargets = parameterApplyScope.value === 'cluster' ? clusterInstances.value.map(parameterTarget) : [parameterTarget(selectedParameterInstance.value)]
      const result = await request('/tasks/mysql-parameters', { method: 'POST', body: JSON.stringify({
        targets: changeTargets,
        restart_targets: restartItems,
        restart_confirmed: restartItems.length > 0,
        changes: parameterChanges.value.map(({ action, name, value }) => ({ action, name, value }))
      }) })
      parameterRestartDialog.value.open = false
      const count = parameterChanges.value.length
      parameterChanges.value = []
      notice.value = `已创建 ${count} 项参数变更任务${restartItems.length ? '，将按确认范围重启并验证' : '，动态参数将立即生效'}`
      const task = value(result.parent,'Task','task')?.ID || value(result.parent,'Task','task')?.id ? result.parent : result.tasks?.[0]
      if (task) emit('open-task', task)
    })
    const confirmParameterRestart = () => {
      const items = parameterRestartDialog.value.scope === 'cluster' ? clusterInstances.value.map(parameterTarget) : [parameterTarget(selectedParameterInstance.value)]
      executeParameterChanges(items)
    }
    const roleFor = instance => {
      const ip = value(instance, 'MachineIP', 'machine_ip'), port = Number(value(instance, 'Port', 'port'))
      return props.topology.nodes?.find(node => node.ip === ip && Number(node.port) === port)?.role || value(instance, 'Role', 'role') || '独立实例'
    }
    const lifecycleEndpoint = computed(() => lifecycleDialog.value.item ? `${value(lifecycleDialog.value.item,'MachineIP','machine_ip')}:${value(lifecycleDialog.value.item,'Port','port')}` : '')
    const lifecycleConfirmation = computed(() => `${lifecycleDialog.value.action.toUpperCase()} ${lifecycleEndpoint.value}`)
    const lifecycleIsPrimary = computed(() => lifecycleDialog.value.item && /(^m$|m\/s|master|primary|主)/i.test(String(roleFor(lifecycleDialog.value.item))))
    const lifecycleCanSubmit = computed(() => lifecycleDialog.value.riskAcknowledged && (!lifecycleIsPrimary.value || lifecycleDialog.value.primaryAcknowledged) && lifecycleDialog.value.confirmation === lifecycleConfirmation.value)
    const openLifecycle = (item, action) => {
      lifecycleDialog.value = { open: true, item, action, riskAcknowledged: false, primaryAcknowledged: false, deepDataCheck: true, confirmation: '' }
    }
    const submitLifecycle = () => run(async () => {
      if (!lifecycleCanSubmit.value) throw new Error('请完成风险确认并准确输入确认短语')
      const dialog = lifecycleDialog.value
      const result = await request('/tasks/mysql-lifecycle', { method: 'POST', body: JSON.stringify({
        machine: value(dialog.item,'MachineIP','machine_ip') || value(dialog.item,'MachineID','machine_id'),
        port: Number(value(dialog.item,'Port','port')), action: dialog.action,
        confirmation: dialog.confirmation, risk_acknowledged: dialog.riskAcknowledged,
        primary_acknowledged: dialog.primaryAcknowledged, deep_data_check: dialog.deepDataCheck
      }) })
      lifecycleDialog.value.open = false
      notice.value = dialog.action === 'restart' ? '安全重启任务已创建；将校验原拓扑、复制状态与主从数据一致性' : '安全关闭任务已创建；校验通过后只关闭 MySQL 实例，不关闭宿主机'
      emit('open-task', result)
    })
    const primaryInstance = computed(() => {
      const nodes = props.topology.nodes || []
      const primaries = nodes.filter(node => ['m', 'm/s', 'master', 'primary'].includes(lower(node.role)))
      return clusterInstances.value.find(item => primaries.some(node =>
        (node.machine_id && node.machine_id === value(item, 'MachineID', 'machine_id')) ||
        (node.ip === value(item, 'MachineIP', 'machine_ip') && Number(node.port) === Number(value(item, 'Port', 'port')))
      )) || clusterInstances.value.find(item => /(^m$|m\/s|master|primary|主)/i.test(String(roleFor(item)))) || clusterInstances.value[0] || null
    })
    const mysqlUserPrivilegeOptions = computed(() => privilegesForMySQLVersion(value(primaryInstance.value, 'Version', 'version') || value(primaryInstance.value, 'PackageName', 'package_name')))
    const filteredMySQLUsers = computed(() => {
      const keyword = lower(userKeyword.value).trim()
      return mysqlUsers.value.filter(item => !keyword || [item.username, item.host, ...(item.privileges || [])].some(field => lower(field).includes(keyword)))
    })
    const userPageCount = computed(() => Math.max(1, Math.ceil(filteredMySQLUsers.value.length / userPageSize)))
    const pagedMySQLUsers = computed(() => filteredMySQLUsers.value.slice((userPage.value - 1) * userPageSize, userPage.value * userPageSize))
    watch(userKeyword, () => { userPage.value = 1 })
    watch(userPageCount, count => { if (userPage.value > count) userPage.value = count })
    const dispatchUserAction = async (action, target = {}, privileges = [], password = '') => {
      if (!primaryInstance.value) throw new Error('未识别到当前集群的 MySQL 主节点')
      const created = await request('/tasks/mysql-users', { method: 'POST', body: JSON.stringify({
        machine: value(primaryInstance.value, 'MachineIP', 'machine_ip'), port: Number(value(primaryInstance.value, 'Port', 'port')), action,
        target_username: target.username || '', target_host: target.host || '', target_password: password, privileges
      }) })
      const taskID = value(created.task, 'ID', 'id')
      if (!taskID) throw new Error('用户管理任务创建失败')
      const detail = await waitTask(taskID)
      if (lower(value(detail.task, 'Status', 'status')) !== 'success') throw new Error(parameterTaskFailure(detail))
      return detail
    }
    const loadMySQLUsersData = async () => {
      const detail = await dispatchUserAction('list')
      const rows = []
      for (const event of detail.events || []) {
        for (const line of String(value(event, 'Content', 'content') || '').split('\n')) {
          if (!line.startsWith('GMHA_MYSQL_USER\t')) continue
          const [, username = '', host = '', locked = 'N', privilegeText = '', management = 'N'] = line.split('\t')
          rows.push({ username, host, locked: String(locked).toUpperCase() === 'Y', management: String(management).toUpperCase() === 'Y', privileges: privilegeText ? privilegeText.split(',').filter(Boolean) : [] })
        }
      }
      mysqlUsers.value = rows
      usersLoaded.value = true
      return rows
    }
    const refreshMySQLUsers = () => run(async () => {
      const rows = await loadMySQLUsersData()
      notice.value = `已从主节点同步 ${rows.length} 个 MySQL 用户`
    })
    const openCreateUser = () => { userDialog.value = { open: true, mode: 'create', username: '', host: '%', password: '', privileges: ['SELECT'] } }
    const openUserPrivileges = user => {
      if (user.management) { notice.value = '当前 MHA 管理账号权限由安装策略维护，不能在此处削弱'; return }
      userDialog.value = { open: true, mode: 'permissions', username: user.username, host: user.host, password: '', privileges: [...user.privileges] }
    }
    const closeUserDialog = () => { userDialog.value = { open: false, mode: 'create', username: '', host: '%', password: '', privileges: [] } }
    const saveMySQLUser = () => run(async () => {
      const form = userDialog.value
      if (!form.username.trim() || !form.host.trim()) throw new Error('用户名和来源 Host 不能为空')
      if (form.mode === 'create') {
        if (!form.privileges.length) throw new Error('请至少选择一项权限')
        if (!form.password) throw new Error('新用户密码不能为空')
        await dispatchUserAction('create', form, form.privileges, form.password)
        notice.value = `用户 ${form.username}@${form.host} 已创建`
      } else {
        const current = mysqlUsers.value.find(item => item.username === form.username && item.host === form.host)?.privileges || []
        const grants = form.privileges.filter(item => !current.includes(item))
        const revokes = current.filter(item => !form.privileges.includes(item))
        if (grants.length) await dispatchUserAction('grant', form, grants)
        if (revokes.length) await dispatchUserAction('revoke', form, revokes)
        notice.value = grants.length || revokes.length ? `用户 ${form.username}@${form.host} 的权限已更新` : '权限没有变化'
      }
      closeUserDialog()
      await loadMySQLUsersData()
    })
    const requestDeleteUser = user => {
      if (user.management) { error.value = '不能删除当前 MHA 管理账号'; return }
      deleteUserDialog.value = { open: true, user, confirmation: '' }
    }
    const toggleMySQLUserLock = user => run(async () => {
      if (user.management) throw new Error('不能锁定当前 MHA 管理账号')
      await dispatchUserAction(user.locked ? 'unlock' : 'lock', user)
      await loadMySQLUsersData()
      notice.value = `用户 ${user.username}@${user.host} 已${user.locked ? '解锁' : '锁定'}`
    })
    const confirmDeleteUser = () => run(async () => {
      const user = deleteUserDialog.value.user
      if (!user || deleteUserDialog.value.confirmation !== `${user.username}@${user.host}`) throw new Error('请输入完整用户标识以确认删除')
      if (user.management) throw new Error('不能删除当前 MHA 管理账号')
      await dispatchUserAction('delete', user)
      deleteUserDialog.value = { open: false, user: null, confirmation: '' }
      await loadMySQLUsersData()
      notice.value = `用户 ${user.username}@${user.host} 已删除`
    })
    const primaryUserEndpoint = computed(() => primaryInstance.value ? `${value(primaryInstance.value, 'MachineIP', 'machine_ip')}:${value(primaryInstance.value, 'Port', 'port')}` : '')
    watch([view, primaryUserEndpoint], ([nextView, endpoint], previous = []) => {
      if (endpoint !== previous[1]) usersLoaded.value = false
      if (nextView === 'users' && endpoint && !usersLoaded.value && !busy.value) refreshMySQLUsers()
    }, { immediate: true })
    const selectedUpgradeInstance = computed(() => {
      const [ip, port] = upgrade.value.instance.split(':'); const instance = clusterInstances.value.find(item => value(item, 'MachineIP', 'machine_ip') === ip && String(value(item, 'Port', 'port')) === port)
      return instance
    })
    const upgradeArchitectures = computed(() => [...new Set(props.packages.map(pkg => normalizeArchitecture(pkg.arch || pkg.Arch)).filter(Boolean))])
    const upgradeCandidateVersions = computed(() => {
      const current = value(selectedUpgradeInstance.value, 'Version', 'version')
      return [...new Map(props.packages.filter(pkg => (!upgrade.value.architecture || normalizeArchitecture(pkg.arch || pkg.Arch) === normalizeArchitecture(upgrade.value.architecture)) && (!current || compareMySQLVersions(pkg.version || pkg.Version, current) > 0)).map(pkg => [pkg.version || pkg.Version, pkg])).values()]
    })
    const upgradeVersions = computed(() => {
      const current = value(selectedUpgradeInstance.value, 'Version', 'version')
      if (!current) return upgradeCandidateVersions.value
      const parsed = parseMySQLVersion(current)
      if (parsed?.major === 5 && parsed.minor === 7 && compareMySQLVersions(current, mysql57UpgradeBridgeVersion) < 0) {
        return upgradeCandidateVersions.value.filter(pkg => (pkg.version || pkg.Version) === mysql57UpgradeBridgeVersion)
      }
      return upgradeCandidateVersions.value.filter(pkg => mysqlDirectUpgradeDecision(current, pkg.version || pkg.Version).allowed)
    })
    const upgradeRoute = computed(() => {
      const current = value(selectedUpgradeInstance.value, 'Version', 'version')
      const parsed = parseMySQLVersion(current)
      if (!parsed) return null
      const blocked = upgradeCandidateVersions.value.filter(pkg => !mysqlDirectUpgradeDecision(current, pkg.version || pkg.Version).allowed)
      if (parsed.major === 5 && parsed.minor === 7 && compareMySQLVersions(current, mysql57UpgradeBridgeVersion) < 0) {
        const bridgeAvailable = upgradeVersions.value.some(pkg => (pkg.version || pkg.Version) === mysql57UpgradeBridgeVersion)
        return { kind: bridgeAvailable ? 'bridge' : 'missing', title: `必须先升级到 MySQL ${mysql57UpgradeBridgeVersion}`, detail: bridgeAvailable ? `当前 MySQL ${current} 不能直接进入 8.0。请先完成 ${current} → ${mysql57UpgradeBridgeVersion}，成功并刷新实例版本后，再创建 5.7.44 → 8.0 升级。` : `当前 MySQL ${current} 不能直接进入 8.0，且软件包仓库中缺少 ${mysql57UpgradeBridgeVersion} 的同架构安装包。请先上传桥接版本。`, blocked: blocked.length }
      }
      if (parsed.major === 5 && parsed.minor === 7) return { kind: 'next', title: '5.7 高版本检查已满足', detail: '当前可选择 MySQL 8.0 作为下一阶段；8.4 和 9.x 会继续被拦截，避免跨过必要的数据字典升级。', blocked: blocked.length }
      return blocked.length ? { kind: 'next', title: '已按安全升级路径筛选', detail: `${blocked.length} 个不能直接到达的高版本已隐藏，请完成当前阶段后再选择后续版本。`, blocked: blocked.length } : null
    })
    const upgradePreview = computed(() => {
      const instance = selectedUpgradeInstance.value
      const pkg = props.packages.find(item => (item.version || item.Version) === upgrade.value.version && normalizeArchitecture(item.arch || item.Arch) === normalizeArchitecture(upgrade.value.architecture))
      return { instance, pkg, ready: !!instance && !!pkg }
    })
    const resetUpgradePrecheck = () => { upgradePrecheck.value = { status: 'idle', taskID: '', report: '', checker: '', checkedAt: '' }; upgrade.value.force = false }
    const upgradeInstanceChanged = () => {
      const instance = selectedUpgradeInstance.value
      upgrade.value.architecture = normalizeArchitecture(value(instance, 'Architecture', 'architecture')) || upgrade.value.architecture || upgradeArchitectures.value[0] || ''
      if (!upgradeVersions.value.some(pkg => (pkg.version || pkg.Version) === upgrade.value.version)) upgrade.value.version = upgradeVersions.value[0]?.version || ''
    }
    const upgradeArchitectureChanged = () => {
      if (!upgradeVersions.value.some(pkg => (pkg.version || pkg.Version) === upgrade.value.version)) upgrade.value.version = upgradeVersions.value[0]?.version || ''
    }
    watch(() => [upgrade.value.instance, upgrade.value.architecture, upgrade.value.version], resetUpgradePrecheck)
    const upgradeStages = ['执行前安全复核','暂停写入并等待事务结束','下载并校验安装包','解压目标版本','停止数据库','原子切换软链接','校验配置','启动并升级数据字典','完整性与复制检查','恢复业务访问']
    const upgradeCanStart = computed(() => upgradePreview.value.ready && upgrade.value.risk_acknowledged && (upgradePrecheck.value.status === 'success' || upgrade.value.force))
    const upgradeReportText = detail => (detail?.events || []).map(event => String(value(event,'Content','content') || '').trim()).filter(Boolean).join('\n\n') || (detail?.steps || []).map(step => `${value(step,'StepName','step_name')}: ${value(step,'Message','message') || ''}`).join('\n')
    const runUpgradePrecheck = () => run(async () => {
      if (!upgradePreview.value.ready) throw new Error('请先选择实例和目标版本')
      upgradePrecheck.value = { status: 'running', taskID: '', report: '', checker: '', checkedAt: '' }
      const [machine, port] = upgrade.value.instance.split(':')
      let plan
      try { plan = await request('/tasks/mysql-upgrade/precheck', { method: 'POST', body: JSON.stringify({ machine, port: Number(port), package_name: value(upgradePreview.value.pkg,'FileName','file_name') }) }) }
      catch (err) { upgradePrecheck.value.status = 'failed'; upgradePrecheck.value.report = `预检任务无法创建：${err.message}`; upgradePrecheck.value.checkedAt = new Date().toLocaleString('zh-CN', { hour12: false }); throw err }
      const taskID = value(plan.task?.task, 'ID', 'id')
      upgradePrecheck.value.taskID = taskID
      upgradePrecheck.value.checker = plan.checker || 'MySQL 官方升级检查工具'
      const detail = await waitTask(taskID)
      const status = lower(value(detail.task, 'Status', 'status'))
      upgradePrecheck.value.status = status === 'success' ? 'success' : 'failed'
      upgradePrecheck.value.report = upgradeReportText(detail)
      upgradePrecheck.value.checkedAt = new Date().toLocaleString('zh-CN', { hour12: false })
      if (status === 'success') notice.value = `预检通过：MySQL ${plan.current_version} → ${plan.target_version}`
      else error.value = `预检未通过：${parameterTaskFailure(detail)}`
    })
    const startUpgrade = () => run(async () => {
      if (!upgradeCanStart.value) throw new Error('请先完成预检并确认升级风险；预检未通过时只能显式选择强制升级')
      if (!confirm(`确认升级 ${upgrade.value.instance}？\n升级期间会暂停写入并停止服务。新版本一旦启动，系统不会冒险用旧二进制打开已升级的数据目录。${upgrade.value.force ? '\n\n当前选择了强制升级，将绕过预检门禁。' : ''}`)) return
      const [machine, port] = upgrade.value.instance.split(':')
      const plan = await request('/tasks/mysql-upgrade', { method: 'POST', body: JSON.stringify({ machine, port: Number(port), package_name: value(upgradePreview.value.pkg,'FileName','file_name'), precheck_task_id: upgradePrecheck.value.taskID, force: upgrade.value.force, risk_acknowledged: upgrade.value.risk_acknowledged }) })
      notice.value = `升级任务已创建：MySQL ${plan.current_version} → ${plan.target_version}${plan.forced ? '（强制）' : ''}`; emit('open-task', plan.task)
    })
    let refreshTimer
    onMounted(() => {
      refreshInstances(false)
      if (view.value === 'agent-collect') loadParams()
      refreshTimer = setInterval(() => { if (!busy.value) refreshInstances(false) }, 5000)
    })
    onUnmounted(() => { clearInterval(refreshTimer); clearInstallRootPasswords(); userDialog.value.password = '' })
    return { view, busy, error, notice, clusterName, clusterInstances, failures, instancesRefreshing, instancesUpdatedAt, refreshInstances, instanceKeyword, instanceStatus, instancePage, instancePageSize, instancePageCount, filteredInstances, pagedInstances, expandedInstance, instanceKey, instanceHealthCode, instanceHealthLabel, toggleInstanceDetails, lifecycleDialog, lifecycleEndpoint, lifecycleConfirmation, lifecycleIsPrimary, lifecycleCanSubmit, openLifecycle, submitLifecycle, selectedMachines, selectedTopologyMachines, targetConfigs, install, showInstallRootPassword, targetRootPasswordVisibility, targetBaseConfig, isTargetRootPasswordVisible, toggleTargetRootPassword, toggleIndependentBaseConfig, vipInterfaceOptions, vipInterfaceLoading, vipInterfaceError, vipInterfaceLabel, scanVIPInterfaces, directoryFields, installStages, mysqlPrivilegeOptions, mysqlInstallPrivilegeOptions, mysqlUserPrivilegeOptions, presetAccounts, presetAccountsDirty, markPresetAccountsDirty, saveAccountPresets, selectedTargetIssues, collectConfig, paramKeyword, paramCategory, paramCategories, parameterGroups, filteredParams, liveParameters, parametersCollected, collectedParameterCount, parameterAuth, parameterTask, selectedParameterInstance, parameterChanges, parameterApplyScope, parameterRestartDialog, parameterDynamicCount, parameterRestartCount, stagedDynamicCount, stagedRestartCount, collectParameters, exportParameters, stageParameter, isParameterChanged, removeParameterChange, applyParameterChanges, confirmParameterRestart, mysqlUsers, usersLoaded, userKeyword, userPage, userPageCount, pagedMySQLUsers, userDialog, deleteUserDialog, primaryInstance, filteredMySQLUsers, refreshMySQLUsers, openCreateUser, openUserPrivileges, closeUserDialog, saveMySQLUser, toggleMySQLUserLock, requestDeleteUser, confirmDeleteUser, upgrade, upgradePrecheck, upgradeCanStart, upgradePreview, upgradeRoute, upgradeArchitectures, upgradeVersions, upgradeInstanceChanged, upgradeArchitectureChanged, upgradeStages, runUpgradePrecheck, startUpgrade, compatiblePackages, architecturesFor, versionsFor, targetArchitectureChanged, ensureTarget, parameterGroupsFor, addCustomAccount, removeCustomAccount, isCustomAccount, createInstallTasks, uninstall, forget, loadParams, saveParams, roleFor, emit, value, summary }
  },
  template: `
    <section class="instance-management-page">
      <header class="instance-page-head"><div><p>INSTANCE MANAGEMENT</p><h2>{{ clusterName }} · 实例管理</h2><span>统一管理 MySQL 实例生命周期、用户账号、运行参数与版本升级。</span></div><button class="secondary" @click="emit('close')">返回集群概览</button></header>
      <nav class="instance-tabs"><button v-for="item in [['instances','实例'],['inspection','数据库巡检'],['execution-plan','执行计划'],['online-ddl','在线 DDL'],['indexes','索引管理'],['histograms','直方图'],['archive','数据归档'],['binlog-analysis','binlog分析'],['install','创建安装'],['users','用户管理'],['accounts','预设账号'],['params','参数管理'],['agent-collect','Agent采集'],['upgrade','版本升级']]" :key="item[0]" :class="{active:view===item[0]}" @click="view=item[0]; item[0]==='agent-collect'&&loadParams()">{{ item[1] }}</button></nav>
      <div v-if="error" class="alert error"><b>操作未完成</b><span>{{ summary(error) }}</span><small>完整执行日志请在任务中心对应任务详情中查看。</small></div><div v-if="notice" class="alert success"><span>{{ summary(notice,240) }}</span></div>
      <main v-if="view==='instances'" class="instance-panel instance-catalog">
        <div class="panel-head instance-catalog-head"><div><h3>实例列表</h3><p>集中查看实例状态与关键配置；展开详情可检查目录、运行文件和最近任务。</p></div><div class="instance-list-actions"><small>更新于 {{ instancesUpdatedAt || '—' }}</small><button class="secondary" :disabled="instancesRefreshing" @click="refreshInstances(true)">{{ instancesRefreshing ? '刷新中…' : '刷新' }}</button><button class="primary" @click="view='install'">＋ 创建实例</button></div></div>
        <div class="instance-catalog-toolbar">
          <label class="instance-search"><span>⌕</span><input v-model.trim="instanceKeyword" placeholder="搜索机器、IP、端口、server_id、版本或 Profile"></label>
          <label>运行状态<select v-model="instanceStatus"><option value="all">全部状态</option><option value="healthy">运行正常</option><option value="stopped">已安全关闭</option><option value="error">状态异常</option><option value="unknown">等待上报</option></select></label>
          <label>每页<select v-model.number="instancePageSize"><option :value="10">10 条</option><option :value="20">20 条</option><option :value="50">50 条</option></select></label>
          <span class="instance-result-count">显示 {{ pagedInstances.length }} / {{ filteredInstances.length }} 个实例</span>
        </div>
        <div class="instance-table-wrap"><table class="instance-table"><thead><tr><th>实例</th><th>地址 / 标识</th><th>版本与架构</th><th>配置</th><th>健康状态</th><th>最近更新</th><th>操作</th></tr></thead><tbody>
          <template v-for="item in pagedInstances" :key="instanceKey(item)">
            <tr :class="['instance-row',{expanded:expandedInstance===instanceKey(item)}]" @click="toggleInstanceDetails(item)">
              <td class="instance-identity"><span :class="['instance-health-dot',instanceHealthCode(item)]"></span><div><b>{{ value(item,'MachineName','machine_name') || '未命名机器' }}</b><small>{{ value(item,'MachineID','machine_id') || '机器 ID 待上报' }}</small></div></td>
              <td><b class="instance-endpoint">{{ value(item,'MachineIP','machine_ip') || '—' }}:{{ value(item,'Port','port') || '—' }}</b><small>server_id {{ value(item,'ServerID','server_id') || '—' }}</small></td>
              <td><b>{{ value(item,'Version','version') || '版本待上报' }}</b><small>{{ value(item,'Architecture','architecture') || '架构待上报' }} · {{ roleFor(item) }}</small></td>
              <td><b>{{ value(item,'Profile','profile') || 'default' }}</b><small>用户 {{ value(item,'MySQLUser','mysql_user') || 'mysql' }}</small></td>
              <td><span :class="['instance-health-badge',instanceHealthCode(item)]"><i></i>{{ instanceHealthLabel(item) }}</span><small :title="value(item,'HeartbeatDetail','heartbeat_detail')">{{ summary(value(item,'HeartbeatDetail','heartbeat_detail') || value(item,'Status','status') || '尚无心跳详情', 36) }}</small></td>
              <td><b>{{ value(item,'HeartbeatCheckedAt','heartbeat_checked_at') || '—' }}</b><small>登记 {{ value(item,'UpdatedAt','updated_at') ? new Date(value(item,'UpdatedAt','updated_at')).toLocaleString('zh-CN',{hour12:false}) : '—' }}</small></td>
              <td class="instance-row-actions" @click.stop><button class="text-button" @click="toggleInstanceDetails(item)">{{ expandedInstance===instanceKey(item) ? '收起' : '详情' }}</button><button class="text-button lifecycle-restart" @click="openLifecycle(item,'restart')">重启</button><button class="danger-link lifecycle-shutdown" @click="openLifecycle(item,'shutdown')">关机</button><button class="danger-link" @click="uninstall(item)">卸载</button><button class="text-button muted" @click="forget(item)">遗忘</button></td>
            </tr>
            <tr v-if="expandedInstance===instanceKey(item)" class="instance-detail-row"><td colspan="7"><div class="instance-detail-shell">
              <section><header><b>实例标识</b><span>{{ instanceHealthLabel(item) }}</span></header><dl><div><dt>集群</dt><dd>{{ value(item,'Cluster','cluster') || clusterName }}</dd></div><div><dt>机器名称</dt><dd>{{ value(item,'MachineName','machine_name') || '—' }}</dd></div><div><dt>机器 ID</dt><dd>{{ value(item,'MachineID','machine_id') || '—' }}</dd></div><div><dt>访问地址</dt><dd>{{ value(item,'MachineIP','machine_ip') || '—' }}:{{ value(item,'Port','port') || '—' }}</dd></div><div><dt>server_id</dt><dd>{{ value(item,'ServerID','server_id') || '—' }}</dd></div><div><dt>MySQL 用户</dt><dd>{{ value(item,'MySQLUser','mysql_user') || 'mysql' }}</dd></div></dl></section>
              <section><header><b>软件与运行</b><span>{{ roleFor(item) }}</span></header><dl><div><dt>版本</dt><dd>{{ value(item,'Version','version') || '—' }}</dd></div><div class="wide"><dt>安装包</dt><dd>{{ value(item,'PackageName','package_name') || '—' }}</dd></div><div><dt>CPU 架构</dt><dd>{{ value(item,'Architecture','architecture') || '—' }}</dd></div><div><dt>参数 Profile</dt><dd>{{ value(item,'Profile','profile') || 'default' }}</dd></div><div class="wide"><dt>systemd 服务</dt><dd>{{ value(item,'SystemdUnit','systemd_unit') || '—' }}</dd></div></dl></section>
              <section class="instance-path-section"><header><b>目录与配置文件</b><span>部署路径</span></header><dl><div><dt>实例目录</dt><dd>{{ value(item,'InstanceDir','instance_dir') || '—' }}</dd></div><div><dt>安装目录</dt><dd>{{ value(item,'BaseDir','base_dir') || '—' }}</dd></div><div><dt>数据目录</dt><dd>{{ value(item,'DataDir','data_dir') || '—' }}</dd></div><div><dt>binlog</dt><dd>{{ value(item,'BinlogDir','binlog_dir') || '—' }}</dd></div><div><dt>redo</dt><dd>{{ value(item,'RedoDir','redo_dir') || '—' }}</dd></div><div><dt>undo</dt><dd>{{ value(item,'UndoDir','undo_dir') || '—' }}</dd></div><div><dt>tmp</dt><dd>{{ value(item,'TmpDir','tmp_dir') || '—' }}</dd></div><div><dt>my.cnf</dt><dd>{{ value(item,'MyCnfPath','my_cnf_path') || '—' }}</dd></div><div><dt>Socket</dt><dd>{{ value(item,'SocketPath','socket_path') || '—' }}</dd></div></dl></section>
              <section><header><b>状态与追踪</b><span>Manager 记录</span></header><dl><div><dt>登记状态</dt><dd>{{ value(item,'Status','status') || '—' }}</dd></div><div><dt>心跳状态</dt><dd>{{ value(item,'HeartbeatStatus','heartbeat_status') || '—' }}</dd></div><div class="wide"><dt>心跳详情</dt><dd>{{ value(item,'HeartbeatDetail','heartbeat_detail') || '—' }}</dd></div><div><dt>心跳时间</dt><dd>{{ value(item,'HeartbeatCheckedAt','heartbeat_checked_at') || '—' }}</dd></div><div class="wide"><dt>最近任务</dt><dd>{{ value(item,'LastTaskID','last_task_id') || '—' }}</dd></div></dl></section>
            </div></td></tr>
          </template>
          <tr v-if="!pagedInstances.length"><td colspan="7" class="empty">{{ clusterInstances.length ? '没有匹配筛选条件的实例。' : '当前集群暂无 MySQL 实例；页面会持续自动检查安装结果。' }}</td></tr>
        </tbody></table></div>
        <footer v-if="filteredInstances.length" class="instance-pager"><span>第 {{ instancePage }} / {{ instancePageCount }} 页</span><div><button class="secondary" :disabled="instancePage<=1" @click="instancePage--">上一页</button><button class="secondary" :disabled="instancePage>=instancePageCount" @click="instancePage++">下一页</button></div></footer>
      </main>
      <DatabaseInspection v-else-if="view==='inspection'" :instances="clusterInstances" :cluster-name="clusterName" @open-task="emit('open-task',$event)" />
      <ExecutionPlan v-else-if="view==='execution-plan'" :instances="clusterInstances" :cluster-name="clusterName" />
      <OnlineDDLManagement v-else-if="view==='online-ddl'" :instances="clusterInstances" @open-task="emit('open-task',$event)" />
      <IndexManagement v-else-if="view==='indexes'" :instances="clusterInstances" @open-task="emit('open-task',$event)" />
      <HistogramManagement v-else-if="view==='histograms'" :instances="clusterInstances" />
      <ArchiveManagement v-else-if="view==='archive'" :instances="clusterInstances" @open-task="emit('open-task',$event)" />
      <BinlogAnalysis v-else-if="view==='binlog-analysis'" :instances="clusterInstances" :cluster-name="clusterName" />
      <main v-else-if="view==='install'" class="instance-install-page">
        <section class="instance-panel install-target-panel"><div class="panel-head"><div><h3>1. 目标机器、版本与架构</h3><p>版本和 CPU 架构独立选择；后端再按目标机器 glibc 自动确定具体安装包。</p></div><span class="count">已选 {{ selectedMachines.length }} 台</span></div><div class="install-table-wrap"><table><thead><tr><th>选择</th><th>机器</th><th>目标架构</th><th>MySQL 版本</th><th>端口</th><th>server_id</th></tr></thead><tbody><tr v-for="(machine,index) in machines" :key="value(machine,'ID','id')"><td><input v-model="selectedMachines" type="checkbox" :value="value(machine,'ID','id')"></td><td><b>{{ value(machine,'Name','name') }}</b><small>{{ value(machine,'IP','ip') }}</small></td><td><select v-model="ensureTarget(machine,index).architecture" @change="targetArchitectureChanged(machine,index)"><option v-for="arch in architecturesFor(machine)" :key="arch" :value="arch">{{ arch }}</option></select></td><td><select v-model="ensureTarget(machine,index).version"><option v-for="pkg in versionsFor(machine,ensureTarget(machine,index))" :key="pkg.version" :value="pkg.version">{{ pkg.version }} · {{ pkg.release_track }}</option></select><small>具体制品按 glibc 自动匹配</small></td><td><input v-model.number="ensureTarget(machine,index).port" type="number" min="1" max="65535"></td><td><input v-model.number="ensureTarget(machine,index).server_id" type="number" min="1"></td></tr><tr v-if="!machines.length"><td colspan="6" class="empty">当前集群没有可用机器，请先在机器管理中添加并部署 Agent。</td></tr></tbody></table></div></section>
        <section class="instance-panel install-common-panel"><div class="panel-head"><div><h3>2. 基础配置与目录</h3><p>{{ install.independent_base_config ? '每台目标机器分别设置 root 密码、运行用户、Profile 与目录。' : '默认所有目标共用一套配置；目录留空时由 Profile 按端口自动生成。' }}</p></div><label class="independent-base-switch"><input v-model="install.independent_base_config" type="checkbox" @change="toggleIndependentBaseConfig"><span>每台机器独立配置</span></label></div><div v-if="!install.independent_base_config" class="shared-root-auth"><label>公共 root 密码<div class="password-input-control"><input v-model="install.root_password" :type="showInstallRootPassword ? 'text' : 'password'" autocomplete="new-password" required><button type="button" class="password-visibility-toggle" :aria-label="showInstallRootPassword ? '隐藏 root 密码' : '查看 root 密码'" :aria-pressed="showInstallRootPassword" @click="showInstallRootPassword=!showInstallRootPassword">{{ showInstallRootPassword ? '隐藏' : '查看' }}</button></div><small>共享配置模式下，所有目标实例统一使用该密码，且不会写入任务日志。</small></label></div><div v-if="!install.independent_base_config" class="instance-install-fields"><label>MySQL 运行用户<input v-model.trim="install.mysql_user" required></label><label>参数 Profile<input v-model.trim="install.profile" required></label><label v-for="field in directoryFields" :key="field[0]">{{ field[1] }}<input v-model.trim="install[field[0]]" :placeholder="field[2]"></label></div><div v-else class="independent-base-list"><p v-if="!selectedTopologyMachines.length" class="install-empty-hint">请先选择目标机器，再分别配置 root 密码、基础信息与目录。</p><article v-for="(machine,index) in selectedTopologyMachines" :key="value(machine,'ID','id')"><header><div><b>{{ value(machine,'Name','name') }}</b><small>{{ value(machine,'IP','ip') }}</small></div><span>独立配置</span></header><div class="instance-install-fields"><label class="independent-root-password">root 密码<div class="password-input-control"><input v-model="targetBaseConfig(machine,index).root_password" :type="isTargetRootPasswordVisible(machine) ? 'text' : 'password'" autocomplete="new-password" required><button type="button" class="password-visibility-toggle" :aria-label="isTargetRootPasswordVisible(machine) ? '隐藏 '+value(machine,'Name','name')+' 的 root 密码' : '查看 '+value(machine,'Name','name')+' 的 root 密码'" :aria-pressed="isTargetRootPasswordVisible(machine)" @click="toggleTargetRootPassword(machine)">{{ isTargetRootPasswordVisible(machine) ? '隐藏' : '查看' }}</button></div></label><label>MySQL 运行用户<input v-model.trim="targetBaseConfig(machine,index).mysql_user" required></label><label>参数 Profile<input v-model.trim="targetBaseConfig(machine,index).profile" required></label><label v-for="field in directoryFields" :key="field[0]">{{ field[1] }}<input v-model.trim="targetBaseConfig(machine,index)[field[0]]" :placeholder="field[2]"></label></div></article></div></section>
        <details class="instance-panel install-runtime-panel"><summary class="install-runtime-summary"><div><h3>3. MySQL 运行参数</h3><p>默认折叠；展开后可按目标实例覆盖 Profile 自动计算结果。</p></div><span>运行参数</span></summary><div class="install-runtime-content"><p v-if="!targetConfigs._selected_install_targets.length" class="install-empty-hint">选择机器后可配置各目标实例参数。</p><div class="target-version-parameters"><article v-for="target in targetConfigs._selected_install_targets" :key="value(target.machine,'ID','id')"><header><b>{{ value(target.machine,'Name','name') }} · MySQL {{ target.pkg?.version || '未选择版本' }}</b><small>{{ target.pkg?.release_track || value(target.machine,'IP','ip') }}</small></header><section v-for="group in parameterGroupsFor(target)" :key="group.name"><h4>{{ group.name }}</h4><div><label v-for="field in group.fields" :key="field.key">{{ field.label }}<select v-if="field.options" v-model="target.config.runtime_parameters[field.key]"><option value="">自动</option><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select><input v-else v-model.trim="target.config.runtime_parameters[field.key]" :placeholder="field.placeholder || (field.default ? '默认 '+field.default : '自动计算')"><small v-if="field.description || field.default">{{ field.description || ('建议值 '+field.default) }}</small></label></div></section></article></div></div></details>
        <section class="instance-panel install-topology-panel"><div class="panel-head"><div><h3>4. 安装后架构初始化</h3><p>Manager 会等待全部实例安装成功，再使用下方 MHA 管理账号配置复制关系与 VIP；完整流程统一进入任务中心。</p></div><label class="topology-master-switch"><input v-model="install.configure_topology" type="checkbox"><span>安装完成后自动配置</span></label></div><template v-if="install.configure_topology"><div :class="['topology-choice-grid',{'dual-master':install.architecture==='dual_master'}]"><label>目标架构<select v-model="install.architecture"><option value="master_slave">一主多从</option><option value="dual_master">双主架构</option></select><small>其余选中机器自动作为从库跟随首选主库</small></label><label>首选主库<select v-model="install.primary_machine_id"><option value="">选择主库机器</option><option v-for="machine in selectedTopologyMachines" :key="value(machine,'ID','id')" :value="value(machine,'ID','id')">{{ value(machine,'Name','name') }} · {{ value(machine,'IP','ip') }}</option></select><small>复制链路默认使用 MHA 管理账号</small></label><label v-if="install.architecture==='dual_master'">第二主库<select v-model="install.secondary_master_machine_id"><option value="">选择第二主库</option><option v-for="machine in selectedTopologyMachines.filter(item=>value(item,'ID','id')!==install.primary_machine_id)" :key="value(machine,'ID','id')" :value="value(machine,'ID','id')">{{ value(machine,'Name','name') }} · {{ value(machine,'IP','ip') }}</option></select><small>与首选主库建立双向复制</small></label></div><label class="install-vip-switch"><input v-model="install.enable_vip" type="checkbox"><span><b>开启业务 VIP</b><small>系统自动完成 ARP 或集群 BGP 宣告，用户无需选择模式</small></span></label><div v-if="install.enable_vip" class="vip-install-grid"><label>VIP 名称<input v-model.trim="install.vip.vip_name"></label><label>VIP 地址<input v-model.trim="install.vip.vip_address" placeholder="例如 10.0.0.100"></label><label>网络前缀<input v-model.number="install.vip.vip_prefix" type="number" min="1" max="32"></label><label class="vip-interface-field">目标业务网卡<div><select v-model="install.vip.default_interface"><option value="">{{ vipInterfaceLoading ? '正在扫描目标主机…' : '选择目标网卡' }}</option><option v-for="item in vipInterfaceOptions" :key="item.name" :value="item.name">{{ vipInterfaceLabel(item) }}</option></select><button type="button" class="secondary" :disabled="vipInterfaceLoading || !selectedTopologyMachines.length" @click="scanVIPInterfaces(true)">{{ vipInterfaceLoading ? '扫描中…' : '重新扫描' }}</button></div><small v-if="vipInterfaceError" class="vip-interface-error">{{ vipInterfaceError }}</small><small v-else>推荐项优先覆盖全部目标机器，并匹配主机管理地址所在网段</small></label><div class="vip-auto-mode"><b>宣告策略由系统自动完成</b><span>同网段自动使用免费 ARP；集群配置为三层网络时自动复用 BGP 邻居策略。</span></div></div></template></section>
        <section class="instance-panel install-account-panel"><div class="panel-head"><div><h3>5. 初始化账号与权限</h3><p>权限会按每台目标的 MySQL 版本过滤；5.7 与早期 8.0 使用 SUPER 等静态权限，其余版本使用对应动态权限。</p></div><button type="button" class="secondary" @click="addCustomAccount">＋ 自定义用户</button></div><div class="cluster-install-accounts"><article v-for="(account,index) in install.accounts" :key="account.role"><header><div><b>{{ isCustomAccount(account) ? '自定义数据库用户' : ({monitor:'监控账号',mha:'MHA 管理账号',backup:'备份账号'})[account.role] || account.role }}</b><small>{{ account.role }}</small></div><button v-if="isCustomAccount(account)" type="button" class="danger-link" @click="removeCustomAccount(index)">删除</button><label v-else><input v-model="account.enabled" type="checkbox"> 启用</label></header><div class="account-inputs"><label>用户名<input v-model.trim="account.username" :placeholder="account.role"></label><label>密码<input v-model="account.password" type="password" autocomplete="new-password" placeholder="使用预设或输入密码"></label><label>访问白名单<input v-model.trim="account.host" placeholder="默认 %"></label></div><div class="cluster-privileges"><label v-for="privilege in mysqlInstallPrivilegeOptions" :key="privilege"><input v-model="account.privileges" type="checkbox" :value="privilege"><span>{{ privilege }}</span></label></div></article></div></section>
        <aside class="instance-panel install-submit-panel">
          <header class="install-confirm-head">
            <i>06</i>
            <div>
              <h3>确认并安装</h3>
              <p>{{ install.configure_topology ? 'Manager 将按安装、配置架构、应用 VIP 和验证的顺序执行。' : '任务将并发创建，每台机器独立执行，单台失败不会中断其他目标。' }}</p>
            </div>
          </header>
          <section class="install-confirm-section">
            <div class="install-confirm-label"><b>执行流程</b><small>按顺序自动完成</small></div>
            <ol class="install-flow-preview">
              <li v-for="(stage,index) in installStages" :key="stage"><i>{{ index+1 }}</i><span>{{ stage }}</span></li>
              <li v-if="install.configure_topology"><i>7</i><span>配置{{ install.architecture==='dual_master' ? '双主' : '一主多从' }}复制拓扑</span></li>
              <li v-if="install.configure_topology&&install.enable_vip"><i>8</i><span>绑定并验证 VIP {{ install.vip.vip_address || '' }}</span></li>
            </ol>
          </section>
          <section class="install-confirm-section install-extra-section">
            <div class="install-confirm-label"><b>附加配置</b><small>按需选择</small></div>
            <div class="install-extra-options">
              <label class="install-choice-card">
                <input v-model="install.install_pt_tools" type="checkbox" :disabled="!targetConfigs._selected_install_targets.length || targetConfigs._selected_install_targets.some(target=>target.pkg && !target.pkg.pt_tools_supported)">
                <span><b>安装 Percona Toolkit</b><small>根据每台机器的架构和 MySQL 版本自动匹配</small></span>
              </label>
              <label class="install-choice-card">
                <input v-model="install.install_xtrabackup" type="checkbox">
                <span><b>安装 Percona XtraBackup</b><small>匹配主版本、架构和 glibc，并补充备份权限</small></span>
              </label>
              <label class="install-choice-card install-allocator-option">
                <span><b>内存分配器</b><small>建议保持系统默认；tcmalloc 需额外验证</small></span>
                <select v-model="install.memory_allocator"><option value="system">系统默认（推荐）</option><option value="tcmalloc">tcmalloc（需额外验证）</option></select>
              </label>
            </div>
          </section>
          <section v-if="selectedTargetIssues.length" class="install-validation">
            <header><span><b>配置尚未完成</b><small>处理后即可提交</small></span><i>{{ selectedTargetIssues.length }}</i></header>
            <ul><li v-for="issue in selectedTargetIssues" :key="issue">{{ issue }}</li></ul>
          </section>
          <footer class="install-submit-footer">
            <div class="install-submit-summary">
              <span><small>目标机器</small><b>{{ selectedMachines.length }} 台</b></span>
              <span><small>执行模式</small><b>{{ install.configure_topology ? '组合流程' : '仅安装' }}</b></span>
            </div>
            <button type="button" class="primary" :disabled="busy" @click="createInstallTasks">{{ busy ? '正在创建…' : (install.configure_topology ? '创建安装与架构任务' : '并发创建安装任务') }}</button>
            <small>提交后可在任务中心查看安装子任务及架构执行日志。</small>
          </footer>
        </aside>
      </main>
      <main v-else-if="view==='users'" class="mysql-user-page">
        <section class="user-hero"><div><p>MYSQL ACCESS CONTROL</p><h3>用户管理</h3><span>进入页面后自动读取主节点用户；查询和变更都不会发送到从节点。</span></div><div class="user-hero-node"><small>当前主节点</small><b>{{ primaryInstance ? value(primaryInstance,'MachineName','machine_name') || value(primaryInstance,'MachineIP','machine_ip') : '未识别' }}</b><code v-if="primaryInstance">{{ value(primaryInstance,'MachineIP','machine_ip') }}:{{ value(primaryInstance,'Port','port') }}</code><em>{{ usersLoaded ? mysqlUsers.length+' 个用户已同步' : busy ? '正在同步用户' : '等待主节点就绪' }}</em><button class="text-button user-hero-refresh" :disabled="busy || !primaryInstance" @click="refreshMySQLUsers">{{ busy ? '同步中…' : '刷新' }}</button></div></section>
        <section class="instance-panel user-catalog"><div class="panel-head user-catalog-head"><div><h3>主节点用户与权限</h3><p>每页最多显示 8 个用户；点击用户名可编辑权限，Host 列表示允许访问的来源地址。</p></div><button class="primary" :disabled="busy || !usersLoaded" @click="openCreateUser">＋ 新增用户</button></div><div class="user-toolbar"><label><span>⌕</span><input v-model.trim="userKeyword" placeholder="搜索用户名、Host 或权限"></label><span>{{ filteredMySQLUsers.length }} / {{ mysqlUsers.length }} 个用户</span></div><div class="user-table-wrap"><table class="user-table"><thead><tr><th>用户</th><th>允许访问来源（Host）</th><th>登录状态</th><th>拥有的全局权限</th><th>操作</th></tr></thead><tbody><tr v-for="user in pagedMySQLUsers" :key="user.username+'@'+user.host"><td class="user-name-cell" :class="{protected:user.management}" tabindex="0" @click="openUserPrivileges(user)" @keyup.enter="openUserPrivileges(user)"><span class="user-avatar">{{ (user.username || '?').slice(0,1).toUpperCase() }}</span><span><b>{{ user.username }}</b><small>{{ user.management ? '当前 MHA 管理账号' : '点击编辑权限' }}</small></span></td><td><code>{{ user.host }}</code></td><td><div class="user-lock-control"><span :class="['user-state',user.locked?'locked':'active']"><i></i>{{ user.locked ? '已锁定' : '允许登录' }}</span><button class="text-button" :disabled="busy || user.management" @click="toggleMySQLUserLock(user)">{{ user.locked ? '解除锁定' : '锁定账号' }}</button></div></td><td><div class="user-privileges"><span v-for="privilege in user.privileges.slice(0,6)" :key="privilege">{{ privilege }}</span><em v-if="user.privileges.length>6">+{{ user.privileges.length-6 }}</em><small v-if="!user.privileges.length">暂未授予全局权限</small></div></td><td><button class="text-button" :disabled="user.management" @click="openUserPrivileges(user)">编辑权限</button><button class="danger-link" :disabled="user.management" @click="requestDeleteUser(user)">删除用户</button></td></tr><tr v-if="usersLoaded&&!filteredMySQLUsers.length"><td colspan="5" class="empty">没有找到符合条件的 MySQL 用户。</td></tr></tbody></table><div v-if="!usersLoaded" class="user-empty"><span>⌾</span><b>{{ busy ? '正在读取主节点用户' : '等待主节点连接' }}</b><p>页面将自动同步主节点用户，无需输入数据库管理员账号或密码。</p></div></div><footer v-if="usersLoaded&&filteredMySQLUsers.length" class="instance-pager user-pager"><span>第 {{ userPage }} / {{ userPageCount }} 页</span><div><button class="secondary" :disabled="userPage<=1" @click="userPage--">上一页</button><button class="secondary" :disabled="userPage>=userPageCount" @click="userPage++">下一页</button></div></footer></section>
      </main>
      <main v-else-if="view==='accounts'" class="preset-account-page">
        <section class="instance-panel mysql-preset-panel instance-preset-panel">
          <div class="panel-head"><div><h3>预设账号</h3><p>维护监控、MHA 管理和备份账号；保存后，当前及其他集群创建 MySQL 实例时都会自动带入。</p></div><div class="preset-save-actions"><span v-if="presetAccountsDirty">有未保存修改</span><button class="primary" :disabled="busy || !presetAccounts.length" @click="saveAccountPresets">{{ busy ? '保存中…' : '保存预设' }}</button></div></div>
          <div class="mysql-preset-list"><article v-for="account in presetAccounts" :key="account.role"><header><div><b>{{ ({monitor:'监控账号',mha:'MHA 管理账号',backup:'备份账号'})[account.role] || account.role }}</b><small>{{ account.role }}</small></div><label class="switch"><input v-model="account.enabled" type="checkbox" @change="markPresetAccountsDirty"><span>启用</span></label></header><p>{{ account.role === 'monitor' ? '用于监控和健康检查。' : account.role === 'mha' ? '用于 MHA 拓扑管理和切换。' : '用于备份任务。' }}</p><div class="form-row"><label>账号名称<input v-model.trim="account.username" required @input="markPresetAccountsDirty"></label><label>密码<input v-model="account.password" type="password" required autocomplete="new-password" @input="markPresetAccountsDirty"></label></div><label>允许来源 Host<input v-model.trim="account.host" required @input="markPresetAccountsDirty"></label><div class="privilege-picker"><b>授权权限</b><small>只允许选择 GMHA 支持的全局权限；保存后安装任务会生成对应 GRANT 语句。</small><div><label v-for="privilege in mysqlPrivilegeOptions" :key="privilege"><input v-model="account.privileges" type="checkbox" :value="privilege" @change="markPresetAccountsDirty"> {{ privilege }}</label></div></div></article></div>
          <div v-if="!presetAccounts.length" class="preset-account-empty">尚未加载预设账号，请刷新页面后重试。</div>
        </section>
      </main>
      <main v-else-if="view==='params'" class="mysql-parameter-page">
        <section class="parameter-hero"><div><p>MYSQL CONFIGURATION</p><h3>运行参数管理</h3><span>参数目录预先稳定展示；采集后原位填充当前值并识别版本兼容性。</span></div><div class="parameter-summary"><article><b>{{ collectedParameterCount }}</b><small>已采集参数</small></article><article class="dynamic"><b>{{ parameterDynamicCount }}</b><small>动态生效</small></article><article class="restart"><b>{{ parameterRestartCount }}</b><small>重启生效</small></article></div></section>
        <section class="instance-panel parameter-runtime-panel"><div class="parameter-target-bar"><label><span>基准实例</span><select v-model="parameterAuth.instance"><option value="">请选择 MySQL 实例</option><option v-for="item in clusterInstances" :value="value(item,'MachineIP','machine_ip')+':'+value(item,'Port','port')">{{ value(item,'MachineName','machine_name') || value(item,'MachineIP','machine_ip') }} · {{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }}</option></select></label><div class="parameter-scope"><span>应用范围</span><label :class="{active:parameterApplyScope==='instance'}"><input v-model="parameterApplyScope" type="radio" value="instance">当前实例</label><label :class="{active:parameterApplyScope==='cluster'}"><input v-model="parameterApplyScope" type="radio" value="cluster">整个集群（{{ clusterInstances.length }} 个实例）</label></div><div class="parameter-target-actions"><button class="primary" :disabled="busy || !parameterAuth.instance" @click="collectParameters">{{ busy ? '正在采集…' : '采集最新参数' }}</button><button class="secondary parameter-export-button" :disabled="busy || !parametersCollected || !filteredParams.length" title="按当前搜索和分类筛选结果导出" @click="exportParameters">⇩ 导出 Excel</button></div></div><div class="parameter-catalog-note"><span>{{ parametersCollected ? '已完成版本兼容性检查；Excel 将按当前筛选结果导出' : '参数名称已预载，当前值将在采集后原位显示' }}</span><b v-if="parametersCollected">{{ liveParameters.filter(item=>item.compatible===false).length }} 项版本不兼容</b></div><div class="param-toolbar"><label class="parameter-search"><span>⌕</span><input v-model.trim="paramKeyword" placeholder="搜索参数名称、当前值或分类"></label><select v-model="paramCategory"><option value="all">全部分类</option><option v-for="category in paramCategories" :key="category" :value="category">{{ category }}</option></select><span>显示 {{ filteredParams.length }} / {{ liveParameters.length }} 项</span></div><div class="parameter-workspace"><div class="parameter-group-lists"><section v-for="group in parameterGroups" :key="group.category" class="parameter-group"><header><div><b>{{ group.category }}</b><small>{{ group.items.length }} 个参数</small></div><span>{{ group.items.filter(item=>item.dynamic).length }} 动态 · {{ group.items.filter(item=>!item.dynamic).length }} 重启</span></header><div class="param-table-wrap"><table><thead><tr><th>参数名称</th><th>当前值</th><th>修改后的值</th><th>生效方式</th><th>操作</th></tr></thead><tbody><tr v-for="item in group.items" :key="item.name" :class="{'parameter-incompatible-row':item.compatible===false}"><td><b>{{ item.name }}</b><small v-if="parameterChanges.some(change=>change.name===item.name)">已加入待应用</small></td><td class="parameter-value" :title="item.collected ? item.value : ''"><span v-if="item.collected">{{ item.value }}</span><em v-else-if="item.compatible===false" class="parameter-unavailable">版本不兼容</em><em v-else class="parameter-awaiting">采集后显示</em></td><td><input v-model="item.editValue" class="parameter-inline-input" :aria-label="'修改 '+item.name" :placeholder="item.compatible===false ? '当前版本不支持' : (item.collected ? '输入新值' : '等待采集')" :disabled="!item.collected || item.compatible===false" @keyup.enter="stageParameter(item)"></td><td><span v-if="item.compatible===false" class="parameter-mode incompatible">版本不兼容</span><span v-else-if="!item.collected" class="parameter-mode pending">待采集确认</span><span v-else :class="['parameter-mode', item.dynamic ? 'dynamic' : 'restart']">{{ item.dynamic ? '动态生效' : '重启生效' }}</span></td><td class="parameter-inline-actions"><button class="text-button" :disabled="!item.collected || item.compatible===false || !isParameterChanged(item)" @click="stageParameter(item)">保存修改</button><button class="danger-link" :disabled="!item.collected || item.compatible===false" @click="stageParameter(item,'delete')">删除</button></td></tr></tbody></table></div></section><div v-if="!parameterGroups.length" class="parameter-empty">没有匹配的运行参数。</div></div><aside class="parameter-change-panel"><header><div><b>待应用修改</b><small>一次提交，只执行一次必要的重启</small></div><span>{{ parameterChanges.length }}</span></header><div class="parameter-change-counts"><span class="dynamic">动态 {{ stagedDynamicCount }}</span><span class="restart">重启 {{ stagedRestartCount }}</span></div><div class="parameter-change-list"><p v-if="!parameterChanges.length">采集完成后，可直接在左侧参数后输入新值并保存。</p><article v-for="change in parameterChanges" :key="change.name"><div><b>{{ change.name }}</b><small>{{ change.action==='delete' ? '删除配置' : change.value }}</small></div><span :class="['parameter-mode',change.dynamic?'dynamic':'restart']">{{ change.dynamic ? '动态' : '重启' }}</span><button type="button" aria-label="移除待应用修改" @click="removeParameterChange(change.name)">×</button></article></div><footer><small v-if="stagedRestartCount">包含 {{ stagedRestartCount }} 项重启参数，提交后需要二次确认重启范围。</small><small v-else>动态参数会立即下发，同时持久化到配置文件。</small><button class="primary" :disabled="busy || !parameterChanges.length" @click="applyParameterChanges">应用 {{ parameterChanges.length || '' }} 项修改</button></footer></aside></div></section>
      </main>
      <main v-else-if="view==='agent-collect'" class="agent-collect-page">
        <section class="parameter-hero agent-collect-hero"><div><p>AGENT COLLECTION</p><h3>Agent采集</h3><span>统一管理 Agent 需要采集的 MySQL 实例状态指标及执行频率。</span></div><div class="parameter-summary"><article><b>{{ collectConfig.tasks.length }}</b><small>采集指标</small></article><article class="dynamic"><b>{{ collectConfig.tasks.filter(task=>task.enabled).length }}</b><small>已启用</small></article><article class="restart"><b>{{ collectConfig.tasks.filter(task=>!task.enabled).length }}</b><small>已停用</small></article></div></section>
        <section class="instance-panel agent-collect-panel"><div class="panel-head"><div><h3>MySQL 实例状态采集参数</h3><p>策略通过心跳下发到集群内 Agent；采集间隔最小 5 秒，超时最长 10 秒且不能超过采集间隔。</p></div><div class="agent-collect-actions"><label><input v-model="collectConfig.enabled" type="checkbox"> 启用 MySQL 动态采集</label><button class="primary" :disabled="busy" @click="saveParams">{{ busy ? '保存中…' : '保存 Agent 采集配置' }}</button></div></div><div class="agent-collect-meta"><span>配置版本</span><b>{{ collectConfig.version || '未生成' }}</b><span>作用范围</span><b>{{ clusterInstances.length }} 个 MySQL 实例 / {{ clusterName }}</b></div><div class="collect-policy-table"><table><thead><tr><th>指标名称</th><th>内部标识</th><th>分类</th><th>采集</th><th>间隔（秒）</th><th>超时（秒）</th></tr></thead><tbody><tr v-for="task in collectConfig.tasks" :key="task.name"><td><b>{{ task.labels?.display_name || task.name }}</b></td><td><code>{{ task.name }}</code></td><td><span class="agent-category">{{ task.category || '其他' }}</span></td><td><label class="agent-task-switch"><input v-model="task.enabled" type="checkbox"><span>{{ task.enabled ? '启用' : '停用' }}</span></label></td><td><input v-model.number="task.interval_seconds" type="number" min="5"></td><td><input v-model.number="task.timeout_seconds" type="number" min="1" max="10"></td></tr><tr v-if="!collectConfig.tasks.length"><td colspan="6" class="empty">暂无可配置的 MySQL 采集指标，请刷新页面或检查 Manager 配置。</td></tr></tbody></table></div></section>
      </main>
      <main v-else class="mysql-upgrade-page">
        <ClusterRollingUpgrade
          :cluster-name="clusterName"
          :instances="clusterInstances"
          :packages="packages"
          @open-task="emit('open-task',$event)"
          @refresh="emit('refresh')"
        />
        <section class="instance-panel upgrade-page-hero"><div><p>MYSQL LIFECYCLE</p><h3>MySQL 版本升级</h3><span>独立预检报告通过后才允许升级；页面和 API 不接收数据库用户名或密码。</span></div><div class="upgrade-hero-version"><small>当前 → 目标</small><b>{{ upgradePreview.instance ? (value(upgradePreview.instance,'Version','version') || '未知') : '—' }} <i>→</i> {{ upgradePreview.pkg ? (upgradePreview.pkg.version || upgradePreview.pkg.Version) : '—' }}</b></div></section>
        <div class="mysql-upgrade-layout">
          <section class="instance-panel upgrade-config-card">
            <header><span>01</span><div><h4>选择升级目标</h4><p>目标包仅展示与实例机器架构匹配的版本。</p></div></header>
            <div class="upgrade-form-grid"><label>目标实例<select v-model="upgrade.instance" @change="upgradeInstanceChanged"><option value="">选择实例</option><option v-for="item in clusterInstances" :value="value(item,'MachineIP','machine_ip')+':'+value(item,'Port','port')">{{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }} · MySQL {{ value(item,'Version','version') || '版本未知' }}</option></select></label><label>目标架构<select v-model="upgrade.architecture" @change="upgradeArchitectureChanged"><option v-for="arch in upgradeArchitectures" :key="arch" :value="arch">{{ arch }}</option></select></label><label>升级版本<select v-model="upgrade.version"><option value="">{{ upgrade.instance && !upgradeVersions.length ? '暂无更高版本安装包' : '选择版本' }}</option><option v-for="pkg in upgradeVersions" :key="pkg.version || pkg.Version" :value="pkg.version || pkg.Version">MySQL {{ pkg.version || pkg.Version }} · {{ pkg.release_track || pkg.ReleaseTrack }}</option></select></label></div>
            <div v-if="upgradeRoute" :class="['upgrade-route-notice',upgradeRoute.kind]"><i>{{ upgradeRoute.kind==='missing' ? '!' : '↗' }}</i><div><b>{{ upgradeRoute.title }}</b><p>{{ upgradeRoute.detail }}</p></div><span v-if="upgradeRoute.blocked">已隐藏 {{ upgradeRoute.blocked }} 个非推荐目标</span></div>
            <div v-if="upgradePreview.ready" class="upgrade-version-card"><div><small>当前运行版本</small><b>MySQL {{ value(upgradePreview.instance,'Version','version') || '未知' }}</b><span>{{ value(upgradePreview.instance,'MachineIP','machine_ip') }}:{{ value(upgradePreview.instance,'Port','port') }}</span></div><i>→</i><div><small>计划升级版本</small><b>MySQL {{ upgradePreview.pkg.version || upgradePreview.pkg.Version }}</b><span>{{ upgradePreview.pkg.arch || upgradePreview.pkg.Arch }} · {{ upgradePreview.pkg.release_track || upgradePreview.pkg.ReleaseTrack }}</span></div></div>
          </section>
          <section class="instance-panel upgrade-precheck-card">
            <header><span>02</span><div><h4>升级前置检查</h4><p>优先使用 MySQL Shell Upgrade Checker，未安装时回退官方 <code>mysqlcheck --check-upgrade</code>；同时检查软链接、配置、磁盘、事务和复制。</p></div><em :class="upgradePrecheck.status">{{ {idle:'等待检查',running:'检查中',success:'已通过',failed:'未通过'}[upgradePrecheck.status] }}</em></header>
            <div class="upgrade-precheck-actions"><button class="secondary" :disabled="busy || !upgradePreview.ready" @click="runUpgradePrecheck">{{ upgradePrecheck.status==='running' ? '正在生成报告…' : (upgradePrecheck.taskID ? '重新预检' : '开始预检') }}</button><small v-if="upgradePrecheck.checkedAt">完成于 {{ upgradePrecheck.checkedAt }} · {{ upgradePrecheck.checker }}</small><small v-else>预检凭据由 Agent 从主机本地 0600 配置临时注入，不经过 Manager API。</small></div>
            <pre v-if="upgradePrecheck.report" class="upgrade-report">{{ upgradePrecheck.report }}</pre><div v-else class="upgrade-report-empty"><i>✓</i><span>选择目标后运行预检，这里将展示完整检查报告。</span></div>
          </section>
        </div>
        <section class="instance-panel upgrade-risk-card">
          <header><span>03</span><div><h4>风险确认与执行</h4><p>版本升级涉及停写、停机和不可逆的数据字典变化，请在维护窗口内执行。</p></div></header>
          <div class="upgrade-risk-grid"><article class="critical"><b>数据字典不可逆风险</b><p>新版本 mysqld 启动后可能升级系统表。此后不会自动切回旧二进制，应使用已验证的物理备份恢复。</p></article><article><b>业务中断风险</b><p>等待活动事务结束后会停止服务并切换软链接；超时或长事务会导致任务失败。</p></article><article><b>复制与插件兼容风险</b><p>升级前确认副本延迟、复制错误、认证插件、审计插件和备份工具支持目标版本。</p></article></div>
          <label class="upgrade-risk-confirm"><input v-model="upgrade.risk_acknowledged" type="checkbox"><span><b>我确认已完成可恢复备份、验证恢复流程，并已安排维护窗口</b><small>此确认只表示风险已审核，不会替代预检报告。</small></span></label>
          <label v-if="upgradePrecheck.status==='failed'" class="upgrade-force-confirm"><input v-model="upgrade.force" type="checkbox"><span><b>强制升级（绕过预检门禁）</b><small>仅用于已人工评估报告且愿意承担数据不可用风险的场景；操作会在任务记录中标记为强制。</small></span></label>
          <div class="upgrade-submit-row"><div><b>{{ upgradeCanStart ? '升级条件已满足' : '升级尚未就绪' }}</b><small>{{ upgradePrecheck.status==='success' ? '预检已通过' : (upgrade.force ? '已选择强制绕过预检' : '需要通过升级预检') }} · {{ upgrade.risk_acknowledged ? '风险已确认' : '尚未确认备份与维护窗口' }}</small></div><button class="primary" :disabled="busy || !upgradeCanStart" @click="startUpgrade">{{ busy ? '处理中…' : '创建升级任务' }}</button></div>
        </section>
        <section class="instance-panel upgrade-flow"><h4>升级执行流程</h4><ol><li v-for="(stage,index) in upgradeStages" :key="stage"><i>{{ index+1 }}</i><span>{{ stage }}</span></li></ol><p>软链接切换前的失败会自动恢复旧链接；新版本启动后为避免旧二进制打开已升级数据，系统保留新版本并要求人工检查或从备份恢复。</p></section>
      </main>
      <div v-if="lifecycleDialog.open" class="modal-mask lifecycle-mask" @click.self="!busy&&(lifecycleDialog.open=false)"><form class="modal lifecycle-dialog" @submit.prevent="submitLifecycle">
        <header><span>!</span><div><p>MYSQL LIFECYCLE SAFETY GATE</p><h2>{{ lifecycleDialog.action==='restart' ? '安全重启 MySQL 实例' : '安全关闭 MySQL 实例' }}</h2><small>{{ lifecycleEndpoint }} · {{ roleFor(lifecycleDialog.item) }}</small></div><button type="button" :disabled="busy" @click="lifecycleDialog.open=false">×</button></header>
        <div class="lifecycle-risk-banner" :class="{critical:lifecycleIsPrimary}"><b>{{ lifecycleIsPrimary ? '当前拓扑将该实例识别为主库或双主节点' : '该操作会中断当前实例上的数据库连接' }}</b><p>{{ lifecycleDialog.action==='restart' ? '任务会在重启前记录全量主从拓扑，等待活动事务结束，并在恢复后比对 server_uuid、server_id、只读状态、复制源、复制线程、延迟和 GTID。' : '校验通过后只停止该 MySQL systemd 服务，不会关闭宿主机；关闭后不会自动校验在线拓扑，请确认集群仍有可用节点。' }}</p></div>
        <ol class="lifecycle-checklist"><li><i>1</i><span><b>实时拓扑基线</b><small>连接集群所有登记实例，留存角色与复制源关系。</small></span></li><li><i>2</i><span><b>复制与 GTID 门禁</b><small>复制线程必须正常、延迟归零，GTID 集合不得分叉。</small></span></li><li><i>3</i><span><b>重启后原架构复核</b><small>{{ lifecycleDialog.action==='restart' ? '任一节点拓扑变化或数据检查失败，任务即失败并保留审计日志。' : '关机任务不执行此项；重新启动应使用安全重启流程复核。' }}</small></span></li></ol>
        <label class="lifecycle-choice"><input v-model="lifecycleDialog.deepDataCheck" type="checkbox"><span><b>执行深度数据一致性校验（推荐）</b><small>重启前后运行 pt-table-checksum，并检查所有副本的校验结果；大型库耗时较长且会增加有限负载。取消后仍强制校验复制线程、延迟与 GTID。</small></span></label>
        <label class="lifecycle-choice"><input v-model="lifecycleDialog.riskAcknowledged" type="checkbox"><span><b>我确认已安排维护窗口并验证可恢复备份</b><small>长事务超过 60 秒、复制异常或拓扑无法完整读取时，任务会拒绝执行。</small></span></label>
        <label v-if="lifecycleIsPrimary" class="lifecycle-choice critical"><input v-model="lifecycleDialog.primaryAcknowledged" type="checkbox"><span><b>我确认业务流量/VIP 已切走，或接受主库短时不可用</b><small>任务执行时还会实时检查目标是否可写；缺少此项确认将阻止操作。</small></span></label>
        <label class="lifecycle-confirm">输入 <code>{{ lifecycleConfirmation }}</code> 确认<input v-model.trim="lifecycleDialog.confirmation" autocomplete="off" :placeholder="lifecycleConfirmation"></label>
        <footer><button type="button" class="secondary" :disabled="busy" @click="lifecycleDialog.open=false">取消</button><button class="danger-button" :disabled="busy || !lifecycleCanSubmit">{{ busy ? '正在创建任务…' : lifecycleDialog.action==='restart' ? '确认安全重启' : '确认关闭实例' }}</button></footer>
      </form></div>
      <div v-if="parameterRestartDialog.open" class="modal-mask parameter-restart-mask" @click.self="parameterRestartDialog.open=false"><section class="modal parameter-restart-dialog"><header><span>!</span><div><p>RESTART CONFIRMATION</p><h2>参数修改需要重启才能生效</h2></div><button type="button" @click="parameterRestartDialog.open=false">×</button></header><div class="parameter-restart-summary"><b>{{ stagedRestartCount }} 项参数需要重启</b><span>另有 {{ stagedDynamicCount }} 项动态参数会在任务执行时立即生效</span></div><div class="parameter-restart-options"><label :class="{active:parameterRestartDialog.scope==='instance'}"><input v-model="parameterRestartDialog.scope" type="radio" value="instance"><span><b>重启当前实例</b><small>{{ parameterAuth.instance }}，连接会短暂中断</small></span></label><label :class="{active:parameterRestartDialog.scope==='cluster'}"><input v-model="parameterRestartDialog.scope" type="radio" value="cluster"><span><b>滚动重启整个集群</b><small>按任务顺序重启 {{ clusterInstances.length }} 个实例并逐一验证</small></span></label></div><div class="parameter-restart-warning"><b>请确认当前处于可重启维护窗口</b><p>任务会先备份 my.cnf，再应用全部修改；每个实例重启后必须通过 systemd 和数据库连接验证。</p></div><footer><button type="button" class="secondary" @click="parameterRestartDialog.open=false">返回检查</button><button type="button" class="danger-button" :disabled="busy" @click="confirmParameterRestart">确认修改并执行重启</button></footer></section></div>
      <div v-if="userDialog.open" class="modal-mask user-modal-mask" @click.self="closeUserDialog"><form class="modal user-editor" @submit.prevent="saveMySQLUser"><header><div><p>{{ userDialog.mode==='create' ? 'CREATE MYSQL USER' : 'MANAGE PRIVILEGES' }}</p><h2>{{ userDialog.mode==='create' ? '新增数据库用户' : userDialog.username+'@'+userDialog.host }}</h2><span>{{ userDialog.mode==='create' ? '账号将在主节点创建，并由 MySQL 复制策略决定后续同步。' : '勾选期望权限，系统会自动计算 GRANT 与 REVOKE 差异。' }}</span></div><button type="button" @click="closeUserDialog">×</button></header><div v-if="userDialog.mode==='create'" class="user-editor-fields"><label>用户名<input v-model.trim="userDialog.username" required autofocus placeholder="例如 app_reader"></label><label>来源 Host<input v-model.trim="userDialog.host" required placeholder="例如 10.0.0.%"></label><label class="wide">登录密码<input v-model="userDialog.password" type="password" required autocomplete="new-password" placeholder="设置高强度密码"></label></div><section class="user-permission-picker"><header><div><b>全局权限</b><small>权限列表已按主节点版本过滤；5.7 与早期 8.0 不会显示不兼容的动态权限。</small></div><span>已选 {{ userDialog.privileges.length }} 项</span></header><div><label v-for="privilege in mysqlUserPrivilegeOptions" :key="privilege" :class="{selected:userDialog.privileges.includes(privilege)}"><input v-model="userDialog.privileges" type="checkbox" :value="privilege"><span><i>✓</i>{{ privilege }}</span></label></div></section><footer><span><i>i</i>提交后等待 Agent 执行完成，并自动重新同步用户列表。</span><div><button type="button" class="secondary" @click="closeUserDialog">取消</button><button class="primary" :disabled="busy">{{ busy ? '正在执行…' : userDialog.mode==='create' ? '创建用户' : '保存权限' }}</button></div></footer></form></div>
      <div v-if="deleteUserDialog.open" class="modal-mask user-modal-mask" @click.self="deleteUserDialog.open=false"><form class="modal user-delete-dialog" @submit.prevent="confirmDeleteUser"><header><span>!</span><div><p>DESTRUCTIVE ACTION</p><h2>删除数据库用户</h2></div><button type="button" @click="deleteUserDialog.open=false">×</button></header><div><p>删除后，该账号的登录能力和全部授权会立即失效。此操作仅在主节点执行，无法从 GMHA 页面撤销。</p><label>输入 <b>{{ deleteUserDialog.user?.username }}@{{ deleteUserDialog.user?.host }}</b> 确认<input v-model.trim="deleteUserDialog.confirmation" autocomplete="off"></label></div><footer><button type="button" class="secondary" @click="deleteUserDialog.open=false">取消</button><button class="danger-button" :disabled="busy || deleteUserDialog.confirmation!==(deleteUserDialog.user?.username+'@'+deleteUserDialog.user?.host)">{{ busy ? '正在删除…' : '确认删除用户' }}</button></footer></form></div>
    </section>`
}
