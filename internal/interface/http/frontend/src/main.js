// 页面模板定义在本文件中，因此使用包含运行时模板编译器的 Vue 构建版本。
import { computed, createApp, onMounted, onUnmounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'
import './style.css'
import './manager.css'
import './onboarding.css'
import './flow-layout.css'
import './enterprise-layout.css'
import './cluster-overview.css'
import './backup.css'
import './architecture-run.css'
import './architecture-process.css'
import './architecture-topology.css'
import './architecture-topology-risk.css'
import './live-topology.css'
import './machine-delete.css'
import './machine-bulk.css'
import InstanceManagement from './instance-management.js'
import AlertManagement from './alert-management.js'
import './instance-management.css'
import './instance-management-extended.css'
import './mysql-user-management.css'
import './log-safety.css'
import './alert-management.css'
import './select-ui.css'
import './responsive-layout.css'
import './cluster-focus.css'
import './agent-resources.css'
import './cluster-members.css'
import './upgrade.css'
import './vip-management.css'
import './architecture-vip-editor.css'
import './architecture-clarity.css'
import './cluster-observability.css'

const navGroups = [
  { title: '工作台', icon: '▦', items: [{ id: 'overview', icon: '•', label: '运行概览' }] },
  { title: '资源管理', icon: '▣', items: [
    { id: 'machines', icon: '▣', label: '机器与凭证' },
    { id: 'agents', icon: '◉', label: 'Agent 管理' }
  ] },
  { title: '集群运维', icon: '◇', items: [
    { id: 'clusters', icon: '◇', label: '集群列表' },
    { id: 'automation', icon: '⚙', label: '自动化任务' }
  ] },
  { title: '数据库管理', icon: '▤', items: [
    { id: 'mysql', icon: '▤', label: 'MySQL 管理' },
    { id: 'packages', icon: '⬒', label: '安装包管理' }
  ] },
  { title: '告警管理', icon: '!', items: [
    { id: 'alerts', icon: '!', label: '告警中心' }
  ] },
  { title: '平台运维', icon: '✓', items: [
    { id: 'tasks', icon: '✓', label: '任务中心' },
    { id: 'manager', icon: '•', label: 'Manager 控制台' }
  ] }
]

const api = async (path, options = {}) => {
  const response = await fetch(`/api/v1${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  })
  const raw = await response.text()
  let payload = {}
  try { payload = raw ? JSON.parse(raw) : {} } catch (_) { payload = {} }
  if (!response.ok) {
    const detail = payload.error || raw.trim() || `HTTP ${response.status}`
    const error = new Error(`请求失败（${response.status}）：${detail}`)
    error.status = response.status
    error.payload = payload
    throw error
  }
  return payload
}

// List APIs historically returned both bare arrays and paged objects. Normalize
// them at the UI boundary so an empty/null response cannot abort Vue rendering.
const asList = (value, ...keys) => {
  if (Array.isArray(value)) return value.filter(Boolean)
  for (const key of keys) {
    if (Array.isArray(value?.[key])) return value[key].filter(Boolean)
  }
  if (Array.isArray(value?.items)) return value.items.filter(Boolean)
  return []
}

createApp({
  components: { InstanceManagement, AlertManagement },
  setup() {
    const active = ref('overview')
    const expandedNav = ref(Object.fromEntries(navGroups.map(group => [group.title, true])))
    const loading = ref(false)
    const error = ref('')
    const notice = ref('')
    const showOnboard = ref(false)
    const showOnboardFlow = ref(false)
    const showMachineDetail = ref(false)
    const showMachineDelete = ref(false)
    const machineDeleteForm = ref({ mode: 'cleanup', delete_mysql: false, delete_agent: true, confirmation: '' })
    const machineDeleteSubmitting = ref(false)
    const machineDeleteError = ref('')
    const machineDeletePrechecking = ref(false)
    const machineDeletePrecheck = ref({ registered_mysql_ports: [], remote_checked: false, mysql_detected: false, mysql_residues: [], warning: '' })
    const showCredential = ref(false)
    const credentialSubmitting = ref(false)
    const showAssign = ref(false)
    const showQuickClusterAssign = ref(false)
    const showAgentDetail = ref(false)
    const showMySQLInstall = ref(false)
    const showMySQLTask = ref(false)
    const selectedTaskDetail = ref(null)
    const selectedTaskStep = ref(null)
    const selectedTaskFlowDetail = ref(null)
    const taskDetailStack = ref([])
    const taskReturnContext = ref(null)
    const mysqlView = ref('overview')
    const packageItems = ref([])
    const packageSettings = ref({ categories: [], supported_architectures: [], catalog: [], bundles: [] })
    const packageForm = ref({ category: 'mysql', arch: '未识别', version: '', description: '', file: null })
    const packageKeyword = ref('')
    const packageFetching = ref({})
    const packageBundleID = ref('mysql-8.0.44-x86_64')
    const packageBundleFetching = ref(false)
    const upgradeOverview = ref({ manager_version: 'V0.0.1', agent_total: 0, agent_versions: [], manager_packages: [], agent_packages: [], storage: {} })
    const upgradeJobs = ref([])
    const upgradeForm = ref({ manager_package: '', agent_package: '', targets: [] })
    const upgradeSubmitting = ref(false)
    const upgradeComponent = ref('manager')
    const upgradeAgents = ref([])
    const upgradeAgentTotal = ref(0)
    const upgradeAgentPage = ref(1)
    const upgradeAgentPageSize = 50
    const upgradeAgentKeyword = ref('')
    const upgradeAgentStatus = ref('online')
    const upgradeAgentVersion = ref('all')
    const upgradeAgentLoading = ref(false)
    const agentVersionDetecting = ref({})
    const agentVersionBatchDetecting = ref(false)
    let upgradeTimer = null
    const selectedPackageBundle = computed(() => (packageSettings.value.bundles || []).find(item => item.id === packageBundleID.value) || (packageSettings.value.bundles || []).find(item => item.default) || null)
    const taskFilter = ref('all')
    const taskKeyword = ref('')
    const taskTypeFilter = ref('all')
    const taskPage = ref(1)
    const taskPageSize = ref(20)
    const taskTotal = ref(0)
    const selectedTaskIDs = ref([])
    const recentTaskItems = ref([])
    const showClusterEditor = ref(false)
    const showClusterCleanup = ref(false)
    const showClusterMembers = ref(false)
    const clusterCandidatesLoading = ref(false)
    const clusterCandidatesError = ref('')
    const mysqlTaskDetail = ref(null)
    const clusterCleanupResult = ref(null)
    const selectedClusterForMembers = ref(null)
    const clusterCandidates = ref([])
    const clusterCandidatePage = ref(1)
    const clusterCandidateTotal = ref(0)
    const selectedClusterMachineIDs = ref([])
    const clusterMemberAssignResult = ref(null)
    const clusterPage = ref(1)
    const clusterTotal = ref(0)
    const clusterKeyword = ref('')
    const clusterPageItems = ref([])
    const selectedClusterDetail = ref(null)
    const clusterTopology = ref({ nodes: [], edges: [], overview: { summary: {}, series: [], machines: [] } })
    const clusterTopologyError = ref('')
    const clusterTopologyRefreshing = ref(false)
    const clusterTopologyLastUpdated = ref('')
    const clusterTopologyAutoRefresh = ref(true)
    const clusterOverviewRange = ref(60)
    const clusterMachineItems = ref([])
    const clusterMachineStaticInfo = ref({})
    const clusterMachinePage = ref(1)
    const clusterMachineTotal = ref(0)
    const selectedClusterOperationMachineIDs = ref([])
    const clusterMySQLDialog = ref(null)
    const clusterMySQLForm = ref({ package_name: '', port: 3306, server_id_start: 1, mysql_user: 'mysql', root_password: '', profile: 'default', install_pt_tools: false, install_xtrabackup: false, memory_allocator: 'system' })
    const clusterMySQLConfirm = ref('')
    const automationSelectedClusters = ref([])
    const automationForm = ref({ action: 'install', port: 3306, server_id_start: 1, mysql_user: 'root', mysql_password: '', root_password: '', profile: 'default', install_pt_tools: false, install_xtrabackup: false, memory_allocator: 'system', script: '', user_action: 'create', target_username: '', target_password: '', target_host: '%', privileges: ['SELECT'], parameter_name: '', parameter_value: '', apply_mode: 'dynamic', config_path: '/etc/my.cnf', systemd_unit: 'mysqld' })
    const automationConfirm = ref('')
    const automationRunning = ref(false)
    const automationResults = ref([])
    const backupPolicies = ref([])
    const backupRuns = ref([])
    const showBackupPolicyEditor = ref(false)
    const backupPolicyForm = ref(newBackupPolicyForm())
    const architectureForm = ref({ architecture: 'master_slave', primary_machine_id: '', move_vip: false, nodes: [] })
    const architectureCurrent = ref({ type: 'standalone', label: '独立实例', primary_machine_ids: [], nodes: [], edges: [] })
    const architectureHasChanges = computed(() => architectureForm.value.move_vip || architectureForm.value.architecture !== architectureCurrent.value.type || (architectureCurrent.value.primary_machine_ids.length > 0 && !architectureCurrent.value.primary_machine_ids.includes(architectureForm.value.primary_machine_id)) || architectureForm.value.nodes.some(architectureNodeHasChanges))
    const architecturePlan = ref(null)
    const architectureRun = ref(null)
    const architecturePlanDialog = ref(false)
    const architectureLinkSource = ref('')
    const architectureSelectedNode = ref('')
    const architectureRoleChangeDialog = ref(null)
    const architectureRoleChangeFeedback = ref('')
    const architectureDraftHistory = ref([])
    const architectureDraggingNode = ref('')
    const vipDraggingAddress = ref('')
    const vipMagnetTargetID = ref('')
    const vipSnapTargetID = ref('')
    const vipDriftDialog = ref(null)
    const vipConfigs = ref([])
    const vipStates = ref([])
    const vipBusy = ref(false)
    const vipEditingAddress = ref('')
    const vipForm = ref({ vip_name: '业务 VIP', vip_address: '', vip_prefix: 24, default_interface: '', target_machine_id: '', arping_count: 3 })
    const vipEditingConfig = computed(() => vipConfigs.value.find(item => item.vip_address === vipEditingAddress.value) || null)
    const vipEditorState = computed(() => {
      if (!vipEditingConfig.value) return { vip_status: 'UNKNOWN', current_holder_machine_id: '', expected_holder_machine_id: '', current_interface: '' }
      const current = vipStateFor(vipEditingConfig.value)
      return { ...current, expected_holder_machine_id: current.expected_holder_machine_id || vipEditingConfig.value.target_machine_id || '' }
    })
    const vipEditorIsNew = computed(() => !vipEditingConfig.value)
    const vipTargetMachine = computed(() => clusterMachineItems.value.find(item => (item.ID || item.id) === vipForm.value.target_machine_id) || null)
    const vipInterfaceOptions = computed(() => {
      if (!vipTargetMachine.value) return []
      const seen = new Set()
      return clusterMachineInterfaces(vipTargetMachine.value).filter(item => item.name !== '网卡待采集' && item.name !== 'lo' && !seen.has(item.name) && seen.add(item.name))
    })
    const architectureSubmitting = ref(false)
    let architecturePollTimer = null
    let clusterTopologyRefreshTimer = null
    let managerStatusTimer = null
    let agentResourceRefreshTimer = null
    const agentResourceRefreshing = ref(false)
    const agentResourceUpdatedAt = ref('')
    const agentResourceRefreshSeconds = ref(30)
    const data = ref({ manager: { running: false, config: {} }, machines: [], credentials: [], clusters: [], agents: [], agentTotal: 0, agentPage: 1, agentPageSize: 50, agentKeyword: '', agentStatus: 'all', agentVersion: 'all', agentCandidates: [], mysqlInstances: [], mysqlPackages: [], accountPresets: [], tasks: [], taskStats: { all: 0, running: 0, success: 0, failed: 0 }, recovery: [], clusterSection: 'overview', instanceView: 'instances' })
    const machinePage = ref(1), credentialPage = ref(1), pageSize = 20, machineTotal = ref(0), credentialTotal = ref(0)
    const machineKeyword = ref('')
    const machineClusterFilter = ref('all')
    const selectedMachineIDs = ref([])
    const showBatchOnboard = ref(false)
    const batchOnboardRows = ref([{ name: '', ip: '' }, { name: '', ip: '' }])
    const batchOnboardShared = ref({ ssh_port: 22, ssh_user: 'root', ssh_password: '', credential_id: '', preserve_agent: true, preserve_mysql: true, concurrent: true, concurrency: 3 })
    const batchOnboardRunning = ref(false)
    const batchOnboardResults = ref([])
    const showBulkDelete = ref(false)
    const bulkDeleteForm = ref({ mode: 'cleanup', delete_mysql: true, delete_agent: true, concurrent: true, concurrency: 3, confirmation: '' })
    const bulkDeleteRunning = ref(false)
    const bulkDeleteResults = ref([])
    const selectedMachine = ref(null)
    const selectedAgent = ref(null)
    const agentActionDialog = ref(null)
    const agentActionInput = ref('')
    const agentActionReturnToDetail = ref(false)
    const agentActionSubmitting = ref(false)
    const agentActionError = ref('')
    const agentActionElapsed = ref(0)
    let agentActionElapsedTimer = null
    const selectedMachineErrorExpanded = ref(false)
    const selectedMachineCluster = ref('')
    const machineStaticInfo = ref(null)
    const machineDynamicInfo = ref(null)
    const machineInfoError = ref('')
    const managerForm = ref({})
    const form = ref({ name: '', ip: '', ssh_port: 22, ssh_user: 'root', ssh_password: '', credential_id: '', preserve_agent: true, preserve_mysql: true })
    const credentialForm = ref({ name: '', ssh_user: 'root', type: 'password', ssh_password: '', private_key: '', passphrase: '' })
    const mysqlRuntimeParameterGroups = [
      { name: '连接、字符集与缓存', fields: [
        { key: 'character_set_server', label: 'character_set_server', default: 'utf8mb4' }, { key: 'collation_server', label: 'collation_server', default: 'utf8mb4_0900_ai_ci' },
        { key: 'skip_name_resolve', label: 'skip_name_resolve', default: '1', options: ['1','0'] }, { key: 'symbolic_links', label: 'symbolic_links', default: '0', options: ['0','1'] },
        { key: 'autocommit', label: 'autocommit', default: '1', options: ['1','0'] }, { key: 'transaction_isolation', label: 'transaction_isolation', default: 'READ-COMMITTED', options: ['READ-COMMITTED','REPEATABLE-READ','READ-UNCOMMITTED','SERIALIZABLE'] },
        { key: 'max_connections', label: 'max_connections', placeholder: '按机器内存与 Profile 自动计算' }, { key: 'max_connect_errors', label: 'max_connect_errors', default: '1000' }, { key: 'max_allowed_packet', label: 'max_allowed_packet', default: '64M' },
        { key: 'interactive_timeout', label: 'interactive_timeout', default: '1800' }, { key: 'wait_timeout', label: 'wait_timeout', default: '1800' }, { key: 'lock_wait_timeout', label: 'lock_wait_timeout', default: '1800' },
        { key: 'table_open_cache', label: 'table_open_cache', placeholder: '由 Profile 自动计算' }, { key: 'thread_cache_size', label: 'thread_cache_size', placeholder: '由 Profile 自动计算' }
      ]},
      { name: '慢查询与日志', fields: [
        { key: 'log_timestamps', label: 'log_timestamps', default: 'SYSTEM', options: ['SYSTEM','UTC'] }, { key: 'slow_query_log', label: 'slow_query_log', default: '1', options: ['1','0'] },
        { key: 'slow_query_log_file', label: 'slow_query_log_file', placeholder: '数据目录/slow.log' }, { key: 'long_query_time', label: 'long_query_time', default: '2' }, { key: 'min_examined_row_limit', label: 'min_examined_row_limit', default: '100' },
        { key: 'log_slow_admin_statements', label: 'log_slow_admin_statements', default: '1', options: ['1','0'] }, { key: 'log_slow_replica_statements', label: 'log_slow_replica_statements', default: '1', options: ['1','0'] }, { key: 'log_throttle_queries_not_using_indexes', label: '无索引日志限流', default: '10' }
      ]},
      { name: 'Binlog、GTID 与只读', fields: [
        { key: 'binlog_format', label: 'binlog_format', default: 'ROW', options: ['ROW','MIXED','STATEMENT'] }, { key: 'sync_binlog', label: 'sync_binlog', default: '1' }, { key: 'binlog_expire_logs_seconds', label: 'binlog 保留秒数', default: '604800' },
        { key: 'binlog_rows_query_log_events', label: 'binlog_rows_query_log_events', default: '1', options: ['1','0'] }, { key: 'log_replica_updates', label: 'log_replica_updates', default: '1', options: ['1','0'] },
        { key: 'gtid_mode', label: 'gtid_mode', default: 'ON', options: ['ON','OFF','ON_PERMISSIVE','OFF_PERMISSIVE'] }, { key: 'enforce_gtid_consistency', label: 'enforce_gtid_consistency', default: 'ON', options: ['ON','WARN','OFF'] },
        { key: 'relay_log_recovery', label: 'relay_log_recovery', default: '1', options: ['1','0'] }, { key: 'read_only', label: 'read_only', default: '1', options: ['1','0'] }, { key: 'super_read_only', label: 'super_read_only', default: '1', options: ['1','0'] }
      ]},
      { name: 'InnoDB', fields: [
        { key: 'default_storage_engine', label: 'default_storage_engine', default: 'InnoDB', options: ['InnoDB','MyISAM'] }, { key: 'innodb_data_file_path', label: 'innodb_data_file_path', default: 'ibdata1:128M:autoextend' }, { key: 'innodb_temp_data_file_path', label: 'innodb_temp_data_file_path', default: 'ibtmp1:128M:autoextend:max:30720M' },
        { key: 'innodb_buffer_pool_size', label: 'innodb_buffer_pool_size', placeholder: '按机器内存与 Profile 自动计算' }, { key: 'innodb_buffer_pool_instances', label: 'buffer_pool_instances', placeholder: '按 Buffer Pool 自动计算' }, { key: 'innodb_redo_log_capacity', label: 'innodb_redo_log_capacity', placeholder: '按 Buffer Pool 自动计算' },
        { key: 'innodb_flush_log_at_trx_commit', label: 'flush_log_at_trx_commit', default: '1', options: ['1','2','0'] }, { key: 'innodb_lock_wait_timeout', label: 'innodb_lock_wait_timeout', default: '600' }, { key: 'innodb_file_per_table', label: 'innodb_file_per_table', default: '1', options: ['1','0'] },
        { key: 'innodb_flush_method', label: 'innodb_flush_method', default: 'O_DIRECT', options: ['O_DIRECT','O_DIRECT_NO_FSYNC','FSYNC','O_DSYNC'] }, { key: 'innodb_log_buffer_size', label: 'innodb_log_buffer_size', default: '16M' }, { key: 'innodb_read_io_threads', label: 'innodb_read_io_threads', default: '8' }, { key: 'innodb_write_io_threads', label: 'innodb_write_io_threads', default: '8' }
      ]},
      { name: '会话缓冲与系统限制', fields: [
        { key: 'key_buffer_size', label: 'key_buffer_size', default: '32M' }, { key: 'myisam_sort_buffer_size', label: 'myisam_sort_buffer_size', default: '64M' },
        { key: 'sort_buffer_size', label: 'sort_buffer_size', placeholder: '由 Profile 自动计算' }, { key: 'read_buffer_size', label: 'read_buffer_size', placeholder: '由 Profile 自动计算' }, { key: 'read_rnd_buffer_size', label: 'read_rnd_buffer_size', placeholder: '由 Profile 自动计算' }, { key: 'join_buffer_size', label: 'join_buffer_size', placeholder: '由 Profile 自动计算' },
        { key: 'open_files_limit', label: 'open_files_limit', placeholder: '由 Profile 自动计算' }, { key: 'limit_nproc', label: 'systemd LimitNPROC', default: '65536' }, { key: 'sysctl_swappiness', label: 'vm.swappiness', placeholder: '由 Profile 自动计算' }
      ]}
    ]
    // Empty means no override: the backend keeps the value calculated from the
    // target machine resources and selected Profile. Defaults are display-only.
    const mysqlRuntimeParameters = Object.fromEntries(mysqlRuntimeParameterGroups.flatMap(group => group.fields).map(field => [field.key, '']))
    const mysqlInstallForm = ref({ machine: '', version: '', architecture: '', port: 3306, server_id: 1, mysql_user: 'mysql', root_password: '', profile: 'default', package_name: '', instance_dir: '', data_dir: '', binlog_dir: '', redo_dir: '', undo_dir: '', tmp_dir: '', base_dir: '/usr/local/mysql', my_cnf_path: '', socket_path: '', error_log: '', pid_file: '', character_sets_dir: '', plugin_dir: '', install_pt_tools: false, install_xtrabackup: false, memory_allocator: 'system', runtime_parameters: mysqlRuntimeParameters, _runtime_parameter_groups: mysqlRuntimeParameterGroups, accounts: [{ role: 'monitor', username: '', password: '', host: '', enabled: true, extended_backup: false, privileges: [] }, { role: 'mha', username: '', password: '', host: '', enabled: true, extended_backup: false, privileges: [] }, { role: 'backup', username: '', password: '', host: '', enabled: true, extended_backup: false, privileges: [] }] })
    const normalizeMySQLArchitecture = architecture => {
      const arch = String(architecture || '').toLowerCase()
      if (['amd64', 'x64', 'x86-64', 'x86_64'].includes(arch)) return 'x86_64'
      if (['arm64', 'armv8', 'aarch64'].includes(arch)) return 'aarch64'
      return arch
    }
    const mysqlInstallArchitectures = computed(() => [...new Set((data.value.mysqlPackages || []).map(pkg => normalizeMySQLArchitecture(pkg.arch || pkg.Arch)).filter(Boolean))])
    const mysqlInstallVersions = computed(() => [...new Map((data.value.mysqlPackages || [])
      .filter(pkg => !mysqlInstallForm.value.architecture || normalizeMySQLArchitecture(pkg.arch || pkg.Arch) === normalizeMySQLArchitecture(mysqlInstallForm.value.architecture))
      .map(pkg => [pkg.version || pkg.Version, pkg])).values()])
    const mysqlInstallTargetInfo = ref({ loading: false, arch: '', glibc: '', error: '' })
    const compareCompatibilityVersion = (left, right) => {
      const a = String(left || '').split('.').map(Number), b = String(right || '').split('.').map(Number)
      for (let i = 0; i < Math.max(a.length, b.length); i++) { const diff = (a[i] || 0) - (b[i] || 0); if (diff) return diff }
      return 0
    }
    const compatibleMySQLInstallPackages = computed(() => (data.value.mysqlPackages || []).filter(pkg =>
      (!mysqlInstallForm.value.version || (pkg.version || pkg.Version) === mysqlInstallForm.value.version) &&
      (!mysqlInstallForm.value.architecture || normalizeMySQLArchitecture(pkg.arch || pkg.Arch) === normalizeMySQLArchitecture(mysqlInstallForm.value.architecture)) &&
      (!mysqlInstallTargetInfo.value.glibc || compareCompatibilityVersion(pkg.glibc_version || pkg.GlibcVersion, mysqlInstallTargetInfo.value.glibc) <= 0))
      .sort((a,b) => compareCompatibilityVersion(b.glibc_version || b.GlibcVersion, a.glibc_version || a.GlibcVersion)))
    const selectedMySQLInstallPackage = computed(() => compatibleMySQLInstallPackages.value[0] || null)
    const mysqlInstallCompatibility = computed(() => {
      if (!mysqlInstallForm.value.machine) return { status: 'idle', message: '选择目标机器后检查架构与 glibc 兼容性。' }
      if (mysqlInstallTargetInfo.value.loading) return { status: 'checking', message: '正在读取目标机器架构与 glibc…' }
      if (mysqlInstallTargetInfo.value.error) return { status: 'unknown', message: mysqlInstallTargetInfo.value.error }
      if (!mysqlInstallForm.value.version || !mysqlInstallForm.value.architecture || !mysqlInstallTargetInfo.value.glibc) return { status: 'unknown', message: '目标信息不完整，提交时将再次执行兼容性校验。' }
      const pkg = selectedMySQLInstallPackage.value
      if (!pkg) return { status: 'incompatible', message: `没有兼容制品：MySQL ${mysqlInstallForm.value.version} · ${mysqlInstallForm.value.architecture} · 目标 glibc ${mysqlInstallTargetInfo.value.glibc}。请先在安装包管理上传 glibc 不高于目标机器的制品。` }
      return { status: 'compatible', package: pkg, message: `兼容性通过，将使用 ${pkg.file_name || pkg.FileName}（目标 glibc ${mysqlInstallTargetInfo.value.glibc}）。` }
    })
    const mysqlVersionParameterGroups = computed(() => selectedMySQLInstallPackage.value?.runtime_parameter_groups || [])
    const mysqlInstallParameterGroups = computed(() => [...mysqlRuntimeParameterGroups, ...mysqlVersionParameterGroups.value])
    Object.defineProperties(mysqlInstallForm.value, {
      _selected_package: { enumerable: false, get: () => selectedMySQLInstallPackage.value },
      _version_parameter_groups: { enumerable: false, get: () => mysqlVersionParameterGroups.value },
      _parameter_groups: { enumerable: false, get: () => mysqlInstallParameterGroups.value }
    })
    async function mysqlInstallMachineChanged() {
      const machine = data.value.machines.find(item => (item.IP || item.ip) === mysqlInstallForm.value.machine)
      mysqlInstallTargetInfo.value = { loading: true, arch: '', glibc: '', error: '' }
      try {
        const info = machine ? await api(`/machines/${encodeURIComponent(machine.ID || machine.id)}/static-info`) : null
        const host = info?.host || info?.Host || {}
        mysqlInstallTargetInfo.value = { loading: false, arch: normalizeMySQLArchitecture(host.arch || host.Arch), glibc: String(host.glibc_version || host.GlibcVersion || '').trim(), error: '' }
      } catch (err) {
        mysqlInstallTargetInfo.value = { loading: false, arch: '', glibc: '', error: `无法读取目标机器兼容信息：${err.message}` }
      }
      mysqlInstallForm.value.architecture = mysqlInstallTargetInfo.value.arch || normalizeMySQLArchitecture(machine?.Architecture || machine?.architecture || machine?.Arch || machine?.arch) || mysqlInstallForm.value.architecture || mysqlInstallArchitectures.value[0] || ''
      mysqlInstallArchitectureChanged()
    }
    function mysqlInstallArchitectureChanged() {
      if (!mysqlInstallVersions.value.some(pkg => (pkg.version || pkg.Version) === mysqlInstallForm.value.version)) mysqlInstallForm.value.version = mysqlInstallVersions.value[0]?.version || ''
      mysqlInstallForm.value.package_name = ''
    }
    const clusterForm = ref({ old_name: '', name: '', description: '' })
    const selectedCredential = ref('')
    const assignedMachineIDs = ref([])
    const onboardingFlow = ref([])
    const onboardingResult = ref(null)
    const onboardingDetected = ref({ agent: false, mysql: false })
    const canSkipPrecheck = ref(false)
    const expandedFlowErrors = ref({})
    const mysqlPrivilegeOptions = [
      'SELECT', 'INSERT', 'UPDATE', 'DELETE', 'CREATE', 'CREATE USER', 'ALTER', 'DROP', 'SHOW VIEW', 'TRIGGER', 'EVENT',
  'PROCESS', 'RELOAD', 'LOCK TABLES', 'REPLICATION CLIENT', 'REPLICATION SLAVE', 'CONNECTION_ADMIN', 'SYSTEM_VARIABLES_ADMIN', 'REPLICATION_SLAVE_ADMIN', 'BACKUP_ADMIN', 'CLONE_ADMIN'
    ]

    function newBackupPolicyForm() {
      const start = new Date(Date.now() + 3600000); start.setSeconds(0, 0)
      return { id: '', name: '', machine_id: '', port: 3306, backup_type: 'full', weekday_backup_types: {'0':'full','1':'incremental','2':'incremental','3':'incremental','4':'incremental','5':'full','6':'full'}, disk_usage_threshold: 95, schedule_type: 'weekly', weekdays: [1,2,3,4,5], interval_minutes: 1440, start_at: start.toISOString().slice(0,16), retry_count: 5, retry_interval_seconds: 60, include_binlog: true, backup_location: '/data/gmha/backups', mysql_user: 'backup', mysql_password: '', enabled: true }
    }

    const current = computed(() => navGroups.flatMap(group => group.items).find(item => item.id === active.value))
    function toggleNavGroup(title) { expandedNav.value[title] = !expandedNav.value[title] }
    function chooseNavigation(id) {
      if (selectedClusterDetail.value) closeClusterDetail()
      if (selectedTaskDetail.value) clearTaskDetail()
      active.value = id
      error.value = ''
    }
    const metrics = computed(() => {
      const agents = asList(data.value.agents)
      const tasks = asList(data.value.tasks)
      return [
        { label: '已纳管机器', value: data.value.machines.length, hint: '资源池中的服务器', tone: 'blue' },
        { label: '在线 Agent', value: agents.filter(item => String(item.State || item.state).toLowerCase() === 'online').length, hint: `共 ${agents.length} 个 Agent`, tone: 'green' },
        { label: '运行中任务', value: Number(data.value.taskStats?.running || 0), hint: `全部 ${Number(data.value.taskStats?.all || tasks.length)} 个任务`, tone: 'amber' },
        { label: '异常需关注', value: agents.filter(item => ['offline', 'error', 'degraded'].includes(String(item.State || item.state).toLowerCase())).length, hint: 'Agent 离线或安装失败', tone: 'red' }
      ]
    })
    const unifiedTasks = computed(() => asList(data.value.tasks, 'tasks'))
    const recentTasks = computed(() => recentTaskItems.value)
    const filteredTasks = computed(() => unifiedTasks.value.filter(item => {
      const status = String(item.Status || item.status || '').toLowerCase()
      const statusMatch = taskFilter.value === 'all' ||
        (taskFilter.value === 'running' && ['pending', 'sent', 'running'].includes(status)) ||
        (taskFilter.value === 'success' && ['success', 'completed', 'succeeded'].includes(status)) ||
        (taskFilter.value === 'failed' && ['failed', 'error', 'suppressed'].includes(status))
      const typeMatch = taskTypeFilter.value === 'all' || String(item.Type || item.type || '').toLowerCase() === taskTypeFilter.value
      const keyword = taskKeyword.value.toLowerCase()
      const text = [item.ID, item.id, item.Type, item.type, taskTitle(item), item.MachineID, item.machine_id, item.Target, item.target].filter(Boolean).join(' ').toLowerCase()
      return statusMatch && typeMatch && (!keyword || text.includes(keyword))
    }))
    function taskListPath() {
      const query = new URLSearchParams({ page: String(taskPage.value), page_size: String(taskPageSize.value) })
      if (taskFilter.value !== 'all') query.set('status', taskFilter.value)
      if (taskTypeFilter.value !== 'all') query.set('type', taskTypeFilter.value)
      if (taskKeyword.value.trim()) query.set('keyword', taskKeyword.value.trim())
      return `/tasks?${query}`
    }
    async function loadTaskPage() {
      try {
        const result = await api(taskListPath())
        data.value.tasks = asList(result, 'tasks')
        taskTotal.value = Number(result?.total || data.value.tasks.length)
        selectedTaskIDs.value = selectedTaskIDs.value.filter(id => data.value.tasks.some(item => (item.ID || item.id) === id))
      } catch (err) { error.value = err.message }
    }
    async function changeTaskPage(next) {
      const totalPages = Math.max(1, Math.ceil(taskTotal.value / taskPageSize.value))
      if (next < 1 || next > totalPages || next === taskPage.value) return
      taskPage.value = next
      await loadTaskPage()
    }
    async function changeTaskPageSize(size) {
      const nextSize = Number(size)
      if (![10, 20, 50, 100].includes(nextSize) || nextSize === taskPageSize.value) return
      taskPageSize.value = nextSize
      taskPage.value = 1
      selectedTaskIDs.value = []
      await loadTaskPage()
    }
    function canDeleteTask(item) {
      return ['success', 'completed', 'succeeded', 'failed', 'error'].includes(state(item?.Status || item?.status))
    }
    async function deleteTaskRecord(item) {
      const id = item?.ID || item?.id
      if (!id) return
      if (!canDeleteTask(item)) { error.value = '仅允许删除已成功或已失败的任务记录。'; return }
      if (!confirm(`确认删除任务记录 ${id}？\n任务步骤和完整日志将同时删除，此操作不可恢复。`)) return
      try {
        await api(`/tasks?id=${encodeURIComponent(id)}`, { method: 'DELETE' })
        if ((taskObject()?.ID || taskObject()?.id) === id) clearTaskDetail()
        if (taskPage.value > 1 && data.value.tasks.length <= 1) taskPage.value -= 1
        notice.value = `任务记录 ${id} 已删除。`
        await refresh()
      } catch (err) { error.value = err.message }
    }
    function toggleTaskSelection(item) {
      const id = item?.ID || item?.id
      if (!id || !canDeleteTask(item)) return
      selectedTaskIDs.value = selectedTaskIDs.value.includes(id) ? selectedTaskIDs.value.filter(value => value !== id) : [...selectedTaskIDs.value, id]
    }
    function selectCurrentTaskPage() {
      const ids = filteredTasks.value.filter(canDeleteTask).map(item => item.ID || item.id)
      selectedTaskIDs.value = ids.length && ids.every(id => selectedTaskIDs.value.includes(id)) ? [] : ids
    }
    async function deleteTaskRecords(allFiltered = false) {
      const count = allFiltered ? taskTotal.value : selectedTaskIDs.value.length
      if (!count) { error.value = '请先选择需要清理的已完成任务。'; return }
      const label = allFiltered ? `当前筛选条件下的 ${count} 条已完成任务` : `选中的 ${count} 条任务`
      if (!confirm(`确认批量清理${label}？\n父任务下的执行子任务、步骤和日志会一并删除，此操作不可恢复。`)) return
      try {
        const result = await api('/tasks', { method: 'DELETE', body: JSON.stringify({ task_ids: allFiltered ? [] : selectedTaskIDs.value, all_filtered: allFiltered, keyword: taskKeyword.value.trim(), status: taskFilter.value, type: taskTypeFilter.value }) })
        selectedTaskIDs.value = []
        notice.value = `已清理 ${result.deleted || 0} 条任务记录${result.failed ? `，${result.failed} 条因仍在执行等原因未清理` : ''}。`
        await refresh()
      } catch (err) { error.value = err.message }
    }
    const selectedTaskEvents = computed(() => {
      const events = taskEvents(selectedTaskFlowDetail.value || selectedTaskDetail.value)
      const stepID = selectedTaskStep.value?.ID || selectedTaskStep.value?.id
      if (!stepID) return events
      const matched = events.filter(event => (event.StepID || event.step_id) === stepID)
      return matched.length ? matched : []
    })
    let taskRefreshTimer = null

    function taskObject(detail = selectedTaskDetail.value) { return detail?.task || detail?.Task || null }
    function taskSteps(detail = selectedTaskDetail.value) { return asList(detail?.steps ?? detail?.Steps) }
    function taskEvents(detail = selectedTaskDetail.value) { return asList(detail?.events ?? detail?.Events) }
    function taskChildren(detail = selectedTaskDetail.value) { return asList(detail?.children ?? detail?.Children) }
    function taskChildDetails(detail = selectedTaskDetail.value) { return asList(detail?.child_details ?? detail?.ChildDetails) }
    function taskFlowDetails(detail = selectedTaskDetail.value) {
      if (!detail) return []
      return [detail, ...taskChildDetails(detail).flatMap(child => taskFlowDetails(child))]
    }
    function taskFlowStepCount() { return taskFlowDetails().reduce((total, detail) => total + taskSteps(detail).length, 0) }
    function taskFlowSuccessCount() { return taskFlowDetails().reduce((total, detail) => total + taskSteps(detail).filter(step => ['success', 'completed'].includes(state(step.Status || step.status))).length, 0) }
    function selectedTaskFlowView() {
      const selectedID = taskObject(selectedTaskFlowDetail.value)?.ID || taskObject(selectedTaskFlowDetail.value)?.id
      return taskFlowDetails().find(detail => (taskObject(detail)?.ID || taskObject(detail)?.id) === selectedID) || selectedTaskDetail.value
    }
    function taskFlowTabLabel(detail, index) {
      if (!index) return '总流程'
      return detail?.machine_name || detail?.MachineName || taskObject(detail)?.MachineID || taskObject(detail)?.machine_id || `执行任务 ${index}`
    }
    function selectTaskFlow(detail) {
      selectedTaskFlowDetail.value = detail
      const steps = taskSteps(detail)
      selectedTaskStep.value = steps.find(step => ['failed', 'error'].includes(state(step.Status || step.status))) || steps.find(step => state(step.Status || step.status) === 'running') || steps[0] || null
    }
    function taskTypeLabel(value) { return ({ exec: '远程命令执行', collect_machine_info: '采集机器信息', collect_static_info: '采集静态信息', mysql_install: '安装 MySQL', mysql_uninstall: '卸载 MySQL', mysql_topology: '配置 MySQL 拓扑', mysql_upgrade: '升级 MySQL', mysql_cluster_bootstrap: '批量安装并初始化架构', batch_operation: '批量业务操作', architecture_adjustment: '集群架构切换', agent_recovery: 'Agent 恢复', platform_operation: '平台操作' })[String(value || '').toLowerCase()] || value || '任务详情' }
    function taskSpec(item = taskObject()) {
      const raw = item?.SpecJSON || item?.spec_json
      if (!raw) return {}
      if (typeof raw === 'object') return raw
      try { return JSON.parse(raw) || {} } catch (_) { return {} }
    }
    function relatedTaskIDs(item = taskObject()) { return asList(taskSpec(item).related_task_ids) }
    function taskTitle(item) {
      const type = String(item?.Type || item?.type || '').toLowerCase()
      const rawSpec = item?.SpecJSON || item?.spec_json
      let spec = {}
      try { spec = typeof rawSpec === 'string' ? JSON.parse(rawSpec) : (rawSpec || {}) } catch (_) { spec = {} }
      const port = spec.port ? ` · ${spec.port} 端口` : ''
      return ({
        exec: spec.display_name || '执行远程命令',
        collect_machine_info: '采集机器运行信息',
        collect_static_info: '采集机器静态资产',
        mysql_install: `部署 MySQL${port}`,
        mysql_uninstall: `卸载 MySQL${port}`,
        mysql_topology: '采集 MySQL 拓扑',
        mysql_cluster_bootstrap: spec.display_name || '批量安装并初始化架构',
        batch_operation: spec.display_name || '批量业务操作',
        architecture_adjustment: '执行集群架构切换',
        platform_operation: spec.display_name || '平台操作'
      })[type] || taskTypeLabel(type)
    }
    function taskStatusLabel(value) { return ({ pending: '等待执行', confirming: '确认状态', executing: '正在拉起', waiting_heartbeat: '等待心跳', sent: '已下发', running: '执行中', success: '执行成功', completed: '执行成功', succeeded: '恢复成功', failed: '执行失败', error: '执行失败', suppressed: '自动恢复已抑制' })[state(value)] || value || '未知' }
    function stepStatusLabel(value) { return ({ pending: '等待', running: '执行中', success: '成功', completed: '成功', failed: '失败', error: '失败' })[state(value)] || value || '未知' }
    function elapsed(start, end) {
      if (!start) return '—'
      const milliseconds = Math.max(0, new Date(end || Date.now()).getTime() - new Date(start).getTime())
      const seconds = milliseconds / 1000
      if (seconds < 1) return `${Math.round(milliseconds)}ms`
      if (seconds < 60) return `${seconds.toFixed(seconds < 10 ? 2 : 1)}秒`
      const minutes = Math.floor(seconds / 60); const remain = Math.round(seconds % 60)
      return `${minutes}分${remain}秒`
    }
    function safeLog(value) {
      return String(value || '').replace(/((?:root_)?password["']?\s*[:=]\s*)[^\s,;}]+/gi, '$1******').replace(/(-p)([^\s]+)/g, '$1******')
    }
    function chooseTaskStep(step, detail = selectedTaskDetail.value) {
      const name = step?.StepName || step?.step_name
      const relatedID = step?.RelatedTaskID || step?.related_task_id || step?.Message || step?.message
      if (name === '关联执行任务' && String(relatedID || '').trim()) {
        taskDetailStack.value.push({ detail: selectedTaskDetail.value, step: selectedTaskStep.value, flowDetail: selectedTaskFlowDetail.value, context: taskReturnContext.value })
        openTaskDetail({ id: relatedID, _related: true })
        return
      }
      selectedTaskStep.value = step
      selectedTaskFlowDetail.value = detail
    }
    function selectCurrentTaskStep() {
      const details = taskFlowDetails()
      const candidates = details.flatMap(detail => taskSteps(detail).map(step => ({ detail, step })))
      const childFailure = ['failed', 'error'].includes(state(taskObject()?.Status || taskObject()?.status)) ? candidates.find(item => item.detail !== selectedTaskDetail.value && ['failed', 'error'].includes(state(item.step.Status || item.step.status))) : null
      const selected = childFailure || candidates.find(item => state(item.step.Status || item.step.status) === 'running') || candidates.find(item => ['failed', 'error'].includes(state(item.step.Status || item.step.status))) || candidates[candidates.length - 1]
      selectedTaskStep.value = selected?.step || null
      selectedTaskFlowDetail.value = selected?.detail || selectedTaskDetail.value
    }
    function stopTaskPolling() { if (taskRefreshTimer) { clearInterval(taskRefreshTimer); taskRefreshTimer = null } }
    function startTaskPolling() {
      stopTaskPolling()
      if (['pending', 'sent', 'running', 'confirming', 'executing', 'waiting_heartbeat'].includes(state(taskObject()?.Status || taskObject()?.status))) taskRefreshTimer = setInterval(() => refreshSelectedTaskDetail(true), 3000)
    }
    function applyTaskDetail(detail, preserveStep = false) {
      const previousID = preserveStep ? (selectedTaskStep.value?.ID || selectedTaskStep.value?.id) : ''
      selectedTaskDetail.value = detail
      const candidates = taskFlowDetails(detail).flatMap(flowDetail => taskSteps(flowDetail).map(step => ({ detail: flowDetail, step })))
      const childFailure = ['failed', 'error'].includes(state(taskObject(detail)?.Status || taskObject(detail)?.status)) ? candidates.find(item => item.detail !== detail && ['failed', 'error'].includes(state(item.step.Status || item.step.status))) : null
      const selected = candidates.find(item => (item.step.ID || item.step.id) === previousID) || childFailure || candidates.find(item => state(item.step.Status || item.step.status) === 'running') || candidates.find(item => ['failed', 'error'].includes(state(item.step.Status || item.step.status))) || candidates[0]
      selectedTaskStep.value = selected?.step || null
      selectedTaskFlowDetail.value = selected?.detail || detail
      startTaskPolling()
    }
    function openTaskChild(child) {
      taskDetailStack.value.push({ detail: selectedTaskDetail.value, step: selectedTaskStep.value, flowDetail: selectedTaskFlowDetail.value, context: taskReturnContext.value })
      openTaskDetail({ id: child?.ID || child?.id, _related: true })
    }
    function currentTaskReturnContext(item) {
      if (selectedTaskDetail.value && taskReturnContext.value) return { ...taskReturnContext.value }
      const hinted = item?._taskOrigin || item?.task_origin || {}
      return {
        active: hinted.active || active.value,
        clusterSection: hinted.clusterSection || hinted.cluster_section || data.value.clusterSection,
        instanceView: hinted.instanceView || hinted.instance_view || data.value.instanceView,
        mysqlView: hinted.mysqlView || hinted.mysql_view || mysqlView.value
      }
    }
    function showTaskDetail(detail, context) {
      taskReturnContext.value = context
      applyTaskDetail(detail)
      active.value = 'tasks'
    }
    function recoveryTaskDetail(item) {
      const status = state(item?.status || item?.Status)
      const active = ['pending', 'sent', 'running', 'confirming', 'executing', 'waiting_heartbeat'].includes(status)
      const completed = status === 'succeeded'
      const failed = ['failed', 'error', 'suppressed'].includes(status)
      const stepStatus = (done) => completed ? 'success' : failed ? 'failed' : done ? 'success' : active ? 'running' : 'pending'
      const task = { ...item, Type: 'agent_recovery', Status: status, ProgressPercent: completed || failed ? 100 : 50, StartedAt: item?.created_at || item?.CreatedAt, FinishedAt: completed || failed ? (item?.updated_at || item?.UpdatedAt) : '' }
      const steps = [
        { id: `${task.id || task.ID}-request`, step_name: 'manual_recovery_request', status: 'success', message: '已接受手动拉起请求', started_at: task.StartedAt, finished_at: task.StartedAt },
        { id: `${task.id || task.ID}-start`, step_name: 'start_agent', status: stepStatus(completed || failed), message: failed ? 'Agent 拉起未完成' : completed ? 'Agent 服务已拉起' : '正在通过 SSH 拉起 Agent 服务', started_at: task.StartedAt, finished_at: task.FinishedAt },
        { id: `${task.id || task.ID}-heartbeat`, step_name: 'wait_heartbeat', status: stepStatus(completed), message: failed ? (item?.last_error || item?.LastError || '等待 Agent 心跳超时') : completed ? '已收到 Agent 心跳，恢复完成' : '等待 Agent 心跳回连', started_at: task.StartedAt, finished_at: task.FinishedAt }
      ]
      const error = item?.last_error || item?.LastError
      return { task, steps, events: error ? [{ id: `${task.id || task.ID}-error`, event_type: 'error', content: error, created_at: task.FinishedAt || task.StartedAt }] : [], machine_ip: item?.machine_ip || item?.MachineIP }
    }
    async function openTaskDetail(item) {
      const embeddedTask = item?.task || item?.Task
      const id = embeddedTask?.ID || embeddedTask?.id || item?.ID || item?.id
      if (!id) return
      error.value = ''
      const context = currentTaskReturnContext(item)
      if (item?.recovery_task || String(item?.type || item?.Type).toLowerCase() === 'agent_recovery') { showTaskDetail(recoveryTaskDetail(item), context); return }
      if (embeddedTask && (Array.isArray(item?.steps) || Array.isArray(item?.Steps))) { showTaskDetail(item, context); return }
      try { showTaskDetail(await api(`/tasks?id=${encodeURIComponent(id)}`), context) } catch (err) { if (item?._related) taskDetailStack.value.pop(); error.value = err.message }
    }
    async function refreshSelectedTaskDetail(silent = false) {
      const id = taskObject()?.ID || taskObject()?.id
      if (!id) return
      try {
        if (String(taskObject()?.Type || taskObject()?.type).toLowerCase() === 'agent_recovery') {
          const tasks = asList(await api('/agents/recovery-tasks'), 'tasks')
          const task = tasks.find(entry => (entry.id || entry.ID) === id)
          if (task) applyTaskDetail(recoveryTaskDetail({ ...task, recovery_task: true }), true)
          return
        }
        applyTaskDetail(await api(`/tasks?id=${encodeURIComponent(id)}`), true)
      } catch (err) { if (!silent) error.value = err.message }
    }
    function clearTaskDetail() {
      stopTaskPolling()
      selectedTaskDetail.value = null
      selectedTaskStep.value = null
      selectedTaskFlowDetail.value = null
      taskReturnContext.value = null
      taskDetailStack.value = []
    }
    function closeTaskDetail() {
      if (taskDetailStack.value.length) {
        const parent = taskDetailStack.value.pop()
        taskReturnContext.value = parent.context
        selectedTaskDetail.value = parent.detail
        selectedTaskStep.value = parent.step
        selectedTaskFlowDetail.value = parent.flowDetail || parent.detail
        startTaskPolling()
        return
      }
      const context = taskReturnContext.value
      clearTaskDetail()
      if (!context) return
      if (context.clusterSection) data.value.clusterSection = context.clusterSection
      if (context.instanceView) data.value.instanceView = context.instanceView
      if (context.mysqlView) mysqlView.value = context.mysqlView
      active.value = context.active || 'tasks'
    }

    async function refresh() {
      loading.value = true; error.value = ''
      try {
        const [manager, machines, credentials, clusters, agents, agentCandidates, mysqlInstances, mysqlPackages, accountPresets, tasks, taskStats, recentTaskResponse, recovery, packages] = await Promise.all([
          api('/manager/status').catch(err => ({ ...data.value.manager, running: false, unreachable: true, last_error: err.message, last_checked_at: new Date().toISOString() })), api(`/machines?page=${machinePage.value}&page_size=${pageSize}&keyword=${encodeURIComponent(machineKeyword.value)}&cluster=${encodeURIComponent(machineClusterFilter.value)}`), api(`/ssh-credentials?page=${credentialPage.value}&page_size=${pageSize}`), api('/clusters'), api(`/agents?page=${data.value.agentPage}&page_size=${data.value.agentPageSize}&keyword=${encodeURIComponent(data.value.agentKeyword)}&status=${encodeURIComponent(data.value.agentStatus)}&version=${encodeURIComponent(data.value.agentVersion)}`), api('/agents?pending=true'), api('/mysql/instances'), api('/mysql/packages'), api('/mysql/account-presets'), api(taskListPath()), api('/tasks?stats=true'), api('/tasks?limit=6'), api('/agents/recovery-tasks').catch(() => []), api('/packages').catch(() => ({ items: [], settings: {} }))
        ])
        machineTotal.value = machines.total || 0; credentialTotal.value = credentials.total || 0
        const agentItems = asList(agents, 'agents')
        taskTotal.value = Number(tasks?.total || asList(tasks, 'tasks').length)
        recentTaskItems.value = asList(recentTaskResponse, 'tasks')
        data.value = { ...data.value, manager: manager || { running: false, config: {} }, machines: asList(machines), credentials: asList(credentials), clusters: asList(clusters, 'clusters'), agents: agentItems, agentTotal: agents?.total || agentItems.length, agentCandidates: asList(agentCandidates, 'agents'), mysqlInstances: asList(mysqlInstances, 'instances'), mysqlPackages: asList(mysqlPackages, 'packages'), accountPresets: asList(accountPresets, 'presets'), tasks: asList(tasks, 'tasks'), taskStats: taskStats || data.value.taskStats, recovery: asList(recovery, 'tasks'), packageItems: asList(packages), packageSettings: packages?.settings || {} }
        packageItems.value = packages.items || []
        packageSettings.value = packages.settings || { categories: [], supported_architectures: [] }
        managerForm.value = { ...(manager?.config || {}) }
        await loadClusterPage()
      } catch (err) { error.value = err.message }
      finally { loading.value = false }
    }
    async function refreshManagerStatus() {
      try {
        const status = await api('/manager/status')
        data.value.manager = { ...status, unreachable: false, last_error: '', last_checked_at: new Date().toISOString() }
      } catch (err) {
        data.value.manager = { ...data.value.manager, running: false, unreachable: true, last_error: err.message, last_checked_at: new Date().toISOString() }
      }
    }
    async function loadUpgrades(silent = false) {
      try {
        const [overview, jobs] = await Promise.all([api('/upgrades/overview'), api('/upgrades/jobs')])
        upgradeOverview.value = overview || upgradeOverview.value
        upgradeJobs.value = asList(jobs)
      } catch (err) { if (!silent) error.value = err.message }
    }
    async function loadUpgradeAgents(page = upgradeAgentPage.value, silent = false) {
      upgradeAgentLoading.value = true
      try {
        const query = new URLSearchParams({ page: String(page), page_size: String(upgradeAgentPageSize), keyword: upgradeAgentKeyword.value, status: upgradeAgentStatus.value, version: upgradeAgentVersion.value })
        const response = await api(`/agents?${query}`)
        upgradeAgents.value = asList(response, 'items', 'agents')
        upgradeAgentTotal.value = Number(response?.total || upgradeAgents.value.length)
        upgradeAgentPage.value = Number(response?.page || page)
      } catch (err) { if (!silent) error.value = err.message }
      finally { upgradeAgentLoading.value = false }
    }
    function searchUpgradeAgents() {
      upgradeAgentPage.value = 1
      loadUpgradeAgents(1)
    }
    function changeUpgradeAgentPage(offset) {
      const page = upgradeAgentPage.value + offset
      if (page < 1 || (offset > 0 && upgradeAgentPage.value * upgradeAgentPageSize >= upgradeAgentTotal.value)) return
      loadUpgradeAgents(page)
    }
    async function startManagerUpgrade() {
      if (!upgradeForm.value.manager_package || upgradeSubmitting.value) return
      if (!confirm('Manager 将原子替换当前程序并自动重启。数据库和配置不会改动，是否继续？')) return
      upgradeSubmitting.value = true
      try {
        const job = await api('/upgrades/manager', { method: 'POST', body: JSON.stringify({ package_name: upgradeForm.value.manager_package }) })
        notice.value = `Manager 升级任务 ${job.id} 已启动`
        await loadUpgrades()
      } catch (err) { error.value = err.message } finally { upgradeSubmitting.value = false }
    }
    async function startAgentUpgrade() {
      if (!upgradeForm.value.agent_package || !upgradeForm.value.targets.length || upgradeSubmitting.value) return
      upgradeSubmitting.value = true
      try {
        const job = await api('/upgrades/agent', { method: 'POST', body: JSON.stringify({ package_name: upgradeForm.value.agent_package, targets: upgradeForm.value.targets }) })
        notice.value = `Agent 升级任务 ${job.id} 已启动`
        await loadUpgrades()
      } catch (err) { error.value = err.message } finally { upgradeSubmitting.value = false }
    }
    function componentVersionParts(value) {
      const normalized = String(value || '').trim().replace(/^[vV]/, '').split(/[+-]/)[0]
      const parts = normalized.split('.').map(Number)
      return normalized && parts.length >= 2 && parts.every(Number.isInteger) ? parts : null
    }
    function compareUpgradeVersions(current, target) {
      const left = componentVersionParts(current)
      const right = componentVersionParts(target)
      if (!left || !right) return null
      for (let index = 0; index < Math.max(left.length, right.length); index++) {
        if ((left[index] || 0) < (right[index] || 0)) return -1
        if ((left[index] || 0) > (right[index] || 0)) return 1
      }
      return 0
    }
    function selectedManagerUpgradePackage() { return upgradeOverview.value.manager_packages.find(item => item.name === upgradeForm.value.manager_package) }
    function selectedAgentUpgradePackage() { return upgradeOverview.value.agent_packages.find(item => item.name === upgradeForm.value.agent_package) }
    function upgradeAgentRelation(agent) {
      const target = selectedAgentUpgradePackage()?.version
      const comparison = compareUpgradeVersions(agent.version || agent.Version, target)
      if (!target) return 'unselected'
      if (comparison === null) return 'unknown'
      if (comparison < 0) return 'upgrade'
      if (comparison > 0) return 'downgrade'
      return 'current'
    }
    function upgradeAgentSelectable(agent) {
      return upgradeAgentRelation(agent) === 'upgrade' && ['online', 'running', 'installed'].includes(state(agent.heartbeat_state || agent.install_state))
    }
    function upgradeCurrentPageSelected() {
      const selectable = upgradeAgents.value.filter(upgradeAgentSelectable)
      const targets = new Set(upgradeForm.value.targets)
      return selectable.length > 0 && selectable.every(agent => targets.has(agent.ip))
    }
    function toggleUpgradeCurrentPage() {
      const selectable = upgradeAgents.value.filter(upgradeAgentSelectable)
      const targets = new Set(upgradeForm.value.targets)
      if (upgradeCurrentPageSelected()) selectable.forEach(agent => targets.delete(agent.ip))
      else selectable.forEach(agent => targets.add(agent.ip))
      upgradeForm.value.targets = [...targets]
    }
    function agentManagementUpgradePageSelected() {
      const selectable = data.value.agents.filter(upgradeAgentSelectable)
      const targets = new Set(upgradeForm.value.targets)
      return selectable.length > 0 && selectable.every(agent => targets.has(agent.IP || agent.ip))
    }
    function toggleAgentManagementUpgradePage() {
      const selectable = data.value.agents.filter(upgradeAgentSelectable)
      const targets = new Set(upgradeForm.value.targets)
      if (agentManagementUpgradePageSelected()) selectable.forEach(agent => targets.delete(agent.IP || agent.ip))
      else selectable.forEach(agent => targets.add(agent.IP || agent.ip))
      upgradeForm.value.targets = [...targets]
    }
    function upgradeAgentPackageChanged() {
      upgradeForm.value.targets = []
    }
    function replaceDetectedAgent(view) {
      const ip = view.IP || view.ip
      data.value.agents = data.value.agents.map(item => (item.IP || item.ip) === ip ? { ...item, ...view } : item)
      upgradeAgents.value = upgradeAgents.value.map(item => (item.IP || item.ip) === ip ? { ...item, ...view } : item)
      if (selectedAgent.value && (selectedAgent.value.IP || selectedAgent.value.ip) === ip) selectedAgent.value = { ...selectedAgent.value, ...view }
    }
    async function detectAgentVersion(item, silent = false) {
      const ip = item.IP || item.ip
      if (!ip || agentVersionDetecting.value[ip]) return false
      agentVersionDetecting.value = { ...agentVersionDetecting.value, [ip]: true }
      try {
        const view = await api('/agents/detect-version', { method: 'POST', body: JSON.stringify({ ip }) })
        replaceDetectedAgent(view)
        if (!silent) notice.value = `${item.Name || item.name || ip} 当前版本已识别为 ${view.version || view.Version}`
        return true
      } catch (err) {
        if (!silent) error.value = `${item.Name || item.name || ip}：${err.message}`
        return false
      } finally {
        const next = { ...agentVersionDetecting.value }
        delete next[ip]
        agentVersionDetecting.value = next
      }
    }
    async function detectUnknownAgentVersions() {
      if (agentVersionBatchDetecting.value) return
      const pending = data.value.agents.filter(item => !(item.Version || item.version))
      if (!pending.length) { notice.value = '当前页 Agent 版本均已识别。'; return }
      agentVersionBatchDetecting.value = true
      let success = 0
      try {
        for (let index = 0; index < pending.length; index += 5) {
          const results = await Promise.all(pending.slice(index, index + 5).map(item => detectAgentVersion(item, true)))
          success += results.filter(Boolean).length
        }
        notice.value = `当前页版本检测完成：成功 ${success} 台，失败 ${pending.length - success} 台`
        await loadUpgrades(true)
      } finally { agentVersionBatchDetecting.value = false }
    }
    function upgradeStatusLabel(value) { return ({ pending: '等待执行', running: '执行中', success: '升级成功', failed: '升级失败' })[state(value)] || value }
    async function refreshAgentResources(silent = false) {
      if (agentResourceRefreshing.value) return
      agentResourceRefreshing.value = true
      try {
        const response = await api(`/agents?page=${data.value.agentPage}&page_size=${data.value.agentPageSize}&keyword=${encodeURIComponent(data.value.agentKeyword)}&status=${encodeURIComponent(data.value.agentStatus)}&version=${encodeURIComponent(data.value.agentVersion)}`)
        const items = asList(response, 'agents')
        data.value.agents = items
        data.value.agentTotal = response?.total || items.length
        if (selectedAgent.value) {
          const selectedIP = selectedAgent.value.IP || selectedAgent.value.ip
          selectedAgent.value = items.find(item => (item.IP || item.ip) === selectedIP) || selectedAgent.value
        }
        agentResourceUpdatedAt.value = new Date().toISOString()
      } catch (err) {
        if (!silent) error.value = err.message
      } finally {
        agentResourceRefreshing.value = false
      }
    }
    function stopAgentResourceRefresh() {
      if (agentResourceRefreshTimer) clearInterval(agentResourceRefreshTimer)
      agentResourceRefreshTimer = null
    }
    function startAgentResourceRefresh() {
      stopAgentResourceRefresh()
      if (active.value !== 'agents') return
      const seconds = Math.max(15, Number(agentResourceRefreshSeconds.value) || 30)
      agentResourceRefreshTimer = setInterval(() => refreshAgentResources(true), seconds * 1000)
    }
    async function searchMachines() {
      machinePage.value = 1
      selectedMachineIDs.value = []
      await refresh()
    }
    function toggleCurrentMachinePage() {
      const ids = data.value.machines.map(item => item.ID || item.id)
      const allSelected = ids.length && ids.every(id => selectedMachineIDs.value.includes(id))
      selectedMachineIDs.value = allSelected
        ? selectedMachineIDs.value.filter(id => !ids.includes(id))
        : [...new Set([...selectedMachineIDs.value, ...ids])]
    }
    async function changeMachinePage(delta) {
      const next = machinePage.value + delta
      if (next < 1 || (next - 1) * pageSize >= machineTotal.value) return
      machinePage.value = next
      await refresh()
    }
    function openBatchOnboard() {
      batchOnboardRows.value = [{ name: '', ip: '' }, { name: '', ip: '' }]
      batchOnboardShared.value = { ssh_port: 22, ssh_user: 'root', ssh_password: '', credential_id: '', preserve_agent: true, preserve_mysql: true, concurrent: true, concurrency: 3 }
      batchOnboardResults.value = []
      showBatchOnboard.value = true
    }
    function addBatchOnboardRow() { batchOnboardRows.value.push({ name: '', ip: '' }) }
    function removeBatchOnboardRow(index) { if (batchOnboardRows.value.length > 1) batchOnboardRows.value.splice(index, 1) }
    function batchOnboardCredentialChanged() {
      const credential = data.value.credentials.find(item => (item.id || item.ID) === batchOnboardShared.value.credential_id)
      if (credential) batchOnboardShared.value.ssh_user = credential.ssh_user || credential.SSHUser || 'root'
    }
    function updateBulkResult(target, index, patch) {
      target.value[index] = { ...target.value[index], ...patch }
    }
    function updateBulkStep(target, index, key, state, detail = '') {
      const item = target.value[index]
      updateBulkResult(target, index, { steps: item.steps.map(step => step.key === key ? { ...step, state, detail: detail || step.detail } : step) })
    }
    async function runWithConcurrency(items, concurrency, worker) {
      let cursor = 0
      const count = Math.max(1, Math.min(Number(concurrency) || 1, items.length))
      await Promise.all(Array.from({ length: count }, async () => {
        while (cursor < items.length) {
          const index = cursor++
          await worker(items[index], index)
        }
      }))
    }
    function onboardResultSteps() {
      return [
        { key: 'precheck', title: 'SSH 与环境预检查', state: 'pending', detail: '检查连接、systemd、磁盘和已有组件' },
        { key: 'register', title: '登记机器与建立互信', state: 'pending', detail: '写入平台记录并建立 SSH 管理通道' },
        { key: 'agent', title: '部署或接管 Agent', state: 'pending', detail: '部署新 Agent，或保留并重新登记已有 Agent' },
        { key: 'finish', title: '完成纳管', state: 'pending', detail: '同步机器、Agent 与 MySQL 记录' }
      ]
    }
    async function submitBatchOnboard() {
      const rows = batchOnboardRows.value.map(item => ({ name: item.name.trim(), ip: item.ip.trim() })).filter(item => item.name || item.ip)
      if (!rows.length || rows.some(item => !item.name || !item.ip)) { error.value = '请完整填写每台机器的名称和 IP。'; return }
      batchOnboardRunning.value = true; error.value = ''; notice.value = ''
      batchOnboardResults.value = rows.map(item => ({ ...item, status: 'pending', message: '等待调度', steps: onboardResultSteps() }))
      const shared = batchOnboardShared.value
      const concurrency = shared.concurrent ? Math.min(10, Math.max(2, Number(shared.concurrency) || 3)) : 1
      await runWithConcurrency(rows, concurrency, async (row, index) => {
        const { concurrent, concurrency: ignoredConcurrency, ...connection } = shared
        const payload = { ...connection, name: row.name, ip: row.ip }
        updateBulkResult(batchOnboardResults, index, { status: 'running', message: '正在执行 SSH 与环境预检查' })
        updateBulkStep(batchOnboardResults, index, 'precheck', 'running')
        try {
          const precheck = await api('/machines/precheck', { method: 'POST', body: JSON.stringify(payload) })
          const componentRisk = precheck.agent_detected || precheck.mysql_detected
          if (precheck.warning) throw new Error(precheck.warning)
          const preservationReady = (!precheck.agent_detected || shared.preserve_agent) && (!precheck.mysql_detected || shared.preserve_mysql)
          if (componentRisk && !preservationReady) throw new Error('检测到已有组件；请选择保留已有 Agent/MySQL，或先执行清理。')
          updateBulkStep(batchOnboardResults, index, 'precheck', 'success', componentRisk ? `检查通过，检测到已有组件${(precheck.mysql_residues || []).length ? '：'+precheck.mysql_residues.join('、') : ''}，将按选择处理` : 'SSH、systemd、磁盘与组件检查通过')
          updateBulkStep(batchOnboardResults, index, 'register', 'running')
          updateBulkResult(batchOnboardResults, index, { message: '正在登记机器并建立 SSH 管理通道' })
          const result = await api('/machines', { method: 'POST', body: JSON.stringify({
            ...payload,
            preserve_agent: !!(precheck.agent_detected && shared.preserve_agent),
            preserve_mysql: !!(precheck.mysql_detected && shared.preserve_mysql)
          }) })
          updateBulkStep(batchOnboardResults, index, 'register', 'success')
          updateBulkStep(batchOnboardResults, index, 'agent', 'running')
          updateBulkResult(batchOnboardResults, index, { message: precheck.agent_detected ? '正在接管已有 Agent' : '正在部署 Agent' })
          if (!precheck.agent_detected) await api('/agents/retry-install', { method: 'POST', body: JSON.stringify({ ip: result.ip || result.IP || row.ip, ssh_user: shared.ssh_user, ssh_password: shared.ssh_password }) })
          updateBulkStep(batchOnboardResults, index, 'agent', 'success', precheck.agent_detected ? '已有 Agent 已保留并重新登记' : 'Agent 已安装、配置并启动')
          updateBulkStep(batchOnboardResults, index, 'finish', 'success')
          updateBulkResult(batchOnboardResults, index, { status: 'success', message: precheck.agent_detected ? '重新纳管完成，已有 Agent/MySQL 已保留' : '纳管完成，Agent 已部署' })
        } catch (err) {
          const item = batchOnboardResults.value[index]
		  const registeredButAgentFailed = /machine registered but preserved components could not be adopted|机器已登记|保留 Agent 接管失败/i.test(String(err.message || ''))
          if (registeredButAgentFailed) {
            updateBulkStep(batchOnboardResults, index, 'register', 'success', '机器已登记，SSH 管理通道已建立')
			updateBulkStep(batchOnboardResults, index, 'agent', 'failed', errorSummary(err.message, 360))
          } else {
            const runningKey = item.steps.find(step => step.state === 'running')?.key || 'precheck'
			updateBulkStep(batchOnboardResults, index, runningKey, 'failed', errorSummary(err.message, 360))
          }
          updateBulkResult(batchOnboardResults, index, { status: 'failed', message: err.message, steps: batchOnboardResults.value[index].steps.map(step => step.state === 'pending' ? { ...step, state: 'skipped' } : step) })
        }
      })
      const succeeded = batchOnboardResults.value.filter(item => item.status === 'success').length
      notice.value = `批量纳管完成：${succeeded}/${rows.length} 台成功。`
      if (succeeded !== rows.length) error.value = '部分机器纳管失败，请查看弹窗内结果。'
      batchOnboardRunning.value = false
      await refresh()
    }
    function bulkDeleteExpected() { return `${bulkDeleteForm.value.mode === 'detach' ? 'DETACH' : 'DELETE'} ${selectedMachineIDs.value.length} MACHINES` }
    function bulkDeleteClusterMembers() {
      const selected = new Set(selectedMachineIDs.value)
      return data.value.machines.filter(item => selected.has(item.ID || item.id) && String(item.Cluster || item.cluster || '').trim())
    }
    function bulkDeleteClusterSummary() {
      return bulkDeleteClusterMembers().map(item => `${item.Name || item.name}（${item.Cluster || item.cluster}）`).join('、')
    }
    function leaveBulkDeleteForClusters() {
      showBulkDelete.value = false
      active.value = 'clusters'
      data.value.clusterSection = 'overview'
      error.value = '请先在集群的“机器管理”中将所选机器移出集群，再执行删除。'
    }
    function openBulkDelete() {
      if (!selectedMachineIDs.value.length) { error.value = '请先选择需要删除的机器。'; return }
      bulkDeleteForm.value = { mode: 'cleanup', delete_mysql: true, delete_agent: true, concurrent: true, concurrency: 3, confirmation: '' }
      bulkDeleteResults.value = []
      showBulkDelete.value = true
    }
    function deleteResultSteps(detachOnly, form) {
      return [
		{ key: 'validate', title: '校验机器、集群与 SSH 通道', state: 'pending', detail: '确认机器已移出集群，并验证 Agent 卸载所需的 SSH 连通性与凭证' },
		{ key: 'mysql', title: '卸载 MySQL、清理数据并复检', state: detachOnly || !form.delete_mysql ? 'skipped' : 'pending', detail: '清理后再次检查服务、进程、配置、程序与数据路径' },
		{ key: 'agent', title: '卸载 Agent、systemd 并复检', state: detachOnly || !form.delete_agent ? 'skipped' : 'pending', detail: '停止服务和残留进程，删除 unit 与安装目录后执行实机验证' },
        { key: 'local', title: '清理平台关联记录', state: 'pending', detail: '清理心跳、任务、静态信息、实例和机器记录' }
      ]
    }
    function applyBulkDeleteResult(index, machineResult, failure, detachOnly) {
      if (!failure) {
        const sshPorts = machineResult?.mysql_ssh_ports || machineResult?.MySQLSSHPorts || []
        updateBulkResult(bulkDeleteResults, index, {
          status: 'success',
          message: detachOnly ? '已从平台直接剔除，未连接目标机器' : (sshPorts.length ? `清理并删除成功；MySQL ${sshPorts.join('、')} 通过 SSH 清理` : '清理并删除成功'),
          steps: bulkDeleteResults.value[index].steps.map(step => step.state === 'running' ? { ...step, state: 'success' } : step)
        })
        return
      }
      const message = String(failure)
      const failedKey = /卸载前检查|ssh 通道不可用|no route to host|connection refused|authentication|permission denied/i.test(message) ? 'validate' : /mysql/i.test(message) ? 'mysql' : /agent/i.test(message) ? 'agent' : 'local'
      let reachedFailure = false
      const steps = bulkDeleteResults.value[index].steps.map(step => {
        if (step.state === 'skipped') return step
        if (step.key === failedKey) { reachedFailure = true; return { ...step, state: 'failed', detail: errorSummary(message, 320) } }
        if (step.key === 'validate') return step
        if (reachedFailure) return { ...step, state: 'skipped' }
        return { ...step, state: 'success' }
      })
      updateBulkResult(bulkDeleteResults, index, { status: 'failed', message, steps })
    }
    async function submitBulkDeleteCompat(ids, form, detachOnly, concurrency) {
      await runWithConcurrency(ids, concurrency, async (id, index) => {
        try {
          const result = await api(`/machines/${encodeURIComponent(id)}`, {
            method: 'DELETE',
            body: JSON.stringify({ detach_only: detachOnly, delete_mysql: detachOnly ? false : form.delete_mysql, delete_agent: detachOnly ? false : form.delete_agent })
          })
          applyBulkDeleteResult(index, result, '', detachOnly)
        } catch (err) {
          applyBulkDeleteResult(index, null, err.message, detachOnly)
        }
      })
    }
    async function submitBulkDelete() {
      if (bulkDeleteClusterMembers().length) { error.value = '所选机器仍属于集群，请先移出集群后再删除。'; return }
      if (bulkDeleteForm.value.confirmation !== bulkDeleteExpected()) { error.value = '批量删除确认内容不匹配。'; return }
      const ids = selectedMachineIDs.value.slice()
      const form = { ...bulkDeleteForm.value }
      const detachOnly = form.mode === 'detach'
      bulkDeleteRunning.value = true
      bulkDeleteResults.value = ids.map(id => {
        const machine = data.value.machines.find(item => (item.ID || item.id) === id)
        return { id, name: machine?.Name || machine?.name || id, ip: machine?.IP || machine?.ip || '', status: 'pending', message: '等待调度', steps: deleteResultSteps(detachOnly, form) }
      })
      error.value = ''; notice.value = ''
      const concurrency = form.concurrent ? Math.min(10, Math.max(2, Number(form.concurrency) || 3)) : 1
      bulkDeleteResults.value.forEach((_, index) => {
        updateBulkStep(bulkDeleteResults, index, 'validate', 'success')
        updateBulkResult(bulkDeleteResults, index, { status: 'running', message: detachOnly ? '正在从平台剔除' : '正在执行远端清理与平台删除', steps: bulkDeleteResults.value[index].steps.map(step => step.state === 'pending' ? { ...step, state: 'running' } : step) })
      })
      let compatibilityFallback = false
      try {
        const batchResult = await api('/machines/batch-delete', { method: 'POST', body: JSON.stringify({ machine_ids: ids, detach_only: detachOnly, delete_mysql: detachOnly ? false : form.delete_mysql, delete_agent: detachOnly ? false : form.delete_agent, concurrency }) })
        const items = batchResult.items || []
        ids.forEach((id, index) => {
          const item = items.find(value => value.machine_id === id) || { error: '批量删除未返回该机器的执行结果' }
          applyBulkDeleteResult(index, item.result || {}, item.error || '', detachOnly)
        })
      } catch (err) {
        if (err.status === 404 || err.status === 405) {
          compatibilityFallback = true
          await submitBulkDeleteCompat(ids, form, detachOnly, concurrency)
        } else {
          bulkDeleteResults.value.forEach((_, index) => applyBulkDeleteResult(index, null, err.message, detachOnly))
        }
      }
      const succeeded = bulkDeleteResults.value.filter(item => item.status === 'success').length
	  const failedIDs = bulkDeleteResults.value.filter(item => item.status === 'failed').map(item => item.id)
	  const allSucceeded = failedIDs.length === 0
	  if (!allSucceeded) selectedMachineIDs.value = failedIDs
      notice.value = `批量删除完成：${succeeded}/${ids.length} 台成功。${compatibilityFallback ? ' 当前 Manager 使用兼容模式逐台执行。' : ''}`
      if (succeeded !== ids.length) error.value = '部分机器删除失败，失败项已保持选中。'
      bulkDeleteRunning.value = false
      await refresh()
	  if (allSucceeded) {
		selectedMachineIDs.value = []
		showBulkDelete.value = false
		bulkDeleteResults.value = []
		bulkDeleteForm.value = { mode: 'cleanup', delete_mysql: true, delete_agent: true, concurrent: true, concurrency: 3, confirmation: '' }
	  } else {
		bulkDeleteForm.value.confirmation = ''
	  }
    }
    async function onboard(skipComponentRisk = false) {
      notice.value = ''; error.value = ''
      expandedFlowErrors.value = {}
      onboardingFlow.value = [
        { title: '预检查目标机器', detail: '正在检查 SSH、系统权限、磁盘、已有 Agent 和 MySQL…', state: 'running', details: [
          { title: '验证 SSH 凭证与远程命令执行', state: 'running' }, { title: '检查 systemd 写入权限与磁盘空间', state: 'pending' }, { title: '检测已有 GMHA Agent', state: 'pending' }, { title: '检测已有 MySQL', state: 'pending' }
        ] },
        { title: '建立 SSH 互信并登记机器', detail: '等待预检查通过…', state: 'pending' },
        { title: '部署 GMHA Agent', detail: '空机器将自动部署 Agent；如失败会显示原因。', state: 'pending', details: [
          { title: '准备安装目录与系统权限', state: 'pending' },
          { title: '上传 Agent 二进制文件', state: 'pending' },
          { title: '写入 agent.yaml 与 systemd 服务单元', state: 'pending' },
          { title: '启动 gmha-agent 服务', state: 'pending' },
          { title: '等待 Agent 心跳注册', state: 'pending' }
        ] }
      ]
      onboardingDetected.value = { agent: false, mysql: false }
      showOnboard.value = false; showOnboardFlow.value = true
      canSkipPrecheck.value = false
      try {
        const precheck = await api('/machines/precheck', { method: 'POST', body: JSON.stringify(form.value) })
        const precheckStep = onboardingFlow.value[0]
        precheckStep.details[0].state = precheck.remote_command ? 'success' : 'error'
        precheckStep.details[1].state = precheck.systemd_ready ? 'success' : 'error'
        const componentRisk = precheck.agent_detected || precheck.mysql_detected
        onboardingDetected.value = { agent: !!precheck.agent_detected, mysql: !!precheck.mysql_detected }
        precheckStep.details[2].state = precheck.agent_detected ? 'error' : 'success'
        precheckStep.details[3].state = precheck.mysql_detected ? 'error' : 'success'
        const mysqlResidueDetail = (precheck.mysql_residues || []).length ? `（${precheck.mysql_residues.join('、')}）` : ''
        const risks = [precheck.warning, precheck.agent_detected ? '检测到已有 GMHA Agent，需要人工确认后处理。' : '', precheck.mysql_detected ? `检测到已有 MySQL 或安装残留${mysqlResidueDetail}，纳管流程不会自动修改数据库。` : ''].filter(Boolean)
        const blockingRisk = precheck.warning
        // 只要任一旧组件存在就提供处理入口；基础环境问题仍由后续流程阻断。
        canSkipPrecheck.value = componentRisk
        if (blockingRisk || (componentRisk && !skipComponentRisk)) {
          precheckStep.state = 'error'; precheckStep.detail = risks.join(' ')
          throw new Error(precheckStep.detail)
        }
        if (componentRisk && skipComponentRisk) precheckStep.detail = `检测到已有组件，将保留${precheck.agent_detected && form.value.preserve_agent ? ' Agent' : ''}${precheck.mysql_detected && form.value.preserve_mysql ? ' MySQL' : ''} 并重新接入平台。`
        precheckStep.state = 'success'; precheckStep.detail = `SSH=${precheck.identity || '已认证'}；系统=${precheck.os || '已识别'}；磁盘=${precheck.disk || '已检查'}`
        const retryMachine = data.value.machines.find(item => {
          const name = item.Name || item.name
          const status = state(item.Status || item.status)
          return name === form.value.name && ['agent_error', 'ssh_failed'].includes(status)
        })
        const result = await api('/machines', { method: 'POST', body: JSON.stringify({ ...form.value, preserve_agent: !!(precheck.agent_detected && form.value.preserve_agent), preserve_mysql: !!(precheck.mysql_detected && form.value.preserve_mysql), machine_id: retryMachine?.ID || retryMachine?.id || '' }) })
        onboardingResult.value = result
        onboardingFlow.value[1] = { title: '建立 SSH 互信并登记机器', detail: `SSH 互信已就绪，机器 ${result.name || form.value.name} 已纳入 Manager。`, state: 'success' }
        onboardingFlow.value[2].detail = precheck.agent_detected && form.value.preserve_agent ? '正在重新登记并连接已有 Agent…' : '安装器正在依次执行下列步骤…'; onboardingFlow.value[2].state = 'running'
        onboardingFlow.value[2].details[0].state = 'running'
        const install = precheck.agent_detected && form.value.preserve_agent ? { install_dir: '原安装目录', final_state: '已保留并重启' } : await api('/agents/retry-install', { method: 'POST', body: JSON.stringify({ ip: result.ip || result.IP || form.value.ip, ssh_user: form.value.ssh_user, ssh_password: form.value.ssh_password }) })
        onboardingFlow.value[2].details.forEach(item => { item.state = 'success' })
        onboardingFlow.value[2].detail = `Agent 已部署至 ${install.install_dir || '目标机器'}，状态：${install.final_state || '已提交'}`
        onboardingFlow.value[2].state = 'success'
        notice.value = componentRisk ? '机器已重新纳管，选择保留的 Agent 与 MySQL 已重新接入。' : '机器已纳管，并已提交 Agent 自动部署。'
        form.value = { name: '', ip: '', ssh_port: 22, ssh_user: 'root', ssh_password: '', credential_id: '', preserve_agent: true, preserve_mysql: true }; await refresh()
      } catch (err) {
        const running = onboardingFlow.value.find(item => item.state === 'running')
        if (running) {
          running.state = 'error'; running.detail = err.message
          const currentDetail = resolveFailedDetail(running, err.message)
          if (currentDetail) {
            currentDetail.state = 'error'
            currentDetail.error = err.message
            currentDetail.errorSummary = errorSummary(err.message)
          }
        }
        error.value = err.message
      }
    }
    async function cleanupTarget() {
      const target = form.value.ip
      if (!confirm(`高风险操作：将停止 ${target} 上的 gmha-agent、mysql/mysqld 服务，并删除 GMHA Agent 文件。MySQL 数据目录不会删除。是否继续？`)) return
      const phrase = prompt(`二次确认：请输入 CLEAN ${target} 执行清理`)
      if (phrase !== `CLEAN ${target}`) { error.value = '二次确认内容不匹配，已取消清理。'; return }
      try { await api('/machines/cleanup', { method:'POST', body:JSON.stringify({ ...form.value, confirm_phrase: phrase }) }); notice.value = '目标机器的旧 Agent 已清理，MySQL 服务已停止。请重新开始纳管。'; showOnboardFlow.value = false }
      catch (err) { error.value = `清理失败：${err.message}` }
    }
    function openAgentAction(type, item) {
      if (agentActionSubmitting.value) return
      const ip = item?.IP || item?.ip || ''
      const name = item?.Name || item?.name || ip
      const definitions = {
        recover: { title: '手动拉起 Agent', description: `将在 ${name}（${ip}）上创建恢复任务，并持续更新恢复结果。`, confirm: '创建恢复任务' },
        retry: { title: '重试安装 Agent', description: `重新向 ${name}（${ip}）下发 Agent 安装任务。请确认安装目录。`, confirm: '开始重试', inputLabel: 'Agent 安装目录', inputValue: item?.InstallDir || item?.install_dir || '/home/gmha/agent' },
        upgrade: { title: '升级 Agent', description: `将在 ${name}（${ip}）上执行 Agent 升级。升级期间会短暂重启 Agent 服务。`, confirm: '确认升级' },
        repair: { title: '修复 MySQL 采集', description: `将在 ${name}（${ip}）上修复 mysql-heartbeat.json 采集配置并创建修复任务。`, confirm: '提交修复' },
        uninstall: { title: '卸载 Agent', description: `高风险操作：将从 ${name}（${ip}）卸载 GMHA Agent，Manager 将不再接收其心跳。`, confirm: '确认卸载', danger: true, inputLabel: '二次确认口令', inputValue: '', expected: `UNINSTALL ${ip}` }
      }
      const definition = definitions[type]
      if (!definition) return
      agentActionReturnToDetail.value = showAgentDetail.value
      showAgentDetail.value = false
      agentActionInput.value = definition.inputValue || ''
      agentActionError.value = ''
      agentActionElapsed.value = 0
      agentActionDialog.value = { ...definition, type, ip, name }
    }
    function closeAgentAction(restoreDetail = true) {
      if (agentActionSubmitting.value) return
      clearInterval(agentActionElapsedTimer)
      agentActionElapsedTimer = null
      agentActionDialog.value = null
      agentActionInput.value = ''
      agentActionError.value = ''
      agentActionElapsed.value = 0
      if (restoreDetail && agentActionReturnToDetail.value && selectedAgent.value) showAgentDetail.value = true
      agentActionReturnToDetail.value = false
    }
    function recover(ip) { openAgentAction('recover', data.value.agents.find(item => (item.IP || item.ip) === ip) || { ip }) }
    async function performAgentRecovery(ip) {
      error.value = ''; notice.value = ''
      try {
        const task = await api('/agents/recover', { method: 'POST', body: JSON.stringify({ ip }) })
        data.value.manualRecovery = { ...task, status: task.status || 'pending' }
        notice.value = `已创建 ${ip} 的恢复任务，正在执行。`
        pollManualRecovery(task.id, ip)
      } catch (err) { error.value = err.message }
    }
    async function pollManualRecovery(taskID, ip) {
      try {
        const [agent, recovery] = await Promise.all([api(`/agents?ip=${encodeURIComponent(ip)}`), api('/agents/recovery-tasks')])
        const task = recovery.find(item => (item.id || item.ID) === taskID) || data.value.manualRecovery
        data.value = { ...data.value, agents: data.value.agents.map(item => (item.IP || item.ip) === ip ? agent : item), recovery, manualRecovery: { ...task, status: task.status || task.Status || 'pending' } }
        const status = String(task.status || task.Status || '').toLowerCase()
        if (['succeeded', 'failed', 'suppressed'].includes(status)) {
          if (status === 'succeeded') notice.value = `${ip} 的 Agent 已恢复在线。`
          return
        }
        setTimeout(() => pollManualRecovery(taskID, ip), 1000)
      } catch (err) { error.value = err.message }
    }
    async function showAgent(ip) {
      try { selectedAgent.value = await api(`/agents?ip=${encodeURIComponent(ip)}`); showAgentDetail.value = true }
      catch (err) { error.value = err.message }
    }
    function retryAgent(item) { openAgentAction('retry', item) }
    function upgradeAgent(item) { openAgentAction('upgrade', item) }
    function uninstallAgent(item) { openAgentAction('uninstall', item) }
    function repairMySQLAgentConfig(item) { openAgentAction('repair', item) }
    async function submitAgentAction() {
      const dialog = agentActionDialog.value
      if (!dialog || agentActionSubmitting.value) return
      error.value = ''; notice.value = ''
      agentActionError.value = ''
      if (dialog.expected && agentActionInput.value !== dialog.expected) { agentActionError.value = '二次确认口令不匹配，未执行操作。'; return }
      agentActionSubmitting.value = true
      const startedAt = Date.now()
      agentActionElapsed.value = 0
      clearInterval(agentActionElapsedTimer)
      agentActionElapsedTimer = setInterval(() => { agentActionElapsed.value = Math.floor((Date.now() - startedAt) / 1000) }, 1000)
      try {
        if (dialog.type === 'recover') await performAgentRecovery(dialog.ip)
        if (dialog.type === 'retry') { if (!agentActionInput.value.trim()) throw new Error('请输入 Agent 安装目录。'); await api('/agents/retry-install', { method: 'POST', body: JSON.stringify({ ip: dialog.ip, install_dir: agentActionInput.value.trim() }) }); notice.value = `已完成 ${dialog.ip} 的 Agent 重试安装。`; await refresh() }
        if (dialog.type === 'upgrade') { const result = await api('/agents/upgrade', { method: 'POST', body: JSON.stringify({ ip: dialog.ip }) }); notice.value = `${dialog.name}（${dialog.ip}）Agent 升级完成，服务已恢复在线。`; await refreshAgentResources(true); selectedAgent.value = data.value.agents.find(item => (item.IP || item.ip) === dialog.ip) || selectedAgent.value; active.value = 'agents'; if (result?.final_state && String(result.final_state).toLowerCase() !== 'online') throw new Error(result.last_error || 'Agent 升级后未恢复在线。') }
        if (dialog.type === 'repair') { const result = await api('/agents/repair-mysql-config', { method: 'POST', body: JSON.stringify({ ip: dialog.ip }) }); notice.value = `已提交 MySQL 采集配置修复任务：${result.task_id}`; await refresh() }
        if (dialog.type === 'uninstall') { await api('/agents/uninstall', { method: 'POST', body: JSON.stringify({ ip: dialog.ip }) }); notice.value = `已卸载 ${dialog.ip} 上的 Agent。`; showAgentDetail.value = false; await refresh() }
        agentActionSubmitting.value = false
        closeAgentAction(!['uninstall', 'upgrade'].includes(dialog.type))
        if (dialog.type === 'upgrade') window.scrollTo({ top: 0, behavior: 'smooth' })
      } catch (err) { agentActionError.value = errorSummary(err.message, 260) || '操作失败，请稍后重试。' }
      finally {
        clearInterval(agentActionElapsedTimer)
        agentActionElapsedTimer = null
        agentActionSubmitting.value = false
      }
    }
    async function saveManagerConfig() {
      try { await api('/manager/config', { method: 'PUT', body: JSON.stringify(managerForm.value) }); notice.value = 'Manager 启动参数已保存。重启后生效。'; await refresh() }
      catch (err) { error.value = err.message }
    }
    async function testManagerDatabase() {
      error.value = ''; notice.value = ''
      try { const result = await api('/manager/database/test', { method: 'POST', body: JSON.stringify(managerForm.value) }); notice.value = result.message || '数据库连接成功。' }
      catch (err) { error.value = err.message }
    }
    async function managerAction(action) {
      const labels = { start: '启动', restart: '重启', stop: '停止' }
      if (!confirm(`确认${labels[action]} Manager 服务？`)) return
      try {
        const result = await api(`/manager/${action}`, { method: 'POST', body: JSON.stringify({ config: managerForm.value }) })
        notice.value = `Manager 已${labels[action]}。`
        if (action === 'stop') { data.value.manager = result || { running: false, config: { ...managerForm.value } }; return }
        if (action === 'restart') {
          for (let attempt = 0; attempt < 20; attempt++) {
            await new Promise(resolve => setTimeout(resolve, 500))
            try { const status = await api('/manager/status'); if (status?.running && (!result?.pid || status.pid === result.pid)) { await refresh(); return } } catch (_) { /* 切换监听期间继续等待 */ }
          }
          throw new Error('新 Manager 未在 10 秒内就绪，请检查 Manager 日志。')
        }
        await refresh()
      }
      catch (err) { error.value = err.message }
    }
    async function showMachine(id) {
      try {
        const machineID = encodeURIComponent(String(id || '').trim())
        if (!machineID) throw new Error('机器 ID 为空，无法打开详情。')
        selectedMachine.value = await api(`/machines/${machineID}`)
        selectedMachineCluster.value = selectedMachine.value.Cluster || selectedMachine.value.cluster || ''
        selectedMachineErrorExpanded.value = false; machineStaticInfo.value = null; machineDynamicInfo.value = null; machineInfoError.value = ''
        showMachineDetail.value = true
        try { machineStaticInfo.value = await api(`/machines/${machineID}/static-info`) } catch (_) { /* 未采集静态信息时由用户主动采集 */ }
        try { machineDynamicInfo.value = await api(`/machines/${machineID}/dynamic-metrics`) }
        catch (err) { machineInfoError.value = `获取动态指标失败：${err.message}` }
      } catch (err) { error.value = err.message }
    }
    async function saveMachine() { try { const m = selectedMachine.value; await api(`/machines/${m.ID || m.id}`, { method: 'PUT', body: JSON.stringify({ name: m.Name || m.name, ip: m.IP || m.ip, ssh_port: m.SSHPort || m.ssh_port, ssh_user: m.SSHUser || m.ssh_user }) }); notice.value = '机器基本信息已更新。'; showMachineDetail.value = false; await refresh() } catch (err) { error.value = err.message } }
    function machineDeleteExpected() { const m = selectedMachine.value; return m ? `${machineDeleteForm.value.mode === 'detach' ? 'DETACH' : 'DELETE'} ${m.Name || m.name}` : '' }
    function machineDeleteClusterName() { return String(selectedMachine.value?.Cluster || selectedMachine.value?.cluster || '').trim() }
    async function leaveMachineDeleteForCluster() {
      const clusterName = machineDeleteClusterName()
      showMachineDelete.value = false
      showMachineDetail.value = false
      active.value = 'clusters'
      const cluster = data.value.clusters.find(item => (item.Name || item.name) === clusterName)
      if (cluster) {
        data.value.clusterSection = 'machines'
        await openClusterDetail(cluster)
      }
      error.value = `请先将机器从集群 ${clusterName} 移出，再执行删除。`
    }
    async function openMachineDelete() {
      const registered = mysqlInstancesOnMachine(selectedMachine.value).length > 0
      machineDeleteForm.value = { mode: 'cleanup', delete_mysql: registered, delete_agent: true, confirmation: '' }
      machineDeleteError.value = ''
      machineDeletePrecheck.value = { registered_mysql_ports: mysqlInstancesOnMachine(selectedMachine.value).map(item => item.Port || item.port), remote_checked: false, mysql_detected: false, mysql_residues: [], warning: '' }
      showMachineDelete.value = true
      machineDeletePrechecking.value = true
      try {
        const machineID = selectedMachine.value?.ID || selectedMachine.value?.id
        const report = await api(`/machines/${machineID}/delete-precheck`)
        machineDeletePrecheck.value = report || machineDeletePrecheck.value
        if (report?.mysql_detected || (report?.registered_mysql_ports || []).length) machineDeleteForm.value.delete_mysql = true
      } catch (err) {
        machineDeletePrecheck.value.warning = `实机探测失败：${errorSummary(err.message, 180)}。未登记不代表目标机没有 MySQL。`
      } finally { machineDeletePrechecking.value = false }
    }
    function machineDeleteRegisteredPorts() { return machineDeletePrecheck.value?.registered_mysql_ports || [] }
    function machineDeleteRemoteMySQLDetected() { return !!machineDeletePrecheck.value?.mysql_detected }
    function machineDeleteMySQLResidues() { return machineDeletePrecheck.value?.mysql_residues || [] }
	function machineDeleteSSHBlocked() { return machineDeleteForm.value.mode === 'cleanup' && machineDeleteForm.value.delete_agent && !machineDeletePrechecking.value && !machineDeletePrecheck.value?.ssh_reachable }
    function machineDeleteResidueLabel(value) {
      const text = String(value || '')
      if (text.startsWith('systemd:')) return `systemd 服务：${text.slice(8)}`
      if (text.startsWith('process:')) return `运行进程：${text.slice(8)}`
      if (text.startsWith('config-path:')) return `配置目录：${text.slice(12)}`
      if (text.startsWith('config:')) return `配置文件：${text.slice(7)}`
      if (text.startsWith('path:')) return `文件路径：${text.slice(5)}`
      if (text.startsWith('binary:')) return `程序文件：${text.slice(7)}`
      return text
    }
    function machineDeleteSteps() {
      const mysqlCount = machineDeleteRegisteredPorts().length
      const remoteDetected = machineDeleteRemoteMySQLDetected()
      return [
        { key: 'validate', enabled: machineDeleteForm.value.mode !== 'detach', title: '实机探测清理范围', detail: machineDeletePrechecking.value ? '正在通过 SSH 检查 systemd、mysqld 进程、配置和数据路径' : `平台登记 ${mysqlCount} 个实例；实机${remoteDetected ? '检测到 MySQL' : machineDeletePrecheck.value.remote_checked ? '未检测到 MySQL' : '状态未确认'}` },
        { key: 'mysql', enabled: machineDeleteForm.value.mode !== 'detach' && machineDeleteForm.value.delete_mysql, title: '卸载 MySQL、清理数据并复检', detail: mysqlCount ? `${mysqlCount} 个登记实例及实机残留：清理后复检服务、进程、配置、程序与数据路径` : remoteDetected ? '清理实机检测到的未登记 MySQL，并验证所有关联资源均已移除' : '执行远程清理，并以复检结果确认 MySQL 已彻底移除' },
		{ key: 'agent', enabled: machineDeleteForm.value.mode !== 'detach' && machineDeleteForm.value.delete_agent, title: '卸载 Agent、systemd 并复检', detail: '停止服务和残留进程，删除 systemd unit 与安装目录后执行实机验证' },
        { key: 'local', enabled: true, title: '删除 Manager 本地记录', detail: '清理心跳、任务、静态信息、实例关联及机器记录' }
      ]
    }
    async function deleteMachine() {
      const m = selectedMachine.value
      if (!showMachineDelete.value) { openMachineDelete(); return }
      if (machineDeleteClusterName()) { error.value = `机器仍属于集群 ${machineDeleteClusterName()}，请先移出集群后再删除。`; return }
      if (!m || machineDeleteForm.value.confirmation !== machineDeleteExpected()) { error.value = '二次确认内容不匹配，未执行删除。'; return }
      machineDeleteSubmitting.value = true; machineDeleteError.value = ''; error.value = ''
      try {
        const detachOnly = machineDeleteForm.value.mode === 'detach'
        const result = await api(`/machines/${m.ID || m.id}`, { method: 'DELETE', body: JSON.stringify({ detach_only: detachOnly, delete_mysql: detachOnly ? false : machineDeleteForm.value.delete_mysql, delete_agent: detachOnly ? false : machineDeleteForm.value.delete_agent }) })
        const mysqlPorts = result.mysql_ports || result.MySQLPorts || []
        const summaries = [detachOnly ? `机器 ${m.Name || m.name} 已从平台直接剔除，未连接目标机器` : `机器 ${m.Name || m.name} 已删除`]
        if (mysqlPorts.length) summaries.push(`MySQL ${mysqlPorts.join('、')} 已卸载，远端复检通过`)
        const sshPorts = result.mysql_ssh_ports || result.MySQLSSHPorts || []
        if (sshPorts.length) summaries.push(`MySQL ${sshPorts.join('、')} 通过 SSH 通道清理`)
        const residues = result.mysql_residues || result.MySQLResidues || []
        if (residues.length && !mysqlPorts.length) summaries.push(`实机检测到的未登记 MySQL 与 ${residues.length} 项关联资源已清理`)
        if (result.agent_verified ?? result.AgentVerified) summaries.push('Agent 已卸载，远端复检通过')
        else if (result.agent_uninstalled ?? result.AgentUninstalled) summaries.push('Agent 已卸载')
        notice.value = summaries.join('；') + '。'
        showMachineDelete.value = false; showMachineDetail.value = false
        if (data.value.machines.length === 1 && machinePage.value > 1) machinePage.value--
        await refresh()
      } catch (err) { machineDeleteError.value = err.message; error.value = `删除机器失败：${err.message}` }
      finally { machineDeleteSubmitting.value = false }
    }
    async function assignMachineCluster() {
      const m = selectedMachine.value; const cluster = selectedMachineCluster.value
      if (!cluster) { error.value = '请选择目标集群。'; return }
      if (!confirm(`确认将 ${m.Name || m.name} 分配至集群 ${cluster}？系统将按 CLI 流程安装 Agent 并采集静态信息。`)) return
      try { await api(`/machines/${m.ID || m.id}/assign-cluster`, { method: 'POST', body: JSON.stringify({ cluster }) }); notice.value = `机器已分配至集群 ${cluster}。`; await refresh(); selectedMachine.value = await api(`/machines/${m.ID || m.id}`) }
      catch (err) { error.value = err.message }
    }
    function openQuickClusterAssign(machine) {
      if (!data.value.clusters.length) { error.value = '请先在“集群管理”中创建集群。'; return }
      selectedMachine.value = machine
      selectedMachineCluster.value = ''
      showQuickClusterAssign.value = true
    }
    async function quickAssignMachineCluster() {
      const m = selectedMachine.value; const cluster = selectedMachineCluster.value
      if (!cluster) { error.value = '请选择目标集群。'; return }
      try {
        await api(`/machines/${m.ID || m.id}/assign-cluster`, { method: 'POST', body: JSON.stringify({ cluster }) })
        notice.value = `机器已分配至集群 ${cluster}。`
        showQuickClusterAssign.value = false
        await refresh()
      } catch (err) { error.value = err.message }
    }
    async function collectMachineStaticInfo() {
      const m = selectedMachine.value; machineInfoError.value = ''
      try { machineStaticInfo.value = await api(`/machines/${m.ID || m.id}/static-info`, { method: 'POST' }); notice.value = '机器静态信息采集完成。' }
      catch (err) { machineInfoError.value = err.message }
    }
    async function loadMachineDynamicInfo() {
      const m = selectedMachine.value; machineInfoError.value = ''
      try { machineDynamicInfo.value = await api(`/machines/${m.ID || m.id}/dynamic-metrics`) }
      catch (err) { machineInfoError.value = err.message }
    }
    function changePage(target, delta, total) { const next = target.value + delta; if (next >= 1 && (next - 1) * pageSize < total) { target.value = next; refresh() } }
    async function createCredential() {
      if (credentialSubmitting.value) return
      credentialSubmitting.value = true
      try {
        const requestID = globalThis.crypto?.randomUUID?.() || `credential-${Date.now()}-${Math.random().toString(16).slice(2)}`
        await api('/ssh-credentials', { method: 'POST', headers: { 'X-Idempotency-Key': requestID }, body: JSON.stringify(credentialForm.value) })
        notice.value = '凭证已保存，可立即分配给已纳管机器。'; showCredential.value = false
        credentialForm.value = { name: '', ssh_user: 'root', type: 'password', ssh_password: '', private_key: '', passphrase: '' }; await refresh()
      } catch (err) { error.value = err.message }
      finally { credentialSubmitting.value = false }
    }
    async function deleteCredential(item) {
      if (!confirm(`确认删除 SSH 凭证 ${item.name}？已分配机器将不再关联该凭证。`)) return
      try { await api(`/ssh-credentials/${item.id}`, { method: 'DELETE' }); notice.value = 'SSH 凭证已删除。'; await refresh() }
      catch (err) { error.value = err.message }
    }
    async function createMySQLInstall() { try { if (mysqlInstallCompatibility.value.status === 'incompatible') throw new Error(mysqlInstallCompatibility.value.message); const allowed = new Set(mysqlInstallParameterGroups.value.flatMap(group => group.fields || []).map(field => field.key)); const payload = { ...mysqlInstallForm.value, package_name: mysqlInstallCompatibility.value.package?.file_name || mysqlInstallForm.value.package_name || '', install_pt_tools: (!selectedMySQLInstallPackage.value || selectedMySQLInstallPackage.value.pt_tools_supported) && mysqlInstallForm.value.install_pt_tools, runtime_parameters: Object.fromEntries(Object.entries(mysqlInstallForm.value.runtime_parameters || {}).filter(([key,value]) => allowed.has(key) && String(value || '').trim() !== '')) }; delete payload._runtime_parameter_groups; const result = await api('/tasks/mysql-install', { method:'POST', body:JSON.stringify(payload) }); mysqlTaskDetail.value=result; showMySQLInstall.value=false; showMySQLTask.value=false; await openTaskDetail(result); notice.value = `MySQL 安装任务已创建：${result.Task?.ID || result.task?.ID || result.task?.id || '已提交'}`; await refresh() } catch(err) { error.value=err.message } }
    async function openMySQLInstall() {
      if (data.value.accountPresets.length) mysqlInstallForm.value.accounts = JSON.parse(JSON.stringify(data.value.accountPresets))
      showMySQLInstall.value = false
      mysqlView.value = 'install'
      try { await loadMySQLPackages() } catch (err) { error.value = `刷新 MySQL 安装版本失败：${err.message}` }
    }
    function isCustomMySQLAccount(account) { return String(account?.role || '').startsWith('custom_') }
    function addCustomMySQLAccount() {
      mysqlInstallForm.value.accounts.push({ role: `custom_${Date.now()}_${mysqlInstallForm.value.accounts.length}`, username: '', password: '', host: '%', enabled: true, privileges: ['SELECT'] })
    }
    function removeCustomMySQLAccount(index) {
      if (isCustomMySQLAccount(mysqlInstallForm.value.accounts[index])) mysqlInstallForm.value.accounts.splice(index, 1)
    }
    async function saveMySQLAccountPresets() { try { const items = await api('/mysql/account-presets', { method:'PUT', body:JSON.stringify(data.value.accountPresets) }); data.value.accountPresets = items; notice.value = 'MySQL 预设账号已保存，后续安装任务将自动带入。' } catch (err) { error.value = err.message } }
    async function refreshMySQLTask() { const id=mysqlTaskDetail.value?.Task?.ID || mysqlTaskDetail.value?.task?.id; if(!id)return; try { mysqlTaskDetail.value=await api(`/tasks?id=${encodeURIComponent(id)}`) } catch(err) { error.value=err.message } }
    async function uninstallMySQL(item) { const machine=item.MachineIP||item.machine_ip; const port=item.Port||item.port; if (!confirm(`确认卸载 ${machine}:${port} 的 MySQL？该操作会删除数据文件。`)) return; if (prompt(`请输入 UNINSTALL ${machine}:${port}`)!==`UNINSTALL ${machine}:${port}`) return; try { await api('/tasks/mysql-uninstall',{method:'POST',body:JSON.stringify({machine,port})}); notice.value='MySQL 卸载任务已创建。'; await refresh() } catch(err){error.value=err.message} }
    async function forgetMySQL(item) { const machine=item.MachineIP||item.machine_ip; const port=item.Port||item.port; if(!confirm(`仅删除 Manager 中 ${machine}:${port} 的实例记录，不会连接目标机器。是否继续？`))return; try{await api('/mysql/instances',{method:'DELETE',body:JSON.stringify({machine,port})});notice.value='MySQL 实例记录已遗忘。';await refresh()}catch(err){error.value=err.message} }
    function openCreateCluster() { clusterForm.value = { old_name: '', name: '', description: '' }; showClusterEditor.value = true }
    function openEditCluster(item) { clusterForm.value = { old_name: item.Name || item.name, name: item.Name || item.name, description: item.Description || item.description || '' }; showClusterEditor.value = true }
    async function openClusterDetail(item) {
      const name = item.Name || item.name
      data.value.clusterSection = 'overview'
      data.value.instanceView = 'instances'
      selectedClusterDetail.value = item
      clusterTopology.value = { cluster: name, nodes: [], edges: [], overview: { summary: {}, series: [], machines: [] } }
      clusterTopologyError.value = ''
      selectedClusterOperationMachineIDs.value = []
      clusterMachinePage.value = 1
      try {
        const [detail] = await Promise.all([api(`/clusters/${encodeURIComponent(name)}`), loadClusterMachines(1, name)])
        selectedClusterDetail.value = detail
        await refreshClusterTopology({ silent: true })
      } catch (err) { clusterTopologyError.value = `获取集群详情失败：${err.message}` }
    }
    function closeClusterDetail() {
      stopClusterTopologyAutoRefresh()
      if (architecturePollTimer) { clearTimeout(architecturePollTimer); architecturePollTimer = null }
      selectedClusterDetail.value = null
      data.value.clusterSection = 'overview'
      clusterTopologyError.value = ''
      clusterTopologyLastUpdated.value = ''
      clusterMachineItems.value = []
      selectedClusterOperationMachineIDs.value = []
      architecturePlan.value = null
      architectureRun.value = null
    }
    async function loadClusterMachines(page = clusterMachinePage.value, clusterName = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name) {
      if (!clusterName) return
      const encodedCluster = encodeURIComponent(clusterName)
      const [result, configs, states] = await Promise.all([
        api(`/clusters/${encodedCluster}/machines?page=${page}&page_size=50`),
        api(`/clusters/${encodedCluster}/vip/config`).catch(() => []),
        api(`/clusters/${encodedCluster}/vip/status`).catch(() => [])
      ])
      clusterMachineItems.value = result.items || []
      clusterMachinePage.value = result.page || page
      clusterMachineTotal.value = result.total || 0
      vipConfigs.value = Array.isArray(configs) ? configs : []
      vipStates.value = Array.isArray(states) ? states : []
      const staticInfoEntries = await Promise.all(clusterMachineItems.value.map(async machine => {
        const id = machine.ID || machine.id
        try { return [id, await api(`/machines/${encodeURIComponent(id)}/static-info`)] }
        catch (_) { return [id, null] }
      }))
      clusterMachineStaticInfo.value = Object.fromEntries(staticInfoEntries)
    }
    async function changeClusterMachinePage(delta) {
      const next = clusterMachinePage.value + delta
      if (next < 1 || (next - 1) * 50 >= clusterMachineTotal.value) return
      selectedClusterOperationMachineIDs.value = []
      try { await loadClusterMachines(next) } catch (err) { error.value = err.message }
    }
    async function refreshClusterTopology(options = {}) {
      const name = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      if (!name || clusterTopologyRefreshing.value) return
      clusterTopologyRefreshing.value = true
      try {
        const requests = [api(`/clusters/${encodeURIComponent(name)}/topology?range_minutes=${clusterOverviewRange.value}`)]
        if (options.includeMachines) requests.push(loadClusterMachines())
        const [topology] = await Promise.all(requests)
        clusterTopology.value = topology
        clusterTopologyLastUpdated.value = new Date().toISOString()
        clusterTopologyError.value = ''
      } catch (err) {
        if (!options.silent) clusterTopologyError.value = `获取集群拓扑失败：${err.message}`
      } finally { clusterTopologyRefreshing.value = false }
    }
    function startClusterTopologyAutoRefresh() {
      stopClusterTopologyAutoRefresh()
      if (!clusterTopologyAutoRefresh.value) return
      clusterTopologyRefreshTimer = setInterval(() => {
        if (active.value === 'clusters' && selectedClusterDetail.value && (data.value.clusterSection || 'overview') === 'overview') refreshClusterTopology({ silent: true })
      }, 5000)
    }
    function stopClusterTopologyAutoRefresh() {
      if (clusterTopologyRefreshTimer) clearInterval(clusterTopologyRefreshTimer)
      clusterTopologyRefreshTimer = null
    }
    function toggleClusterTopologyAutoRefresh() {
      clusterTopologyAutoRefresh.value = !clusterTopologyAutoRefresh.value
      if (clusterTopologyAutoRefresh.value) startClusterTopologyAutoRefresh()
      else stopClusterTopologyAutoRefresh()
    }
    function overviewTopologyEndpoint(node) { return topologyEndpoint(node?.ip, node?.port) }
    function overviewTopologyEdgeForReplica(node) {
      const endpoint = overviewTopologyEndpoint(node)
      return (clusterTopology.value.edges || []).find(edge => topologyEndpoint(edge.target_ip, edge.target_port) === endpoint)
    }
    function overviewTopologyRoots() {
      const nodes = clusterTopology.value.nodes || []
      const outgoing = new Set((clusterTopology.value.edges || []).map(edge => topologyEndpoint(edge.source_ip, edge.source_port)))
      const roots = nodes.filter(node => outgoing.has(overviewTopologyEndpoint(node)) || ['m','m/s','master','primary'].includes(String(node.role || '').toLowerCase()))
      return roots
    }
    function overviewTopologyReplicas() {
      const rootEndpoints = new Set(overviewTopologyRoots().map(overviewTopologyEndpoint))
      return (clusterTopology.value.nodes || []).filter(node => !rootEndpoints.has(overviewTopologyEndpoint(node)) && overviewTopologyEdgeForReplica(node))
    }
    function overviewTopologyStandalone() {
      const rootEndpoints = new Set(overviewTopologyRoots().map(overviewTopologyEndpoint))
      const replicaEndpoints = new Set(overviewTopologyReplicas().map(overviewTopologyEndpoint))
      return (clusterTopology.value.nodes || []).filter(node => !rootEndpoints.has(overviewTopologyEndpoint(node)) && !replicaEndpoints.has(overviewTopologyEndpoint(node)))
    }
    function overviewTopologySourceName(node) {
      const edge = overviewTopologyEdgeForReplica(node)
      return edge?.source_name || edge?.source_ip || '未识别'
    }
    function topologyMetric(value, suffix = '') {
      if (value === undefined || value === null || value === '') return '—'
      const number = Number(value)
      return Number.isFinite(number) ? `${number >= 100 ? Math.round(number) : Math.round(number * 10) / 10}${suffix}` : `${value}${suffix}`
    }
    function clusterOverview() { return clusterTopology.value.overview || { summary: {}, series: [], machines: [] } }
    function overviewNumber(value, digits = 1) {
      const number = Number(value || 0)
      if (!Number.isFinite(number)) return '—'
      return number.toLocaleString('zh-CN', { maximumFractionDigits: digits, minimumFractionDigits: number > 0 && number < 10 ? 1 : 0 })
    }
    function overviewBytes(value, rate = false) {
      let number = Number(value || 0)
      if (!Number.isFinite(number) || number <= 0) return rate ? '0 B/s' : '0 B'
      const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']; let index = 0
      while (number >= 1024 && index < units.length - 1) { number /= 1024; index++ }
      return `${number >= 100 ? number.toFixed(0) : number >= 10 ? number.toFixed(1) : number.toFixed(2)} ${units[index]}${rate ? '/s' : ''}`
    }
    function overviewChartPoints(field, width = 520, height = 130) {
      const values = (clusterOverview().series || []).map(item => Number(item[field] || 0))
      if (!values.length) return ''
      const max = Math.max(...values, 1); const min = Math.min(...values, 0); const span = Math.max(max - min, 1)
      return values.map((value, index) => `${values.length === 1 ? width / 2 : index * width / (values.length - 1)},${height - 8 - ((value - min) / span) * (height - 20)}`).join(' ')
    }
    function overviewChartArea(field, width = 520, height = 130) {
      const points = overviewChartPoints(field, width, height)
      return points ? `0,${height} ${points} ${width},${height}` : ''
    }
    function overviewRangeLabel() { return ({15:'近 15 分钟',60:'近 1 小时',360:'近 6 小时',1440:'近 24 小时'})[clusterOverviewRange.value] || '近期' }
    function changeClusterOverviewRange() { refreshClusterTopology() }
    async function openClusterBackup() {
      stopClusterTopologyAutoRefresh()
      data.value.clusterSection = 'backup'
      showBackupPolicyEditor.value = false
      await loadClusterBackups()
    }
    async function loadClusterBackups() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      if (!cluster) return
      try {
        const [policies, runs] = await Promise.all([api(`/backup/policies?cluster=${encodeURIComponent(cluster)}`), api(`/backup/runs?cluster=${encodeURIComponent(cluster)}&limit=100`)])
        backupPolicies.value = policies || []; backupRuns.value = runs || []
      } catch (err) { error.value = `读取备份配置失败：${err.message}` }
    }
    function openBackupPolicyEditor(policy = null) {
      if (!policy) {
        const form = newBackupPolicyForm()
        const replica = clusterTopology.value.nodes.find(node => ['s','slave','replica','readonly'].includes(String(node.role||'').toLowerCase()))
        const preferred = replica ? clusterMachineItems.value.find(machine => (machine.IP||machine.ip)===replica.ip) : clusterMachineItems.value.find(machine => mysqlInstancesOnMachine(machine).length)
        if (preferred) { form.machine_id=preferred.ID||preferred.id; const instances=backupInstancesForMachine(form.machine_id); form.port=Number(replica?.port || instances[0]?.Port || instances[0]?.port || 3306) }
        backupPolicyForm.value = form
      } else backupPolicyForm.value = { ...policy, start_at: new Date(policy.start_at).toISOString().slice(0,16), mysql_password: '', weekdays: [...(policy.weekdays || [])], weekday_backup_types:{...(policy.weekday_backup_types||{})} }
      showBackupPolicyEditor.value = true
    }
    function backupMachines() { return clusterMachineItems.value.filter(machine => mysqlInstancesOnMachine(machine).length) }
    function backupInstancesForMachine(machineID) { const machine=clusterMachineItems.value.find(item=>(item.ID||item.id)===machineID); return machine ? mysqlInstancesOnMachine(machine) : [] }
    function backupMachineChanged() { const instances=backupInstancesForMachine(backupPolicyForm.value.machine_id); if (instances.length) backupPolicyForm.value.port=Number(instances[0].Port||instances[0].port) }
    function backupMachineRole(machine) { const instance=mysqlInstancesOnMachine(machine)[0]; return mysqlRoleLabel(mysqlTopologyNode(machine,instance)?.role) }
    function weekdayName(day) { return ['周日','周一','周二','周三','周四','周五','周六'][Number(day)] }
    function toggleAllBackupWeekdays(event) { backupPolicyForm.value.weekdays = event.target.checked ? [1,2,3,4,5,6,0] : [] }
    async function saveBackupPolicy() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const payload = { ...backupPolicyForm.value, cluster, start_at: new Date(backupPolicyForm.value.start_at).toISOString() }
      try { await api('/backup/policies', { method:'POST', body:JSON.stringify(payload) }); notice.value = 'XtraBackup 备份策略已保存，Manager 调度器会按下一次执行时间下发给 Agent。'; showBackupPolicyEditor.value=false; await loadClusterBackups() }
      catch (err) { error.value = err.message }
    }
    async function deleteBackupPolicy(item) {
      if (!confirm(`确认删除备份策略“${item.name}”？已生成的备份记录不会删除。`)) return
      try { await api(`/backup/policies/${item.id}`, {method:'DELETE'}); notice.value='备份策略已删除。'; await loadClusterBackups() } catch(err){error.value=err.message}
    }
    async function runBackupPolicy(item) {
      if (!confirm(`立即在目标 Agent 上执行策略“${item.name}”？`)) return
      try { const run=await api(`/backup/policies/${item.id}/run`,{method:'POST'}); notice.value=`备份任务已创建：${run.task_id}`; await loadClusterBackups(); await refresh() } catch(err){error.value=err.message}
    }
    function restoreBackup(run) {
      const restoreAt = new Date(Date.now() - 5 * 60000); restoreAt.setSeconds(0, 0)
      data.value.restoreDialog = run
      data.value.restoreForm = { mode:'flashback', backup_path:run.backup_path, restore_time:restoreAt.toISOString().slice(0,16), mysql_user:'root', mysql_password:'', repair_replication:true, apply_flashback:false, database:'', tables:'', output_dir:'/data/gmha/recovery', confirmation:'' }
    }
    function restoreExpected() { return data.value.restoreDialog ? `${data.value.restoreForm.mode==='flashback'?'FLASHBACK':'RESTORE'} ${data.value.restoreDialog.id}` : '' }
    async function submitRestore() {
      if (!data.value.restoreDialog || data.value.restoreForm.confirmation !== restoreExpected()) { error.value='二次确认内容不匹配，未创建恢复任务。'; return }
      const payload = { ...data.value.restoreForm, restore_time:new Date(data.value.restoreForm.restore_time).toISOString(), tables:String(data.value.restoreForm.tables||'').split(',').map(value=>value.trim()).filter(Boolean) }
      try { const result=await api(`/backup/runs/${data.value.restoreDialog.id}/restore`,{method:'POST',body:JSON.stringify(payload)}); notice.value=`恢复任务已创建：${result.task?.ID || result.Task?.ID}`; data.value.restoreDialog=null; await loadClusterBackups(); await refresh() } catch(err){error.value=err.message}
    }
    data.value.submitRestore = submitRestore
    data.value.restoreExpected = restoreExpected
    function backupScheduleLabel(item) {
      if (item.schedule_type==='once') return `仅一次 · ${date(item.start_at)}`
      if (item.schedule_type==='custom') return `每 ${item.interval_minutes} 分钟 · ${date(item.start_at)} 起`
      const names=['周日','周一','周二','周三','周四','周五','周六']; return `每周 ${item.weekdays.map(i=>names[i]).join('、')} · ${new Date(item.start_at).toLocaleTimeString('zh-CN',{hour:'2-digit',minute:'2-digit'})}`
    }
    function backupTypeLabel(value){ return value==='incremental'?'增量备份':'全量备份' }
    async function installClusterMySQL() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const port = Number(prompt('MySQL 端口：', '3306')); if (!port) return
      const rootPassword = prompt(`为集群 ${cluster} 创建 MySQL 安装任务。请输入 root 密码：`); if (!rootPassword) return
if (!confirm(`确认向集群 ${cluster} 的集群内所有机器创建 MySQL 安装任务？`)) return
      try { const result = await api('/tasks/cluster-mysql-install', { method:'POST', body:JSON.stringify({ cluster, port, server_id_start:1, mysql_user:'mysql', root_password:rootPassword, profile:'default', accounts:data.value.accountPresets }) }); notice.value=`已创建 ${result.created || 0} 个安装任务${result.failed ? `，${result.failed} 个失败` : ''}。`; await refresh(); await refreshClusterTopology() } catch (err) { error.value=err.message }
    }
    async function uninstallClusterMySQL() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const port = Number(prompt(`高风险操作：将卸载集群 ${cluster} 中端口为多少的 MySQL（数据会被删除）？`, '3306')); if (!port) return
      if (prompt(`二次确认：请输入 UNINSTALL ${cluster}:${port}`) !== `UNINSTALL ${cluster}:${port}`) return
      try { const result = await api('/tasks/cluster-mysql-uninstall', { method:'POST', body:JSON.stringify({cluster,port}) }); notice.value=`已创建 ${result.created || 0} 个 MySQL 卸载任务${result.failed ? `，${result.failed} 个失败` : ''}。`; await refresh(); await refreshClusterTopology() } catch (err) { error.value=err.message }
    }
    function mysqlInstancesOnMachine(machine) {
      const id = machine?.ID || machine?.id
      const ip = machine?.IP || machine?.ip
      return data.value.mysqlInstances.filter(item => (item.MachineID || item.machine_id) === id || (item.MachineIP || item.machine_ip) === ip)
    }
    function clusterMachineInterfaces(machine) {
      const id = machine?.ID || machine?.id
      const staticInfo = clusterMachineStaticInfo.value[id]
      const host = staticInfo?.Host || staticInfo?.host || staticInfo
      const interfaces = host?.Interfaces || host?.interfaces || []
      // 静态采集会保留漂移前的 VIP。配置内 VIP 只以 Manager 当前绑定状态为准，
      // 避免旧持有者和新持有者的快照同时显示同一地址。
      const managedVIPs = new Set([
        ...vipConfigs.value.map(item => item.vip_address),
        ...vipStates.value.map(item => item.vip_address)
      ].filter(Boolean))
      const rows = interfaces.flatMap(item => {
        const name = item.Name || item.name || '未知网卡'
        const ips = item.IPs || item.ips || []
        return ips.filter(ip => !managedVIPs.has(String(ip).split('/')[0])).map(ip => ({ name, ip }))
      })
      const currentVIPRows = vipStates.value
        .filter(item => item.current_holder_machine_id === id && item.vip_address)
        .map(item => {
          const config = vipConfigs.value.find(configItem => configItem.vip_address === item.vip_address)
          return { name: item.current_interface || config?.default_interface || 'VIP', ip: item.vip_address }
        })
      rows.push(...currentVIPRows)
      if (rows.length) return rows
      return [{ name: '网卡待采集', ip: machine?.IP || machine?.ip || '—' }]
    }
    function machineArchitecture(machine) {
      return String(machine?.Architecture || machine?.architecture || machine?.Arch || machine?.arch || '').toLowerCase()
    }
    function compatibleMySQLPackage(machine, requestedName = '') {
      if (requestedName) return requestedName
      const arch = machineArchitecture(machine)
      const packages = data.value.mysqlPackages || []
      const match = packages.find(item => {
        const packageArch = String(item.arch || item.Arch || '').toLowerCase()
        return !arch || !packageArch || packageArch === arch || (arch === 'x86_64' && ['amd64', 'x86_64'].includes(packageArch)) || (arch === 'aarch64' && ['arm64', 'aarch64'].includes(packageArch))
      })
      return match?.file_name || match?.FileName || ''
    }
    function mysqlTopologyNode(machine, instance) {
      const ip = machine?.IP || machine?.ip
      const port = instance?.Port || instance?.port
      return clusterTopology.value.nodes.find(node => node.ip === ip && (!port || Number(node.port) === Number(port))) || null
    }
    function mysqlRoleLabel(role) {
      return ({ M: '主库', S: '从库', 'M/S': '主从节点', readonly: '只读实例', standalone: '独立实例' })[role] || '独立实例'
    }
    function openClusterMySQLWizard(action, singleMachine = null) {
      const selectedIDs = singleMachine ? [singleMachine.ID || singleMachine.id] : selectedClusterOperationMachineIDs.value
      const candidates = clusterMachineItems.value.filter(machine => !selectedIDs.length || selectedIDs.includes(machine.ID || machine.id)).filter(machine => action === 'install' ? !mysqlInstancesOnMachine(machine).length : mysqlInstancesOnMachine(machine).length)
      if (!candidates.length) { error.value = action === 'install' ? '没有可安装 MySQL 的目标机器。' : '没有可卸载 MySQL 的目标实例。'; return }
      const packageOptions = (data.value.mysqlPackages || []).map(pkg => `<option value="${pkg.file_name}">${pkg.version} · ${pkg.arch} · glibc ${pkg.glibc_version}</option>`).join('')
      const rows = candidates.map((machine, index) => {
        const ip = machine.IP || machine.ip; const name = machine.Name || machine.name; const arch = machineArchitecture(machine) || '自动识别'; const selected = compatibleMySQLPackage(machine)
        const options = packageOptions.replace(`value="${selected}"`, `value="${selected}" selected`)
        return `<tr data-ip="${ip}"><td><input class="batch-target" type="checkbox" checked></td><td><b>${name}</b><small>${ip}</small></td><td>${arch}</td>${action === 'install' ? `<td><select class="batch-package"><option value="">自动匹配兼容包</option>${options}</select></td><td><input class="batch-port" type="number" value="3306" min="1"></td><td><input class="batch-server-id" type="number" value="${index + 1}" min="1"></td>` : `<td colspan="3">将卸载：${mysqlInstancesOnMachine(machine).map(item => `${item.Port || item.port}`).join('、')} 端口实例</td>`}</tr>`
      }).join('')
      const host = document.createElement('div')
      host.className = 'modal-mask cluster-batch-wizard-mask'
      host.innerHTML = `<section class="modal cluster-mysql-batch-modal cluster-batch-wizard"><div class="modal-head"><div><p>集群 MySQL 运维</p><h2>${action === 'install' ? '批量安装 MySQL' : '批量卸载 MySQL'}</h2></div><button type="button" class="batch-close">×</button></div><p class="form-note">为每台机器单独选择安装包、端口和 server_id；不同架构与 MySQL 版本可同时提交。</p><div class="batch-global">${action === 'install' ? '<label>root 密码<input class="batch-root-password" type="password" required></label><label>MySQL 用户<input class="batch-mysql-user" value="mysql"></label><label>参数 Profile<input class="batch-profile" value="default"></label>' : '<label>二次确认<input class="batch-confirm" placeholder="UNINSTALL '+(selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name)+'"></label>'}</div><div class="cluster-machine-table-wrap"><table class="cluster-machine-table"><thead><tr><th>选择</th><th>机器</th><th>架构</th>${action === 'install' ? '<th>MySQL 安装包</th><th>端口</th><th>server_id</th>' : '<th>卸载实例</th>'}</tr></thead><tbody>${rows}</tbody></table></div><div class="modal-actions"><button type="button" class="secondary batch-close">取消</button><button class="primary batch-submit">创建批量${action === 'install' ? '安装' : '卸载'}任务</button></div></section>`
      const close = () => host.remove()
      host.querySelectorAll('.batch-close').forEach(button => button.addEventListener('click', close))
      host.addEventListener('click', event => { if (event.target === host) close() })
      host.querySelector('.batch-submit').addEventListener('click', async () => {
        const rows = [...host.querySelectorAll('tbody tr')].filter(row => row.querySelector('.batch-target').checked)
        if (!rows.length) { alert('请至少选择一台机器。'); return }
        const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
        if (action === 'uninstall' && host.querySelector('.batch-confirm').value !== `UNINSTALL ${cluster}`) { alert(`请输入 UNINSTALL ${cluster} 以确认卸载。`); return }
        const password = host.querySelector('.batch-root-password')?.value
        if (action === 'install' && !password) { alert('请填写 root 密码。'); return }
        const user = host.querySelector('.batch-mysql-user')?.value || 'mysql'; const profile = host.querySelector('.batch-profile')?.value || 'default'
        const requests = rows.flatMap(row => {
          const ip = row.dataset.ip
          if (action === 'uninstall') return mysqlInstancesOnMachine(candidates.find(item => (item.IP || item.ip) === ip)).map(instance => api('/tasks/mysql-uninstall', { method: 'POST', body: JSON.stringify({ machine: ip, port: instance.Port || instance.port }) }))
          return [api('/tasks/mysql-install', { method: 'POST', body: JSON.stringify({ machine: ip, package_name: row.querySelector('.batch-package').value, port: Number(row.querySelector('.batch-port').value), server_id: Number(row.querySelector('.batch-server-id').value), mysql_user: user, root_password: password, profile, accounts: data.value.accountPresets }) })]
        })
        const result = await Promise.allSettled(requests); const succeeded = result.filter(item => item.status === 'fulfilled').length
        close(); notice.value = `已创建 ${succeeded}/${result.length} 个 MySQL ${action === 'install' ? '安装' : '卸载'}任务。`; if (succeeded !== result.length) error.value = '部分任务创建失败，请在任务中心查看详情。'
        await refresh(); await refreshClusterTopology(); await loadClusterMachines(1)
      })
      document.body.appendChild(host)
    }
    function openClusterMySQLBatch(action, machine = null) {
      // MySQL 生命周期已迁入“实例管理”，机器管理不再创建 MySQL 任务。
      data.value.clusterSection = 'instances'
      notice.value = '请在“实例管理”中创建 MySQL 安装或卸载任务。'
      return
      const ids = machine ? [machine.ID || machine.id] : selectedClusterOperationMachineIDs.value.slice()
      if (!ids.length) { error.value = '请先选择需要操作的机器。'; return }
      const selected = clusterMachineItems.value.filter(item => ids.includes(item.ID || item.id))
      const eligible = selected.filter(item => action === 'install' ? !mysqlInstancesOnMachine(item).length : mysqlInstancesOnMachine(item).length)
      if (!eligible.length) {
        error.value = action === 'install' ? '所选机器均已登记 MySQL 实例。' : '所选机器均未登记 MySQL 实例。'
        return
      }
      clusterMySQLDialog.value = { action, machines: eligible, cluster: selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name, targets: eligible.map((item, index) => ({ machine_id: item.ID || item.id, machine_ip: item.IP || item.ip, architecture: machineArchitecture(item) || '自动识别', package_name: compatibleMySQLPackage(item), port: 3306, server_id: index + 1 })) }
      clusterMySQLConfirm.value = ''
      clusterMySQLForm.value = { package_name: '', port: 3306, server_id_start: 1, mysql_user: 'mysql', root_password: '', profile: 'default', install_pt_tools: false, install_xtrabackup: false, memory_allocator: 'system' }
    }
    async function submitClusterMySQLBatch() {
      const dialog = clusterMySQLDialog.value
      if (!dialog) return
      if (dialog.action === 'uninstall' && clusterMySQLConfirm.value !== `UNINSTALL ${dialog.cluster}`) {
        error.value = '二次确认内容不匹配，未创建卸载任务。'
        return
      }
      const requests = []
      if (dialog.action === 'install') {
        const targets = dialog.targets || dialog.machines.map((machine, index) => ({ machine_ip: machine.IP || machine.ip, package_name: compatibleMySQLPackage(machine, clusterMySQLForm.value.package_name), port: clusterMySQLForm.value.port, server_id: Number(clusterMySQLForm.value.server_id_start) + index }))
        targets.forEach((target, index) => requests.push(api('/tasks/mysql-install', { method: 'POST', body: JSON.stringify({ ...clusterMySQLForm.value, machine: target.machine_ip, package_name: target.package_name || clusterMySQLForm.value.package_name, port: Number(target.port || clusterMySQLForm.value.port), server_id: Number(target.server_id || (Number(clusterMySQLForm.value.server_id_start) + index)), accounts: data.value.accountPresets }) })))
      } else {
        dialog.machines.forEach(machine => mysqlInstancesOnMachine(machine).forEach(instance => requests.push(api('/tasks/mysql-uninstall', { method: 'POST', body: JSON.stringify({ machine: machine.IP || machine.ip, port: instance.Port || instance.port }) }))))
      }
      const results = await Promise.allSettled(requests)
      const succeeded = results.filter(item => item.status === 'fulfilled').length
      const failed = results.length - succeeded
      clusterMySQLDialog.value = null
      selectedClusterOperationMachineIDs.value = []
      notice.value = `已创建 ${succeeded} 个 MySQL ${dialog.action === 'install' ? '安装' : '卸载'}任务${failed ? `，${failed} 个创建失败` : ''}。`
      if (failed) error.value = results.find(item => item.status === 'rejected')?.reason?.message || '部分任务创建失败。'
      await refresh()
      await refreshClusterTopology()
      await loadClusterMachines(1)
    }
    function toggleAllAutomationClusters() {
      const names = data.value.clusters.map(item => item.Name || item.name).filter(Boolean)
      automationSelectedClusters.value = automationSelectedClusters.value.length === names.length ? [] : names
    }
    async function submitAutomationTask() {
      const clusters = automationSelectedClusters.value.slice()
      if (!clusters.length) { error.value = '请至少选择一个目标集群。'; return }
      const form = automationForm.value
      if (form.action === 'shell' && !String(form.script || '').trim()) { error.value = '请输入需要下发的 Shell 脚本。'; return }
      if (['collect_mysql','mysql_user','mysql_parameter'].includes(form.action) && (!String(form.mysql_user || '').trim() || !String(form.mysql_password || '').trim())) { error.value = '数据库操作需要填写管理员用户名和密码。'; return }
      if (form.action === 'mysql_user' && form.user_action !== 'list' && !String(form.target_username || '').trim()) { error.value = '请输入目标数据库用户名。'; return }
      if (form.action === 'mysql_parameter' && (!String(form.parameter_name || '').trim() || !String(form.parameter_value || '').trim())) { error.value = '请输入参数名称和参数值。'; return }
      if (form.action === 'install' && !String(form.root_password || '').trim()) { error.value = '批量安装需要填写 MySQL root 密码。'; return }
      const confirmation = `UNINSTALL ${clusters.length} CLUSTERS`
      if (form.action === 'uninstall' && automationConfirm.value !== confirmation) { error.value = `请输入 ${confirmation} 完成二次确认。`; return }
      automationRunning.value = true
      automationResults.value = []
      error.value = ''
      try {
        if (form.action === 'backup') {
          const result = await api('/backup/cluster-runs', { method: 'POST', body: JSON.stringify({ clusters }) })
          automationResults.value = (result.items || []).map(item => ({ cluster: item.cluster, machine: item.policy, task_id: item.task_id, status: item.error ? 'failed' : 'success', created: item.task_id ? 1 : 0, failed: item.error ? 1 : 0, message: item.error || `已创建备份任务 ${item.run_id || ''}` }))
          const succeeded = automationResults.value.filter(item => item.status === 'success').length
          notice.value = `批量备份已提交：${succeeded}/${automationResults.value.length} 条策略成功。`
          if (succeeded !== automationResults.value.length) error.value = '部分备份任务提交失败，请查看下方执行结果。'
          await refresh()
          return
        }
        if (['collect_machine','shell','collect_mysql','mysql_user','mysql_parameter'].includes(form.action)) {
          const result = await api('/tasks/cluster-automation', { method: 'POST', body: JSON.stringify({ ...form, clusters, operation: form.action }) })
          const messages = { collect_machine:'已创建机器信息采集任务', collect_mysql:'已创建 MySQL 数据采集任务', shell:'已创建 Shell 执行任务', mysql_user:'已创建数据库用户任务', mysql_parameter:'已创建数据库参数任务' }
          automationResults.value = (result.items || []).map(item => ({ cluster: item.cluster, machine: item.machine, task_id: item.task_id, status: item.error ? 'failed' : 'success', created: item.task_id ? 1 : 0, failed: item.error ? 1 : 0, message: item.error || messages[form.action] }))
          const succeeded = automationResults.value.filter(item => item.status === 'success').length
          notice.value = `自动化任务已提交：${succeeded}/${automationResults.value.length} 台机器成功。`
          if (succeeded !== automationResults.value.length) error.value = '部分机器提交失败，请查看下方执行结果。'
          await refresh()
          return
        }
        automationResults.value = await Promise.all(clusters.map(async cluster => {
          try {
            const endpoint = form.action === 'install' ? '/tasks/cluster-mysql-install' : '/tasks/cluster-mysql-uninstall'
            const body = form.action === 'install'
              ? { cluster, port: Number(form.port), server_id_start: Number(form.server_id_start), mysql_user: form.mysql_user, root_password: form.root_password, profile: form.profile, install_pt_tools: form.install_pt_tools, install_xtrabackup: form.install_xtrabackup, memory_allocator: form.memory_allocator || 'system', accounts: data.value.accountPresets }
              : { cluster, port: Number(form.port) }
            const result = await api(endpoint, { method: 'POST', body: JSON.stringify(body) })
            return { cluster, status: 'success', created: result.created || 0, failed: result.failed || 0, message: `已创建 ${result.created || 0} 个任务${result.failed ? `，${result.failed} 个失败` : ''}` }
          } catch (err) {
            return { cluster, status: 'failed', created: 0, failed: 1, message: err.message }
          }
        }))
        const succeeded = automationResults.value.filter(item => item.status === 'success').length
        notice.value = `自动化任务已提交：${succeeded}/${clusters.length} 个集群成功。`
        if (succeeded !== clusters.length) error.value = '部分集群提交失败，请查看下方执行结果。'
        await refresh()
      } finally {
        automationRunning.value = false
      }
    }
    async function removeMachineFromCluster(machine) {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const name = machine.Name || machine.name || machine.IP || machine.ip
      if (!confirm(`确认将机器 ${name} 移出集群 ${cluster}？机器、Agent 和 MySQL 数据不会被删除。`)) return
      try {
        await api(`/machines/${machine.ID || machine.id}/assign-cluster`, { method: 'DELETE' })
        notice.value = `机器 ${name} 已移出集群，现为未分配集群。`
        selectedClusterOperationMachineIDs.value = selectedClusterOperationMachineIDs.value.filter(id => id !== (machine.ID || machine.id))
        await refreshClusterMachinesAfterRemoval()
      } catch (err) { error.value = err.message }
    }
    function clusterMachinePageSelected() {
      const selected = new Set(selectedClusterOperationMachineIDs.value)
      return clusterMachineItems.value.length > 0 && clusterMachineItems.value.every(machine => selected.has(machine.ID || machine.id))
    }
    function toggleClusterMachinePageSelection() {
      const pageIDs = clusterMachineItems.value.map(machine => machine.ID || machine.id)
      const selected = new Set(selectedClusterOperationMachineIDs.value)
      if (clusterMachinePageSelected()) pageIDs.forEach(id => selected.delete(id))
      else pageIDs.forEach(id => selected.add(id))
      selectedClusterOperationMachineIDs.value = [...selected]
    }
    async function refreshClusterMachinesAfterRemoval(removedCount = 1) {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const remaining = Math.max(0, clusterMachineTotal.value - removedCount)
      const targetPage = Math.min(clusterMachinePage.value, Math.max(1, Math.ceil(remaining / 50)))
      await refresh()
      selectedClusterDetail.value = data.value.clusters.find(item => (item.Name || item.name) === cluster) || selectedClusterDetail.value
      data.value.clusterSection = 'machines'
      stopClusterTopologyAutoRefresh()
      await loadClusterMachines(targetPage, cluster)
    }
    async function removeSelectedMachinesFromCluster() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const selected = new Set(selectedClusterOperationMachineIDs.value)
      const machines = clusterMachineItems.value.filter(machine => selected.has(machine.ID || machine.id))
      if (!machines.length) { error.value = '请先勾选需要移出集群的机器。'; return }
      if (!confirm(`确认将选中的 ${machines.length} 台机器移出集群 ${cluster}？机器、Agent 和 MySQL 数据不会被删除。`)) return
      error.value = ''
      try {
        const results = await Promise.allSettled(machines.map(machine => api(`/machines/${machine.ID || machine.id}/assign-cluster`, { method: 'DELETE' })))
        const succeeded = machines.filter((_, index) => results[index].status === 'fulfilled')
        const failed = machines.filter((_, index) => results[index].status === 'rejected')
        const succeededIDs = new Set(succeeded.map(machine => machine.ID || machine.id))
        selectedClusterOperationMachineIDs.value = selectedClusterOperationMachineIDs.value.filter(id => !succeededIDs.has(id))
        notice.value = `批量移出完成：${succeeded.length}/${machines.length} 台成功。`
        if (failed.length) {
          const firstError = results.find(item => item.status === 'rejected')?.reason?.message || '部分机器移出失败'
          error.value = `${failed.length} 台机器移出失败：${firstError}`
        }
        await refreshClusterMachinesAfterRemoval(succeeded.length)
      } catch (err) { error.value = err.message }
    }
    function topologyEndpoint(ip, port) { return `${String(ip || '').trim()}:${Number(port || 3306)}` }
    function topologyEdgeForNode(node) {
      const target = topologyEndpoint(node.ip, node.port)
      return clusterTopology.value.edges.find(edge => topologyEndpoint(edge.target_ip, edge.target_port) === target)
    }
    function topologyMachineForEndpoint(ip, port) {
      const endpoint = topologyEndpoint(ip, port)
      return clusterTopology.value.nodes.find(node => topologyEndpoint(node.ip, node.port) === endpoint)?.machine_id || ''
    }
    function architectureTypeLabel(type) {
      return ({ standalone: '独立实例', master_slave: '一主多从', dual_master: '双主架构', multi_master: '多主架构' })[type] || type
    }
    function architectureTopologyHasChanges() {
      return architectureForm.value.architecture !== architectureCurrent.value.type ||
        (architectureCurrent.value.primary_machine_ids.length > 0 && !architectureCurrent.value.primary_machine_ids.includes(architectureForm.value.primary_machine_id)) ||
        architectureForm.value.nodes.some(architectureNodeHasChanges)
    }
    function architectureVIPActionLabel() {
      return vipStates.value.some(item => item.current_holder_machine_id) ? '迁移' : '绑定'
    }
    const architectureAdjustmentTitle = computed(() => {
      const nodes = architectureForm.value.nodes
      const masters = nodes.filter(node => node.role === 'M')
      const replicas = nodes.filter(node => node.role === 'S')
      const names = items => items.map(node => architectureNodeName(node.machine_id)).join('、')
      if (!architectureTopologyHasChanges() && architectureForm.value.move_vip) {
        return `准备将 VIP ${architectureVIPActionLabel()}至 ${architectureNodeName(architectureForm.value.primary_machine_id)}`
      }
      if (architectureForm.value.architecture === 'standalone') return `准备调整为“${nodes.length} 个独立实例”`
      if (architectureForm.value.architecture === 'master_slave') {
        const topology = replicas.length === 1 ? '一主一从' : `一主 ${replicas.length} 从`
        return `准备调整为“${topology}”：${names(masters) || '待选择主节点'} 为主`
      }
      if (architectureForm.value.architecture === 'dual_master') return `准备调整为“双主架构”：${names(masters) || '待选择主节点'}`
      return `准备调整为“${masters.length} 主架构”：${names(masters) || '待选择主节点'}`
    })
    const architectureAdjustmentDetail = computed(() => {
      if (!architectureTopologyHasChanges() && architectureForm.value.move_vip) {
        const vip = vipConfigs.value.map(item => item.vip_address).filter(Boolean).join('、') || '业务 VIP'
        return `${vip} 将安全${architectureVIPActionLabel()}至 ${architectureNodeName(architectureForm.value.primary_machine_id)}。开始后先生成只读安全计划，确认后由 Manager 通过 Agent 执行。`
      }
      const edges = architectureDraftEdges().map(edge => `${architectureNodeName(edge.source)} ${edge.mutual ? '⇄' : '→'} ${architectureNodeName(edge.target)}`)
      const parts = []
      if (edges.length) parts.push(`目标复制关系：${edges.join('；')}`)
      else parts.push('目标节点之间不建立复制关系')
      if (architectureForm.value.move_vip) {
        const vip = vipConfigs.value.map(item => item.vip_address).filter(Boolean).join('、') || '业务 VIP'
        parts.push(`${vip} 将安全${architectureVIPActionLabel()}至 ${architectureNodeName(architectureForm.value.primary_machine_id)}`)
      }
      return `${parts.join('；')}。开始后先生成只读安全计划，确认后由 Manager 通过 Agent 执行。`
    })
    const architectureOperationVIPOnly = computed(() => Boolean(
      architectureRun.value?.request?.vip_only ||
      (architecturePlan.value && architectureForm.value.move_vip && !architectureTopologyHasChanges())
    ))
    const architectureVIPOperationAction = computed(() => {
      if (architectureRun.value?.request?.vip_only) return architectureRun.value.request.initialize_vip ? '绑定' : '漂移'
      return architectureVIPActionLabel()
    })
    function detectCurrentArchitecture() {
      const nodes = clusterTopology.value.nodes || []
      const edges = clusterTopology.value.edges || []
      const edgeSet = new Set(edges.map(edge => `${topologyEndpoint(edge.source_ip, edge.source_port)}>${topologyEndpoint(edge.target_ip, edge.target_port)}`))
      const mutualEdges = edges.filter(edge => edgeSet.has(`${topologyEndpoint(edge.target_ip, edge.target_port)}>${topologyEndpoint(edge.source_ip, edge.source_port)}`))
      const reportedMasters = nodes.filter(node => node.role === 'M' || node.role === 'M/S')
      let type = 'standalone'
      if (reportedMasters.length >= 3) type = 'multi_master'
      else if (mutualEdges.length >= 2) type = 'dual_master'
      else if (edges.length) type = 'master_slave'
      const incoming = new Set(edges.map(edge => topologyEndpoint(edge.target_ip, edge.target_port)))
      const outgoing = new Set(edges.map(edge => topologyEndpoint(edge.source_ip, edge.source_port)))
      let primaryMachineIDs = []
      if (type === 'multi_master') {
        primaryMachineIDs = reportedMasters.map(node => node.machine_id)
      } else if (type === 'dual_master') {
        const endpoints = new Set(mutualEdges.flatMap(edge => [topologyEndpoint(edge.source_ip, edge.source_port), topologyEndpoint(edge.target_ip, edge.target_port)]))
        primaryMachineIDs = nodes.filter(node => endpoints.has(topologyEndpoint(node.ip, node.port))).map(node => node.machine_id)
      } else if (type === 'master_slave') {
        primaryMachineIDs = nodes.filter(node => outgoing.has(topologyEndpoint(node.ip, node.port)) && !incoming.has(topologyEndpoint(node.ip, node.port))).map(node => node.machine_id)
        if (!primaryMachineIDs.length) primaryMachineIDs = nodes.filter(node => node.role === 'M').map(node => node.machine_id)
      }
      architectureCurrent.value = { type, label: architectureTypeLabel(type), primary_machine_ids: [...new Set(primaryMachineIDs)], nodes, edges }
      return architectureCurrent.value
    }
    function architectureCurrentNodeRole(machineID) {
      const currentNode = clusterTopology.value.nodes.find(item => item.machine_id === machineID)
      if (!currentNode) return ''
      const edge = topologyEdgeForNode(currentNode)
      return currentNode.role === 'M' || currentNode.role === 'M/S' ? 'M' : ((edge || currentNode.role === 'S') ? 'S' : 'I')
    }
    function rememberArchitectureDraft(label) {
      architectureDraftHistory.value.push({ label, form: JSON.parse(JSON.stringify(architectureForm.value)) })
      if (architectureDraftHistory.value.length > 20) architectureDraftHistory.value.shift()
    }
    function undoArchitectureDraft() {
      const previous = architectureDraftHistory.value.pop()
      if (!previous) return
      architectureForm.value = previous.form
      architecturePlan.value = null
      architectureRoleChangeDialog.value = null
      architectureRoleChangeFeedback.value = `已撤销：${previous.label}。草稿已恢复，线上架构未发生变化。`
      notice.value = architectureRoleChangeFeedback.value
    }
    function resetArchitectureDraft() {
      if (!architectureTopologyHasChanges() && !architectureForm.value.move_vip) return
      architectureForm.value = architectureDraftFromTopology(architectureCurrent.value)
      architectureDraftHistory.value = []
      architecturePlan.value = null
      architectureRoleChangeDialog.value = null
      architectureRoleChangeFeedback.value = '已恢复为当前线上架构，所有未执行的草稿变更均已清除。'
      notice.value = architectureRoleChangeFeedback.value
    }
    function architectureNodeChanged(node) {
      if (node.role === 'M') {
        node.delay_seconds = 0
        node.source_machine_id = ''
        architectureForm.value.primary_machine_id = node.machine_id
      }
      if (node.role === 'I') {
        node.delay_seconds = 0
        node.source_machine_id = ''
        if (architectureForm.value.primary_machine_id === node.machine_id) architectureForm.value.primary_machine_id = ''
      }
      const masters = architectureForm.value.nodes.filter(item => item.role === 'M')
      const independents = architectureForm.value.nodes.filter(item => item.role === 'I')
      if (node.role === 'S' && architectureForm.value.primary_machine_id === node.machine_id) architectureForm.value.primary_machine_id = masters[0]?.machine_id || ''
      if (independents.length === architectureForm.value.nodes.length) {
        architectureForm.value.architecture = 'standalone'
        architectureForm.value.primary_machine_id = ''
        architectureForm.value.move_vip = false
      }
      if (masters.length === 1) architectureForm.value.architecture = 'master_slave'
      if (masters.length === 2) architectureForm.value.architecture = 'dual_master'
      if (masters.length >= 3) {
        architectureForm.value.architecture = 'multi_master'
        architectureForm.value.move_vip = false
      }
      architecturePlan.value = null
    }
    function architectureNodeHasChanges(node) {
      const currentNode = clusterTopology.value.nodes.find(item => item.machine_id === node.machine_id && Number(item.port || 3306) === Number(node.port || 3306))
      if (!currentNode) return true
      const edge = topologyEdgeForNode(currentNode)
      const currentRole = architectureCurrentNodeRole(node.machine_id)
      const currentSource = edge ? topologyMachineForEndpoint(edge.source_ip, edge.source_port) : ''
      return node.role !== currentRole || (node.role === 'S' && (node.source_machine_id || '') !== currentSource) || (node.role === 'S' && Number(node.delay_seconds || 0) !== Number(edge?.sql_delay || 0))
    }
    function architectureNodeName(machineID) {
      const topologyNode = clusterTopology.value.nodes.find(item => item.machine_id === machineID)
      const instance = data.value.mysqlInstances.find(item => (item.MachineID || item.machine_id) === machineID)
      const machine = clusterMachineItems.value.find(item => (item.ID || item.id) === machineID)
      return topologyNode?.name || instance?.MachineName || instance?.machine_name || machine?.Name || machine?.name || machineID || '未设置'
    }
    function architectureNodeMeta(machineID) {
      const topologyNode = clusterTopology.value.nodes.find(item => item.machine_id === machineID)
      const instance = data.value.mysqlInstances.find(item => (item.MachineID || item.machine_id) === machineID)
      const machine = clusterMachineItems.value.find(item => (item.ID || item.id) === machineID)
      return {
        name: architectureNodeName(machineID),
        ip: topologyNode?.ip || instance?.MachineIP || instance?.machine_ip || machine?.IP || machine?.ip || '待上报',
        status: topologyNode?.error ? '心跳异常' : (instance?.Status || instance?.status || '正常运行')
      }
    }
    function architectureAvailableInstances() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const used = new Set(architectureForm.value.nodes.map(node => node.machine_id))
      return data.value.mysqlInstances.filter(item => {
        const machineID = item.MachineID || item.machine_id
        return machineID && !used.has(machineID) && (!cluster || (item.Cluster || item.cluster) === cluster)
      })
    }
    function addArchitectureInstance(instance) {
      const machineID = instance.MachineID || instance.machine_id
      if (!machineID || architectureForm.value.nodes.some(node => node.machine_id === machineID)) return
      const source = architectureForm.value.primary_machine_id || architectureForm.value.nodes.find(node => node.role === 'M')?.machine_id || ''
      architectureForm.value.nodes.push({ machine_id: machineID, port: Number(instance.Port || instance.port || 3306), role: 'S', source_machine_id: source, delay_seconds: 0, election_priority: 50 })
      architectureSelectedNode.value = machineID
      architecturePlan.value = null
      notice.value = `已将 ${architectureNodeName(machineID)} 加入目标拓扑，默认作为从节点。`
    }
    function setArchitectureNodeRole(node, role) {
      node.role = role
      architectureNodeChanged(node)
      if (role === 'S' && !node.source_machine_id) node.source_machine_id = architectureForm.value.primary_machine_id
      architectureSelectedNode.value = node.machine_id
    }
    function architectureRoleLabel(role) {
      return ({ M: '主节点', S: '从节点', I: '独立实例' })[role] || role
    }
    function requestArchitectureRoleChange(node, role) {
      if (!node || role === node.role) return
      architectureRoleChangeFeedback.value = ''
      const restoresOriginalDualMaster = role === 'M' && node.role === 'S' && architectureCurrent.value.type === 'dual_master' && architectureCurrentNodeRole(node.machine_id) === 'M'
      if (restoresOriginalDualMaster) {
        rememberArchitectureDraft(`将 ${architectureNodeName(node.machine_id)} 恢复为双主节点`)
        node.role = 'M'
        node.source_machine_id = ''
        node.delay_seconds = 0
        architectureNodeChanged(node)
        architectureSelectedNode.value = node.machine_id
        architectureRoleChangeFeedback.value = `已将 ${architectureNodeName(node.machine_id)} 恢复为主节点，双主草稿已恢复；无需触发切主弹窗。`
        notice.value = architectureRoleChangeFeedback.value
        return
      }
      const otherMasters = architectureForm.value.nodes.filter(item => item.role === 'M' && item.machine_id !== node.machine_id)
      const replacement = architectureForm.value.nodes.filter(item => item.machine_id !== node.machine_id && item.role === 'S').sort((a, b) => Number(b.election_priority || 0) - Number(a.election_priority || 0))[0]
      architectureRoleChangeDialog.value = { machine_id: node.machine_id, from_role: node.role, to_role: role, switchover: role === 'M' && otherMasters.length === 1, needs_replacement: role === 'S' && otherMasters.length === 0, replacement_machine_id: replacement?.machine_id || '', counterpart_machine_id: otherMasters[0]?.machine_id || '' }
    }
    function architectureRoleChangePromotedMachineID(change = architectureRoleChangeDialog.value) {
      if (!change) return ''
      if (change.needs_replacement) return change.replacement_machine_id || ''
      if (change.switchover || change.to_role === 'M') return change.machine_id
      return ''
    }
    function architectureRoleChangeDemotedMachineID(change = architectureRoleChangeDialog.value) {
      if (!change) return ''
      if (change.needs_replacement || (change.from_role === 'M' && change.to_role !== 'M')) return change.machine_id
      if (change.switchover) return change.counterpart_machine_id || ''
      return ''
    }
    function confirmArchitectureRoleChange() {
      const change = architectureRoleChangeDialog.value
      if (!change) return
      const node = architectureForm.value.nodes.find(item => item.machine_id === change.machine_id)
      if (!node) return
      if (change.needs_replacement && !architectureForm.value.nodes.some(item => item.machine_id === change.replacement_machine_id)) { error.value = '请先选择接替的新主节点。'; return }
      rememberArchitectureDraft(`将 ${architectureNodeName(change.machine_id)} 从${architectureRoleLabel(change.from_role)}调整为${architectureRoleLabel(change.to_role)}`)
      let feedback = ''
      if (change.needs_replacement) {
        const replacement = architectureForm.value.nodes.find(item => item.machine_id === change.replacement_machine_id)
        replacement.role = 'M'; replacement.source_machine_id = ''; replacement.delay_seconds = 0
        node.role = 'S'; node.source_machine_id = replacement.machine_id
        architectureForm.value.primary_machine_id = replacement.machine_id
        architectureNodeChanged(node)
        feedback = `草稿已更新：${architectureNodeName(replacement.machine_id)} 将接替为主节点，${architectureNodeName(node.machine_id)} 将降为从节点。`
      } else if (change.switchover) {
        const oldMaster = architectureForm.value.nodes.find(item => item.role === 'M' && item.machine_id !== node.machine_id)
        node.role = 'M'; node.source_machine_id = ''; node.delay_seconds = 0
        if (oldMaster) { oldMaster.role = 'S'; oldMaster.source_machine_id = node.machine_id }
        architectureForm.value.primary_machine_id = node.machine_id
        architectureNodeChanged(node)
        feedback = `草稿已更新：${architectureNodeName(node.machine_id)} 将提升为主节点${oldMaster ? `，${architectureNodeName(oldMaster.machine_id)} 将降为从节点` : ''}。`
      } else {
        setArchitectureNodeRole(node, change.to_role)
        feedback = `草稿已更新：${architectureNodeName(node.machine_id)} 将变更为${architectureRoleLabel(change.to_role)}。`
      }
      architectureRoleChangeDialog.value = null
      architectureRoleChangeFeedback.value = `${feedback} 此操作尚未影响线上，请继续生成并执行架构调整安全计划。`
      architecturePlan.value = null
      notice.value = architectureRoleChangeFeedback.value
    }
    function architectureDraftEdges() {
      const nodes = architectureForm.value.nodes
      const masters = nodes.filter(node => node.role === 'M')
      const edges = []
      if (masters.length === 2) {
        edges.push({ source: masters[0].machine_id, target: masters[1].machine_id, mutual: true })
      } else if (masters.length > 2) {
        masters.forEach((master, index) => edges.push({ source: master.machine_id, target: masters[(index + 1) % masters.length].machine_id, mutual: false, root: true }))
      }
      nodes.filter(node => node.role === 'S').forEach(node => {
        const source = node.source_machine_id || architectureForm.value.primary_machine_id || masters[0]?.machine_id
        if (source && source !== node.machine_id) edges.push({ source, target: node.machine_id, mutual: false })
      })
      return edges
    }
    function startArchitectureNodeDrag(event, node) {
      architectureDraggingNode.value = node.machine_id
      architectureSelectedNode.value = node.machine_id
      if (event?.dataTransfer) {
        event.dataTransfer.effectAllowed = 'move'
        event.dataTransfer.setData('text/plain', node.machine_id)
      }
    }
    function finishArchitectureNodeDrag() {
      architectureDraggingNode.value = ''
    }
    function reorderArchitectureNode(source, target) {
      const nodes = architectureForm.value.nodes
      const sourceIndex = nodes.findIndex(item => item.machine_id === source.machine_id)
      const targetIndex = nodes.findIndex(item => item.machine_id === target.machine_id)
      if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return
      const [moved] = nodes.splice(sourceIndex, 1)
      const insertionIndex = nodes.findIndex(item => item.machine_id === target.machine_id)
      nodes.splice(insertionIndex, 0, moved)
    }
    function dropArchitectureNode(target) {
      const source = architectureForm.value.nodes.find(item => item.machine_id === architectureDraggingNode.value)
      if (!source || source.machine_id === target.machine_id) { finishArchitectureNodeDrag(); return }
      if (source.role === target.role) {
        rememberArchitectureDraft(`调整 ${architectureNodeName(source.machine_id)} 的画布位置`)
        reorderArchitectureNode(source, target)
        notice.value = `已调整 ${architectureNodeName(source.machine_id)} 在${source.role === 'M' ? '根节点' : '叶子节点'}层的位置。`
      } else if (source.role === 'S' && target.role === 'M') {
        rememberArchitectureDraft(`将 ${architectureNodeName(source.machine_id)} 的复制源调整为 ${architectureNodeName(target.machine_id)}`)
        source.source_machine_id = target.machine_id
        architecturePlan.value = null
        notice.value = `已将 ${architectureNodeName(source.machine_id)} 的复制源设为 ${architectureNodeName(target.machine_id)}。`
      } else {
        requestArchitectureRoleChange(source, target.role)
      }
      finishArchitectureNodeDrag()
    }
    function vipCardTargetMachineID(item) {
      if (item?.vip_address === vipEditingAddress.value && architectureForm.value.move_vip && vipForm.value.target_machine_id) {
        return vipForm.value.target_machine_id
      }
      return vipStateFor(item).current_holder_machine_id || ''
    }
    function startArchitectureVIPDrag(event, item) {
      selectArchitectureVIPCard(item)
      if (event?.dataTransfer) {
        event.dataTransfer.effectAllowed = 'move'
        event.dataTransfer.setData('application/x-gmha-vip', item.vip_address)
        event.dataTransfer.setData('text/plain', `vip:${item.vip_address}`)
      }
    }
    function selectArchitectureVIPCard(item) {
      vipDraggingAddress.value = item.vip_address
      vipEditingAddress.value = item.vip_address
      syncVIPEditorFromCurrent()
      notice.value = `已拿起 VIP ${item.vip_address}，请拖放或点击目标主节点。`
    }
    function finishArchitectureVIPDrag() {
      vipDraggingAddress.value = ''
      vipMagnetTargetID.value = ''
    }
    function setVIPMagnetTarget(target) {
      if (!vipDraggingAddress.value || target.role !== 'M') return
      vipMagnetTargetID.value = target.machine_id
    }
    function clearVIPMagnetTarget(event, target) {
      if (event?.currentTarget?.contains?.(event.relatedTarget)) return
      if (vipMagnetTargetID.value === target.machine_id) vipMagnetTargetID.value = ''
    }
    async function dropArchitectureVIPOnNode(target) {
      const address = vipDraggingAddress.value
      const item = vipConfigs.value.find(config => config.vip_address === address)
      if (!item) { finishArchitectureVIPDrag(); return }
      if (target.role !== 'M') {
        notice.value = 'VIP 只能绑定到目标拓扑中的主节点。'
        finishArchitectureVIPDrag()
        return
      }
      const state = vipStateFor(item)
      if (state.current_holder_machine_id === target.machine_id) {
        notice.value = `VIP ${address} 已由 ${architectureNodeName(target.machine_id)} 持有，无需漂移。`
        finishArchitectureVIPDrag()
        return
      }
      const previousTarget = vipForm.value.target_machine_id
      const previousPrimary = architectureForm.value.primary_machine_id
      const previousMoveVIP = architectureForm.value.move_vip
      vipEditingAddress.value = address
      syncVIPEditorFromCurrent()
      vipForm.value.target_machine_id = target.machine_id
      architectureForm.value.primary_machine_id = target.machine_id
      architectureForm.value.move_vip = true
      vipSnapTargetID.value = target.machine_id
      setTimeout(() => { if (vipSnapTargetID.value === target.machine_id) vipSnapTargetID.value = '' }, 560)
      architecturePlan.value = null
      architectureRun.value = null
      await refreshVIPEditorInterfaces()
      vipDriftDialog.value = {
        vip_address: address,
        vip_prefix: item.vip_prefix,
        from_machine_id: state.current_holder_machine_id || '',
        target_machine_id: target.machine_id,
        previous_target_machine_id: previousTarget,
        previous_primary_machine_id: previousPrimary,
        previous_move_vip: previousMoveVIP,
        action: state.current_holder_machine_id ? 'migrate' : 'bind'
      }
      finishArchitectureVIPDrag()
    }
    function dropArchitectureCanvasItem(target) {
      if (vipDraggingAddress.value) return dropArchitectureVIPOnNode(target)
      return dropArchitectureNode(target)
    }
    function cancelVIPDrift() {
      const dialog = vipDriftDialog.value
      if (dialog) {
        vipForm.value.target_machine_id = dialog.previous_target_machine_id || ''
        architectureForm.value.primary_machine_id = dialog.previous_primary_machine_id || ''
        architectureForm.value.move_vip = Boolean(dialog.previous_move_vip)
      }
      vipDriftDialog.value = null
      architecturePlan.value = null
    }
    async function confirmVIPDrift() {
      if (!vipDriftDialog.value) return
      vipDriftDialog.value = null
      await previewArchitectureAdjustment()
    }
    function dropArchitectureLayer(role) {
      const source = architectureForm.value.nodes.find(item => item.machine_id === architectureDraggingNode.value)
      if (!source) return
      if (source.role !== role) {
        requestArchitectureRoleChange(source, role)
      } else {
        const nodes = architectureForm.value.nodes
        rememberArchitectureDraft(`调整 ${architectureNodeName(source.machine_id)} 的画布位置`)
        const sourceIndex = nodes.findIndex(item => item.machine_id === source.machine_id)
        const [moved] = nodes.splice(sourceIndex, 1)
        let lastRoleIndex = -1
        nodes.forEach((item, index) => { if (item.role === role) lastRoleIndex = index })
        nodes.splice(lastRoleIndex + 1, 0, moved)
      }
      finishArchitectureNodeDrag()
    }
    function startArchitectureLink(node) {
      architectureLinkSource.value = node.machine_id
      architectureSelectedNode.value = node.machine_id
      notice.value = `已选择 ${architectureNodeName(node.machine_id)} 作为起点，请点击目标实例完成连线。`
    }
    function completeArchitectureLink(target) {
      const source = architectureForm.value.nodes.find(node => node.machine_id === architectureLinkSource.value)
      if (!source || source.machine_id === target.machine_id) { architectureLinkSource.value = ''; return }
      rememberArchitectureDraft(`调整 ${architectureNodeName(source.machine_id)} 与 ${architectureNodeName(target.machine_id)} 的复制关系`)
      const reverseExists = source.role === 'S' && source.source_machine_id === target.machine_id
      if (reverseExists) {
        source.role = 'M'; source.source_machine_id = ''; source.delay_seconds = 0
        target.role = 'M'; target.source_machine_id = ''; target.delay_seconds = 0
        architectureForm.value.architecture = 'dual_master'
        architectureForm.value.primary_machine_id = target.machine_id
        notice.value = '已建立双向复制，目标架构已自动切换为双主。'
      } else {
        source.role = 'M'; source.source_machine_id = ''; source.delay_seconds = 0
        target.role = 'S'; target.source_machine_id = source.machine_id
        architectureForm.value.primary_machine_id = source.machine_id
        architectureForm.value.architecture = 'master_slave'
        notice.value = `已设置 ${architectureNodeName(target.machine_id)} 跟随 ${architectureNodeName(source.machine_id)}。`
      }
      architectureLinkSource.value = ''
      architectureSelectedNode.value = target.machine_id
      architecturePlan.value = null
    }
    async function kickArchitectureNode(node) {
      const machine = clusterMachineItems.value.find(item => (item.ID || item.id) === node.machine_id)
      if (!machine) { error.value = '未找到该实例所属机器，无法移出集群。'; return }
      architectureLinkSource.value = ''
      await removeMachineFromCluster(machine)
    }
    function applyArchitecturePreset(type) {
      const nodes = architectureForm.value.nodes
      if (!nodes.length) return
      rememberArchitectureDraft(`应用“${architectureTypeLabel(type)}”快捷转换`)
      architectureRoleChangeFeedback.value = ''
      if (type === 'standalone') {
        nodes.forEach(node => { node.role = 'I'; node.source_machine_id = ''; node.delay_seconds = 0 })
        architectureForm.value.architecture = 'standalone'
        architectureForm.value.primary_machine_id = ''
        architectureForm.value.move_vip = false
        architecturePlan.value = null
        architectureRoleChangeFeedback.value = '已生成“全部独立”草稿。执行时 Manager 会冻结写入、等待复制追平、通过 PT 校验数据一致后，才解除复制并恢复各实例独立可写。'
        return
      }
      const dual = type === 'dual_master'
      if (!nodes.some(node => node.machine_id === architectureForm.value.primary_machine_id)) architectureForm.value.primary_machine_id = nodes[0]?.machine_id || ''
      const primaryIndex = nodes.findIndex(node => node.machine_id === architectureForm.value.primary_machine_id)
      nodes.forEach((node, index) => {
        node.role = index === primaryIndex || (dual && index === (primaryIndex === 0 ? 1 : 0)) ? 'M' : 'S'
        if (node.role === 'M') {
          node.delay_seconds = 0
          node.source_machine_id = ''
        } else if (!node.source_machine_id || !nodes.some(source => source.machine_id === node.source_machine_id && source.role === 'M')) {
          node.source_machine_id = nodes[primaryIndex]?.machine_id || ''
        }
      })
      architectureForm.value.architecture = type
      architecturePlan.value = null
      architectureRoleChangeFeedback.value = dual ? '已生成“双主”草稿。Manager 将通过 Agent 建立双向复制，并强制运行 PT 数据一致性验证。' : '已生成“一主多从”草稿。Manager 将通过 Agent 建立复制，并强制运行 PT 数据一致性验证。'
    }
    function applyArchitectureRoles() { applyArchitecturePreset(architectureForm.value.architecture) }
    function architectureDraftFromTopology(current = detectCurrentArchitecture()) {
      const nodes = clusterTopology.value.nodes.map((node, index) => {
        const edge = topologyEdgeForNode(node)
        const isCurrentMaster = node.role === 'M' || node.role === 'M/S'
        const role = current.type === 'standalone' ? 'I' : (isCurrentMaster ? 'M' : 'S')
        return { machine_id: node.machine_id, port: Number(node.port || 3306), role, source_machine_id: edge ? topologyMachineForEndpoint(edge.source_ip, edge.source_port) : '', delay_seconds: Number(edge?.sql_delay || 0), election_priority: Math.max(0, 100 - index) }
      })
      let currentMaster = current.primary_machine_ids[0] || ''
      if (!currentMaster && nodes.length && current.type !== 'standalone') {
        currentMaster = nodes[0].machine_id
        nodes[0].role = 'M'
      }
      return { architecture: current.type, primary_machine_id: currentMaster, current_master_machine_id: current.type === 'standalone' ? '' : currentMaster, move_vip: false, root_password: '', replication_user: '', replication_password: '', nodes }
    }
    function projectedTopologyFromArchitectureRun(run) {
      const requested = run?.request?.nodes || []
      const architecture = run?.request?.architecture || 'standalone'
      const currentNodes = clusterTopology.value.nodes || []
      const nodeByMachine = new Map(currentNodes.map(node => [node.machine_id, node]))
      const nodes = requested.map(request => {
        const current = nodeByMachine.get(request.machine_id) || {}
        let role = 'standalone'
        if (request.role === 'S') role = 'S'
        else if (request.role === 'M') role = ['dual_master', 'multi_master'].includes(architecture) ? 'M/S' : 'M'
        return { ...current, machine_id: request.machine_id, name: current.name || architectureNodeName(request.machine_id), ip: current.ip || architectureNodeMeta(request.machine_id).ip, port: Number(request.port || current.port || 3306), role, read_only: request.role === 'S' ? 'ON' : 'OFF', super_read_only: request.role === 'S' ? 'ON' : 'OFF', error: '' }
      })
      const endpointByMachine = new Map(nodes.map(node => [node.machine_id, { ip: node.ip, port: node.port, name: node.name }]))
      const masters = requested.filter(node => node.role === 'M')
      const primaryID = run?.plan?.selected_candidate?.machine_id || run?.request?.preferred_new_master_machine_id || masters[0]?.machine_id || ''
      const edges = []
      if (architecture === 'dual_master' || architecture === 'multi_master') {
        masters.forEach((node, index) => {
          let sourceID = node.source_machine_id
          if (!sourceID || sourceID === node.machine_id || !masters.some(master => master.machine_id === sourceID)) sourceID = masters[(index - 1 + masters.length) % masters.length]?.machine_id
          const source = endpointByMachine.get(sourceID)
          const target = endpointByMachine.get(node.machine_id)
          if (source && target && sourceID !== node.machine_id) edges.push({ source_ip: source.ip, source_port: source.port, target_ip: target.ip, target_port: target.port, source_name: source.name, target_name: target.name, io_running: 'Yes', sql_running: 'Yes', lag: '0', sql_delay: 0 })
        })
      }
      requested.filter(node => node.role === 'S').forEach(node => {
        const sourceID = node.source_machine_id || primaryID
        const source = endpointByMachine.get(sourceID)
        const target = endpointByMachine.get(node.machine_id)
        if (source && target && sourceID !== node.machine_id) edges.push({ source_ip: source.ip, source_port: source.port, target_ip: target.ip, target_port: target.port, source_name: source.name, target_name: target.name, io_running: 'Yes', sql_running: 'Yes', lag: '0', sql_delay: Number(node.delay_seconds || 0) })
      })
      return { ...clusterTopology.value, nodes, edges }
    }
    async function refreshTopologyAfterArchitectureRun(run) {
      const projected = projectedTopologyFromArchitectureRun(run)
      clusterTopology.value = projected
      let current = detectCurrentArchitecture()
      let heartbeatConfirmed = false
      for (let attempt = 0; attempt < 5; attempt++) {
        await refreshClusterTopology()
        current = detectCurrentArchitecture()
        if (current.type === run?.request?.architecture) { heartbeatConfirmed = true; break }
        clusterTopology.value = projected
        current = detectCurrentArchitecture()
        if (attempt < 4) await new Promise(resolve => setTimeout(resolve, 1000))
      }
      architectureForm.value = architectureDraftFromTopology(current)
      architecturePlan.value = null
      return { ...current, heartbeat_confirmed: heartbeatConfirmed }
    }
    async function openArchitectureAdjustment() {
      stopClusterTopologyAutoRefresh()
      data.value.clusterSection = 'architecture'
      architecturePlan.value = null
      architectureRun.value = null
      architectureLinkSource.value = ''
      architectureSelectedNode.value = ''
      architectureRoleChangeDialog.value = null
      architectureRoleChangeFeedback.value = ''
      architectureDraftHistory.value = []
      architectureDraggingNode.value = ''
      await Promise.all([refreshClusterTopology(), loadClusterMachines(1), loadVIPConfigs()])
      const current = detectCurrentArchitecture()
      architectureForm.value = architectureDraftFromTopology(current)
      await refreshVIPManagement(true)
    }
    async function loadVIPConfigs(scan = false) {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      if (!cluster) return
      try {
        if (scan) await api(`/clusters/${encodeURIComponent(cluster)}/vip/scan`, { method: 'POST' })
        const [configs, states] = await Promise.all([
          api(`/clusters/${encodeURIComponent(cluster)}/vip/config`),
          api(`/clusters/${encodeURIComponent(cluster)}/vip/status`)
        ])
        vipConfigs.value = configs || []
        vipStates.value = states || []
        if (!vipConfigs.value.length) {
          await beginNewVIP()
        } else {
          if (!vipConfigs.value.some(item => item.vip_address === vipEditingAddress.value)) vipEditingAddress.value = vipConfigs.value[0].vip_address
          syncVIPEditorFromCurrent()
        }
      } catch (err) { vipConfigs.value = []; vipStates.value = []; error.value = err.message }
    }
    async function refreshVIPManagement(refreshInterfaces = false) {
      vipBusy.value = true
      error.value = ''
      try {
        await loadClusterMachines(1)
        if (!vipForm.value.target_machine_id || !clusterMachineItems.value.some(item => (item.ID || item.id) === vipForm.value.target_machine_id)) {
          const primary = architectureForm.value.primary_machine_id || clusterTopology.value.nodes.find(node => ['M', 'M/S'].includes(node.role))?.machine_id
          vipForm.value.target_machine_id = primary || (clusterMachineItems.value[0]?.ID || clusterMachineItems.value[0]?.id || '')
        }
        if ((refreshInterfaces || !vipInterfaceOptions.value.length) && vipForm.value.target_machine_id) {
          const target = clusterMachineItems.value.find(item => (item.ID || item.id) === vipForm.value.target_machine_id)
          if (target) {
            const id = target.ID || target.id
            clusterMachineStaticInfo.value[id] = await api(`/machines/${encodeURIComponent(id)}/static-info`, { method: 'POST' })
          }
        }
        if (!vipInterfaceOptions.value.some(item => item.name === vipForm.value.default_interface)) vipForm.value.default_interface = vipInterfaceOptions.value[0]?.name || ''
        await loadVIPConfigs(true)
      } catch (err) { error.value = err.message }
      finally { vipBusy.value = false }
    }
    // Compatibility alias for stale in-memory views during hot reload. The
    // navigation entry is removed; VIP management now lives in architecture.
    const openVIPManagement = refreshVIPManagement
    function vipStateFor(item) {
      return vipStates.value.find(state => state.vip_address === item.vip_address) || { vip_status: 'UNKNOWN', current_holder_machine_id: '', current_interface: '' }
    }
    function vipMachineName(machineID) {
      const machine = clusterMachineItems.value.find(item => (item.ID || item.id) === machineID)
      return machine ? `${machine.Name || machine.name} · ${machine.IP || machine.ip}` : machineID || '未绑定'
    }
    function vipStatusLabel(status) {
      return ({ BOUND: '已绑定', UNBOUND: '未绑定', CONFLICT: '多机冲突', FAILED: '执行失败', MISMATCH: '持有者异常', UNKNOWN: '待检测' })[status] || status
    }
    function syncVIPEditorFromCurrent() {
      const item = vipConfigs.value.find(config => config.vip_address === vipEditingAddress.value)
      if (!item) return
      const currentState = vipStateFor(item)
      vipForm.value = {
        vip_name: item.vip_name || '业务 VIP', vip_address: item.vip_address, vip_prefix: Number(item.vip_prefix || 24),
        default_interface: currentState.current_interface || item.default_interface || '',
        target_machine_id: currentState.expected_holder_machine_id || item.target_machine_id || currentState.current_holder_machine_id || architectureForm.value.primary_machine_id || '',
        arping_count: Number(item.arping_count || 3)
      }
    }
    async function selectVIPForEdit(address) {
      vipEditingAddress.value = address
      syncVIPEditorFromCurrent()
      if (vipForm.value.target_machine_id && !vipInterfaceOptions.value.length) await refreshVIPEditorInterfaces()
    }
    async function beginNewVIP() {
      vipEditingAddress.value = ''
      vipForm.value = { vip_name: '业务 VIP', vip_address: '', vip_prefix: 24, default_interface: '', target_machine_id: architectureForm.value.primary_machine_id || '', arping_count: 3 }
      if (!vipForm.value.target_machine_id) vipForm.value.target_machine_id = architectureForm.value.nodes.find(item => item.role === 'M')?.machine_id || ''
      if (vipForm.value.target_machine_id) await refreshVIPEditorInterfaces()
    }
    async function refreshVIPEditorInterfaces() {
      const target = clusterMachineItems.value.find(item => (item.ID || item.id) === vipForm.value.target_machine_id)
      if (!target) return
      vipBusy.value = true
      try {
        const id = target.ID || target.id
        clusterMachineStaticInfo.value[id] = await api(`/machines/${encodeURIComponent(id)}/static-info`, { method: 'POST' })
        if (!vipInterfaceOptions.value.some(item => item.name === vipForm.value.default_interface)) vipForm.value.default_interface = vipInterfaceOptions.value[0]?.name || ''
      } catch (err) { error.value = err.message }
      finally { vipBusy.value = false }
    }
    async function architectureVIPTargetChanged() {
      architectureForm.value.primary_machine_id = vipForm.value.target_machine_id
      architectureForm.value.move_vip = architectureForm.value.architecture !== 'standalone'
      architecturePlan.value = null
      await refreshVIPEditorInterfaces()
    }
    async function saveVIPConfig() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      if (!vipForm.value.target_machine_id || !vipForm.value.default_interface) { error.value = '请选择 VIP 持有机器和业务网卡。'; return }
      vipBusy.value = true
      try {
        await api(`/clusters/${encodeURIComponent(cluster)}/vip/config`, { method: 'POST', body: JSON.stringify(vipForm.value) })
        const savedAddress = vipForm.value.vip_address
        notice.value = `VIP ${savedAddress} 已保存、绑定并完成单持有者复检。`
        vipEditingAddress.value = savedAddress
        architecturePlan.value = null
        await loadVIPConfigs(false)
        syncVIPEditorFromCurrent()
      } catch (err) { error.value = err.message }
      finally { vipBusy.value = false }
    }
    async function deleteVIPConfig(item) {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      if (!confirm(`确认从所有集群节点撤销 VIP ${item.vip_address}，复检通过后删除配置？`)) return
      vipBusy.value = true
      try { await api(`/clusters/${encodeURIComponent(cluster)}/vip/config?vip=${encodeURIComponent(item.vip_address)}`, { method: 'DELETE' }); architecturePlan.value = null; notice.value = `VIP ${item.vip_address} 已从实机撤销并删除配置。`; if (vipEditingAddress.value === item.vip_address) vipEditingAddress.value = ''; await loadVIPConfigs(false); if (!vipConfigs.value.length) beginNewVIP() } catch (err) { error.value = err.message }
      finally { vipBusy.value = false }
    }
    function architectureAdjustmentPayload() {
      const currentVIPHolder = vipStates.value.map(item => item.current_holder_machine_id).find(Boolean) || ''
      const vipOnly = architectureForm.value.move_vip && !architectureTopologyHasChanges()
      return {
        architecture: architectureForm.value.architecture,
        current_architecture: architectureCurrent.value.type,
        current_master_machine_id: architectureForm.value.move_vip ? currentVIPHolder : (architectureCurrent.value.primary_machine_ids[0] || ''),
        preferred_new_master_machine_id: architectureForm.value.primary_machine_id,
        move_vip: architectureForm.value.move_vip,
        initialize_vip: architectureForm.value.move_vip && !currentVIPHolder,
        vip_only: vipOnly,
        management_users: ['root', 'monitor', 'mha', 'backup', 'repl'],
        nodes: architectureForm.value.nodes
      }
    }
    async function previewArchitectureAdjustment() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      if (!cluster || architectureForm.value.nodes.length < 2) { error.value = '架构调整至少需要两个 MySQL 实例。'; return }
      architectureSubmitting.value = true
      try {
        const payload = architectureAdjustmentPayload()
        architecturePlan.value = await api(`/clusters/${encodeURIComponent(cluster)}/architecture/plan`, { method: 'POST', body: JSON.stringify(payload) })
        architecturePlanDialog.value = true
        notice.value = architecturePlan.value.executable ? (payload.vip_only ? `VIP ${payload.initialize_vip ? '绑定' : '漂移'}预检通过，请核对执行顺序。` : '架构调整预检通过，请核对执行顺序。') : '预检完成，但存在阻断项，暂不能执行。'
      } catch (err) { error.value = err.message } finally { architectureSubmitting.value = false }
    }
    async function submitArchitectureAdjustment() {
      if (!architecturePlan.value) { error.value = '请先生成并检查架构调整计划。'; return }
      if (!architecturePlan.value.executable) { error.value = '当前计划存在安全阻断项，不能执行。'; return }
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      architectureSubmitting.value = true
      try {
        const payload = architectureAdjustmentPayload()
        architectureRun.value = await api(`/clusters/${encodeURIComponent(cluster)}/architecture/start`, { method: 'POST', body: JSON.stringify(payload) })
        architecturePlanDialog.value = true
        notice.value = `${payload.vip_only ? `VIP ${payload.initialize_vip ? '绑定' : '漂移'}安全流程` : '安全架构调整'}已启动：${architectureRun.value.run_id}`
        syncArchitectureRunTaskSummary()
        pollArchitectureRun()
      } catch (err) { error.value = err.message } finally { architectureSubmitting.value = false }
    }
    async function pollArchitectureRun() {
      clearTimeout(architecturePollTimer)
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      const runID = architectureRun.value?.run_id
      if (!cluster || !runID) return
      try { architectureRun.value = await api(`/clusters/${encodeURIComponent(cluster)}/architecture/${encodeURIComponent(runID)}`) } catch (err) { error.value = err.message; return }
      syncArchitectureRunTaskSummary()
      if (!['success','failed'].includes(architectureRun.value.status)) architecturePollTimer = setTimeout(pollArchitectureRun, 2000)
      else {
        const vipOnly = Boolean(architectureRun.value.request?.vip_only)
        const vipAction = architectureRun.value.request?.initialize_vip ? '绑定' : '漂移'
        notice.value = architectureRun.value.status === 'success' ? (vipOnly ? `VIP ${vipAction}成功，唯一持有者复检已通过。` : '架构调整执行成功，实时拓扑已更新。') : (vipOnly ? `VIP ${vipAction}失败，请查看步骤和 Agent 任务日志。` : '架构调整执行失败，请查看步骤和 Agent 任务日志。')
        if (architectureRun.value.status === 'success') {
          if (vipOnly) {
            architectureForm.value.move_vip = false
            await loadVIPConfigs(true)
            syncVIPEditorFromCurrent()
            architectureRoleChangeFeedback.value = `VIP ${vipAction}成功：${vipForm.value.vip_address} 当前仅由 ${architectureNodeName(vipEditorState.value.current_holder_machine_id)} 持有。完整安全步骤已记录到任务中心。`
          } else {
            const current = await refreshTopologyAfterArchitectureRun(architectureRun.value)
            architectureRoleChangeFeedback.value = `架构调整成功：拓扑已更新为“${architectureTypeLabel(current.type)}”${current.heartbeat_confirmed ? '，实时心跳已确认' : '；Manager 实机校验已通过，等待下一次心跳同步'}。Manager 与 Agent 的全部步骤和 PT 校验结果已记录到任务中心。`
          }
        } else {
          await refreshClusterTopology()
          detectCurrentArchitecture()
        }
        await refresh()
      }
    }
    async function confirmArchitectureForce() {
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name
      if (!confirm('复制在 60 秒内未追平。强制切主可能造成数据丢失，确认继续？')) return
      try {
        architectureRun.value = await api(`/clusters/${encodeURIComponent(cluster)}/architecture/${encodeURIComponent(architectureRun.value.run_id)}/force`, { method: 'POST' })
        notice.value = '已确认强制切主，状态机将继续执行。'
        pollArchitectureRun()
      } catch (err) { error.value = err.message }
    }
    function architectureRunStepResult(step) {
      const matches = (architectureRun.value?.step_results || []).filter(item => item.code === step.code)
      return matches[matches.length - 1] || null
    }
    function architectureRunStepStatus(step) {
      const result = architectureRunStepResult(step)
      if (result) return result.status
      if (step.code === 'acquire_lock' && architectureRun.value && architectureRun.value.status !== 'pending') return 'success'
      if (step.code === 'release_lock' && architectureRun.value?.status === 'success') return 'success'
      if (!result && architectureRun.value?.status === 'success') return 'success'
      if (architectureRun.value?.current_step === step.code) return 'running'
      return 'pending'
    }
    function architectureRunProgress() {
      const steps = architectureRun.value?.plan?.steps || []
      if (!steps.length) return 0
      if (architectureRun.value?.status === 'success') return 100
      return Math.round(steps.filter(step => architectureRunStepStatus(step) === 'success').length * 100 / steps.length)
    }
    function syncArchitectureRunTaskSummary() {
      const run = architectureRun.value
      if (!run?.run_id) return
      const cluster = selectedClusterDetail.value?.Name || selectedClusterDetail.value?.name || run.cluster_id
      const summary = { ID: run.run_id, Type: 'architecture_adjustment', MachineID: cluster, MachineName: `集群 ${cluster}`, Cluster: cluster, Status: run.status === 'success' ? 'success' : run.status === 'failed' ? 'failed' : 'running', ProgressPercent: architectureRunProgress(), CurrentStep: run.current_step || '等待启动', CreatedAt: run.created_at }
      const index = data.value.tasks.findIndex(item => (item.ID || item.id) === run.run_id)
      if (index >= 0) data.value.tasks.splice(index, 1, summary)
      else data.value.tasks.unshift(summary)
    }
    async function openArchitectureRunTask() {
      if (!architectureRun.value?.run_id) return
      await openTaskDetail({ id: architectureRun.value.run_id })
    }
    function showClusterCapability(name) {
      notice.value = name === 'upgrade'
        ? 'MySQL 内核升级任务接口尚未开发，当前入口仅用于能力预告。'
        : name === 'ha'
          ? '高可用与 VIP 属于当前集群的运维能力，配置接口尚未开发。'
          : '集群备份任务接口尚未开发，当前入口仅用于能力预告。'
    }
    async function saveCluster() {
      const form = clusterForm.value
      try {
        if (form.old_name) await api(`/clusters/${encodeURIComponent(form.old_name)}`, { method: 'PUT', body: JSON.stringify({ new_name: form.name, description: form.description }) })
        else await api('/clusters', { method: 'POST', body: JSON.stringify({ name: form.name, description: form.description }) })
        notice.value = form.old_name ? '集群信息已更新，机器归属已同步。' : '集群已创建。现在可在机器详情中分配机器。'
        showClusterEditor.value = false
        await refresh()
      } catch (err) { error.value = err.message }
    }
    async function deleteCluster(item) {
      const name = item.Name || item.name
      const members = clusterMachines(item).length
      if (!confirm(`确认删除集群 ${name}？${members ? `其中 ${members} 台机器将变为未分配集群。` : ''}`)) return
      try { await api(`/clusters/${encodeURIComponent(name)}`, { method: 'DELETE' }); notice.value = `集群 ${name} 已删除，机器记录仍保留。`; closeClusterDetail(); await refresh() }
      catch (err) { error.value = err.message }
    }
    async function cleanupCluster(item) {
      const name = item.Name || item.name
      const members = clusterMachines(item).length
      if (!confirm(`高风险操作：将按 CLI“集群一键清理”流程卸载 ${name} 内机器的 MySQL 与 GMHA Agent，并清理关联记录。是否继续？`)) return
      if (prompt(`二次确认：请输入 CLEAN CLUSTER ${name}`) !== `CLEAN CLUSTER ${name}`) { error.value = '二次确认内容不匹配，已取消集群清理。'; return }
      clusterCleanupResult.value = null
      try {
        clusterCleanupResult.value = await api(`/clusters/${encodeURIComponent(name)}/cleanup`, { method: 'POST' })
        showClusterCleanup.value = true
        notice.value = `集群 ${name} 已完成清理。`
        await refresh()
      } catch (err) {
        clusterCleanupResult.value = err.payload?.result || null
        showClusterCleanup.value = !!clusterCleanupResult.value
        error.value = `集群清理未完全成功：${err.message}`
        await refresh()
      }
    }
    function clusterMachines(item) { return item?.Machines || item?.machines || [] }
    function clusterMachineCount(item) { return clusterMachines(item).length }
    function clusterAgentCount(item) {
      const name = item?.Name || item?.name
      return data.value.agents.filter(agent => machineCluster(agent) === name).length
    }
    async function loadClusterPage(page = clusterPage.value) {
      try {
        const keyword = clusterKeyword.value.trim()
        const result = await api(`/clusters?page=${page}&page_size=12&keyword=${encodeURIComponent(keyword)}`)
        if (Array.isArray(result)) {
          clusterPageItems.value = result
          clusterTotal.value = result.length
          clusterPage.value = 1
          return
        }
        clusterPageItems.value = result.items || []
        clusterTotal.value = result.total || 0
        clusterPage.value = result.page || page
      } catch (err) { error.value = err.message }
    }
    async function searchClusterPage() { clusterPage.value = 1; await loadClusterPage(1) }
    async function changeClusterPage(delta) {
      const next = clusterPage.value + delta
      if (next < 1 || (next - 1) * 12 >= clusterTotal.value) return
      await loadClusterPage(next)
    }
    async function loadClusterCandidates(page = clusterCandidatePage.value) {
      try {
        const result = await api(`/machines?page=${page}&page_size=${pageSize}`)
        clusterCandidatePage.value = result.page || page
        clusterCandidateTotal.value = result.total || 0
        clusterCandidates.value = result.items || []
      } catch (err) {
        clusterCandidates.value = []
        clusterCandidateTotal.value = 0
        clusterCandidatesError.value = err.message
        error.value = err.message
        throw err
      }
    }
    async function openClusterMembers(item) {
      selectedClusterForMembers.value = item
      selectedClusterMachineIDs.value = []
      clusterMemberAssignResult.value = null
      clusterCandidatePage.value = 1
      showClusterMembers.value = true
      clusterCandidatesLoading.value = true
      clusterCandidatesError.value = ''
      try { await loadClusterCandidates(1) }
      catch (err) { clusterCandidatesError.value = err.message }
      finally { clusterCandidatesLoading.value = false }
    }
    async function changeClusterCandidatePage(delta) {
      const next = clusterCandidatePage.value + delta
      if (next < 1 || (next - 1) * pageSize >= clusterCandidateTotal.value) return
      selectedClusterMachineIDs.value = []
      clusterCandidatesLoading.value = true
      clusterCandidatesError.value = ''
      try { await loadClusterCandidates(next) }
      catch (_) { /* 错误已显示在弹窗内 */ }
      finally { clusterCandidatesLoading.value = false }
    }
    async function assignClusterMembers() {
      const cluster = selectedClusterForMembers.value?.Name || selectedClusterForMembers.value?.name
      const ids = selectedClusterMachineIDs.value
      if (!cluster || !ids.length) { error.value = '请选择至少一台机器。'; return }
      if (!confirm(`确认将 ${ids.length} 台机器划入集群 ${cluster}？已有其他集群归属的机器会被移动。每台机器将执行 CLI 同一 Agent 检查与静态信息采集流程。`)) return
      try {
        clusterMemberAssignResult.value = await api(`/clusters/${encodeURIComponent(cluster)}/members`, { method: 'POST', body: JSON.stringify({ machine_ids: ids }) })
        notice.value = `${ids.length} 台机器已划入集群 ${cluster}。`
        selectedClusterMachineIDs.value = []
        await Promise.all([refresh(), loadClusterCandidates(clusterCandidatePage.value)])
        if (selectedClusterDetail.value) await openClusterDetail(selectedClusterDetail.value)
      } catch (err) {
        clusterMemberAssignResult.value = err.payload || null
        error.value = err.message
      }
    }
    function showMySQLMachine(item) { const ip=item.MachineIP||item.machine_ip; const machine=data.value.machines.find(m => (m.IP||m.ip)===ip); if (!machine) { error.value='未找到该实例关联的机器记录。'; return }; showMachine(machine.ID||machine.id) }
    async function assignCredential() {
      try {
        await api(`/ssh-credentials/${selectedCredential.value}/assign`, { method: 'POST', body: JSON.stringify({ machine_ids: assignedMachineIDs.value }) })
        notice.value = `凭证已分配给 ${assignedMachineIDs.value.length} 台机器。`; showAssign.value = false; await refresh()
      } catch (err) { error.value = err.message }
    }
    function chooseCredential(id) { selectedCredential.value = id; assignedMachineIDs.value = data.value.machines.filter(item => (item.CredentialID || item.credential_id) === id).map(item => item.ID || item.id); showAssign.value = true }
    function applyCredential() {
      const credential = data.value.credentials.find(item => item.id === form.value.credential_id)
      if (credential) { form.value.ssh_user = credential.ssh_user; form.value.ssh_password = '' }
    }
    function loadKeyFile(event) { const file = event.target.files?.[0]; if (!file) return; const reader = new FileReader(); reader.onload = () => { credentialForm.value.private_key = reader.result }; reader.readAsText(file) }
    function errorSummary(message, limit = 180) {
      const normalized = safeLog(message).replace(/\s+/g, ' ').trim()
      if (!normalized) return ''
	  const endpoint = normalized.match(/(?:dial tcp\s+)?([0-9a-f:.]+:\d+)/i)?.[1] || '目标 SSH 端口'
	  if (/no route to host/i.test(normalized)) return `目标机网络不可达：Manager 无法连接 ${endpoint}。请检查机器是否开机、Manager 到目标网段的路由、防火墙和安全组。`
	  if (/connection refused/i.test(normalized)) return `目标机拒绝 SSH 连接：${endpoint}。请确认 sshd 已启动且 SSH 端口配置正确。`
	  if (/permission denied|unable to authenticate|authentication failed/i.test(normalized)) return `目标机 SSH 认证失败。请在“机器与凭证”中更新该机器关联的 SSH 用户、密码或私钥。`
      const first = normalized.split(/\s+\|\s+/).find(part => part.trim()) || '操作未完成'
      const summary = first.trim()
      return summary.length > limit ? `${summary.slice(0, Math.max(1, limit - 1))}…` : summary
    }
    function machineLastError(machine) { return machine?.LastError || machine?.last_error || '' }
    function machineStatus(machine) {
      const code = state(machine?.Status || machine?.status)
      const labels = {
        pending: '等待连接',
        ssh_connected: 'SSH 已连接',
        ssh_trust_ready: 'SSH 互信就绪',
        agent_installing: 'Agent 安装中',
        agent_online: 'Agent 在线',
        agent_error: 'Agent 异常',
        ssh_failed: 'SSH 失败'
      }
      return { code, label: labels[code] || '状态未知' }
    }
    function machineCluster(machine) { return machine?.Cluster || machine?.cluster || '未分配集群' }
    function machineAgentInstallDir(machine) {
      const direct = machine?.AgentInstallDir || machine?.agent_install_dir
      if (direct) return direct
      const ip = machine?.IP || machine?.ip
      const agent = data.value.agents.find(item => (item.IP || item.ip) === ip)
      return agent?.InstallDir || agent?.install_dir || '尚未安装'
    }
    function agentStatus(agent) {
      const heartbeat = state(agent?.HeartbeatState || agent?.heartbeat_state)
      const install = state(agent?.InstallState || agent?.install_state)
      if (heartbeat === 'online' || heartbeat === 'degraded') return { code: 'agent_online', label: '在线' }
      if (heartbeat === 'offline' || heartbeat === 'suspect') return { code: 'agent_error', label: heartbeat === 'suspect' ? '疑似离线' : '离线' }
      if (install === 'installing') return { code: 'agent_installing', label: '安装中' }
      if (agent?.LastError || agent?.last_error || install === 'error') return { code: 'agent_error', label: '异常' }
      if (install === 'online') return { code: 'pending', label: '等待心跳' }
      return { code: 'pending', label: '未安装' }
    }
    function agentMetric(agent, name) {
      return (agent?.Metrics || agent?.metrics || []).find(item => String(item?.Name || item?.name || '').toLowerCase() === name)
    }
    function agentResource(agent) {
      const cpuMetric = agentMetric(agent, 'agent_cpu_usage_percent')
      const memoryMetric = agentMetric(agent, 'agent_memory_rss_mb')
      const validNumber = metric => {
        if (!metric || !(metric.Success ?? metric.success)) return null
        const value = Number(metric.Value ?? metric.value)
        return Number.isFinite(value) ? value : null
      }
      const cpu = validNumber(cpuMetric)
      const memory = validNumber(memoryMetric)
      const collectedAt = [cpuMetric?.CollectedAt || cpuMetric?.collected_at, memoryMetric?.CollectedAt || memoryMetric?.collected_at].filter(Boolean).sort().at(-1) || ''
      const metricError = cpuMetric?.Error || cpuMetric?.error || memoryMetric?.Error || memoryMetric?.error || ''
      return { cpu, memory, collectedAt, available: cpu !== null || memory !== null, needsUpgrade: /collector not found/i.test(metricError), error: metricError }
    }
    function agentCPU(agent) {
      const value = agentResource(agent).cpu
      return value === null ? '—' : `${value.toFixed(value < 10 ? 2 : 1)}%`
    }
    function agentMemory(agent) {
      const value = agentResource(agent).memory
      return value === null ? '—' : value >= 1024 ? `${(value / 1024).toFixed(2)} GB` : `${value.toFixed(value < 10 ? 2 : 1)} MB`
    }
    function agentResourceAverage(name) {
      const values = data.value.agents.map(item => agentResource(item)[name]).filter(value => value !== null)
      return values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : null
    }
    function agentResourceTotal(name) {
      const values = data.value.agents.map(item => agentResource(item)[name]).filter(value => value !== null)
      return values.length ? values.reduce((sum, value) => sum + value, 0) : null
    }
    function agentResourceCoverage() {
      return data.value.agents.filter(item => agentResource(item).available).length
    }
    function staticHost(info) { return info?.Host || info?.host || {} }
    function staticMySQL(info) { return info?.MySQL || info?.mysql || {} }
    function staticRows(info) {
      const host = staticHost(info); const mysql = staticMySQL(info)
      return [
        ['操作系统', host.OS || host.os || '—'], ['CPU 核数', host.CPUCores ?? host.cpu_cores ?? '—'],
        ['内存', host.MemoryGB ? `${host.MemoryGB} GB` : (host.memory_gb ? `${host.memory_gb} GB` : '—')], ['glibc', host.GlibcVersion || host.glibc_version || '—'],
        ['SSH', (host.SSHAvailable ?? host.ssh_available) ? `${host.SSHUser || host.ssh_user || '—'}:${host.SSHPort || host.ssh_port || 22}` : '不可用'],
        ['网络地址', (host.IPs || host.ips || []).join('、') || '—'], ['SELinux', host.SELinux || host.selinux || '—'], ['防火墙', host.Firewall || host.firewall || '—'],
        ['NTP', (host.NTPEnabled ?? host.ntp_enabled) ? '已启用' : '未启用'], ['Swap', (host.SwapEnabled ?? host.swap_enabled) ? '已启用' : '未启用'],
        ['MySQL', (mysql.Installed ?? mysql.installed) ? `${mysql.Version || mysql.version || '已安装'} · ${mysql.Port || mysql.port || '—'}` : '未安装'],
        ['采集时间', date(info?.CollectedAt || info?.collected_at || info?.UpdatedAt || info?.updated_at)]
      ]
    }
    function dynamicMetrics(info) { return info?.Metrics || info?.metrics || [] }
    function metricValue(metric) {
      const value = metric?.Value ?? metric?.value
      if (value === undefined || value === null) return '—'
      if (typeof value === 'object') return JSON.stringify(value)
      return String(value)
    }
    function resolveFailedDetail(step, message) {
      const details = step.details || []
      const text = String(message || '').toLowerCase()
      let index = details.findIndex(item => item.state === 'running')
      if (step.title === '部署 GMHA Agent') {
        if (text.includes('heartbeat') || text.includes('deadline exceeded')) index = 4
        else if (text.includes('upload agent binary')) index = 1
        else if (text.includes('agent.yaml') || text.includes('systemd unit')) index = 2
        else if (text.includes('systemctl') || text.includes('start gmha-agent')) index = 3
        else if (text.includes('install dir') || text.includes('permission')) index = 0
        if (index >= 0) details.forEach((item, itemIndex) => { if (itemIndex < index && item.state !== 'error') item.state = 'success' })
      }
      return details[index] || details.find(item => item.state === 'running')
    }
    function flowErrorKey(step, detail) { return `${step.title}:${detail.title}` }
    function isFlowErrorExpanded(step, detail) { return !!expandedFlowErrors.value[flowErrorKey(step, detail)] }
    function toggleFlowError(step, detail) { const key = flowErrorKey(step, detail); expandedFlowErrors.value = { ...expandedFlowErrors.value, [key]: !expandedFlowErrors.value[key] } }
    function flowReport() {
      const failed = onboardingFlow.value.find(item => item.state === 'error')
      const detail = failed?.details?.find(item => item.state === 'error')
      return `GMHA 纳管问题报告\n时间：${new Date().toLocaleString('zh-CN')}\n目标：${onboardingResult.value?.name || form.value.name} (${onboardingResult.value?.ip || form.value.ip})\n失败阶段：${failed?.title || '未知'}${detail ? ` / ${detail.title}` : ''}\n错误详情：${detail?.error || failed?.detail || error.value || '未知'}`
    }
    async function loadMySQLPackages() {
      const result = await api('/mysql/packages')
      data.value.mysqlPackages = asList(result, 'packages')
      const selectedVersion = mysqlInstallForm.value.version
      const selectedPackageName = mysqlInstallForm.value.package_name
      if (selectedVersion && !mysqlInstallVersions.value.some(pkg => (pkg.version || pkg.Version) === selectedVersion)) mysqlInstallForm.value.version = ''
      if (selectedPackageName && !data.value.mysqlPackages.some(pkg => (pkg.file_name || pkg.FileName) === selectedPackageName)) mysqlInstallForm.value.package_name = ''
      return data.value.mysqlPackages
    }
    async function loadPackages() {
      try {
        const suffix = packageKeyword.value ? `?keyword=${encodeURIComponent(packageKeyword.value)}` : ''
        const [result] = await Promise.all([api(`/packages${suffix}`), loadMySQLPackages()])
        packageItems.value = result.items || []; packageSettings.value = result.settings || {}
        const bundles = packageSettings.value.bundles || []
        if (!bundles.some(item => item.id === packageBundleID.value)) packageBundleID.value = bundles.find(item => item.default)?.id || bundles[0]?.id || ''
      } catch (err) { error.value = err.message }
    }
    function choosePackageFile(event) { packageForm.value.file = event.target.files?.[0] || null }
    async function uploadPackage() {
      if (!packageForm.value.file) { error.value = '请选择要上传的安装包。'; return }
      const form = new FormData(); form.set('category', packageForm.value.category); form.set('arch', packageForm.value.arch); form.set('version', packageForm.value.version); form.set('description', packageForm.value.description); form.set('file', packageForm.value.file)
      try {
        const response = await fetch('/api/v1/packages', { method: 'POST', body: form }); const result = await response.json().catch(() => ({}))
        if (!response.ok) throw new Error(result.error || '上传失败')
        notice.value = `安装包已上传：${result.name}`; packageForm.value.file = null; packageForm.value.version = ''; packageForm.value.description = ''; const input = document.getElementById('package-upload-file'); if (input) input.value = ''; await loadPackages(); await loadUpgrades(true)
      } catch (err) { error.value = err.message }
    }
    async function deletePackage(item) {
      if (!confirm(`确认删除安装包 ${item.name}？`)) return
      try { await api(`/packages/${encodeURIComponent(item.category)}/${encodeURIComponent(item.name)}`, { method: 'DELETE' }); notice.value = `已删除 ${item.name}`; await loadPackages() } catch (err) { error.value = err.message }
    }
    function packageCatalogInstalled(item) { return packageItems.value.some(pkg => pkg.category === item.category && pkg.name === item.name) }
    function packageCatalogByID(id) { return (packageSettings.value.catalog || []).find(item => item.id === id) }
    function packageBundleCatalogItems(optional = false) {
      const bundle = selectedPackageBundle.value
      if (!bundle) return []
      const ids = optional ? (bundle.optional_catalog_ids || []) : [bundle.mysql_catalog_id, ...(bundle.recommended_catalog_ids || [])]
      return ids.map(packageCatalogByID).filter(Boolean)
    }
    function packageBundleInstalledCount() { return packageBundleCatalogItems().filter(packageCatalogInstalled).length }
    function packageBundleAllInstalled() { const items = packageBundleCatalogItems(); return items.length > 0 && items.every(packageCatalogInstalled) }
    async function fetchPackageBundle() {
      if (!selectedPackageBundle.value || packageBundleFetching.value || packageBundleAllInstalled()) return
      packageBundleFetching.value = true; error.value = ''; notice.value = '正在从官网准备 MySQL 与推荐工具，较大的安装包可能需要几分钟…'
      try {
        const result = await api('/packages/fetch-bundle', { method: 'POST', body: JSON.stringify({ bundle_id: selectedPackageBundle.value.id }) })
        await loadPackages()
        const downloaded = (result.results || []).filter(item => item.status === 'downloaded').length
        const failed = (result.results || []).filter(item => item.status === 'failed')
        if (failed.length) error.value = `已下载 ${downloaded} 个包，${failed.length} 个失败：${failed.map(item => item.error).join('；')}`
        else notice.value = downloaded ? `推荐组合准备完成，新下载 ${downloaded} 个安装包。` : '推荐组合已经全部入库。'
      } catch (err) { error.value = err.message }
      finally { packageBundleFetching.value = false }
    }
    async function fetchCatalogPackage(item) {
      if (packageCatalogInstalled(item) || packageFetching.value[item.id]) return
      packageFetching.value = { ...packageFetching.value, [item.id]: true }
      error.value = ''
      try {
        const result = await api('/packages/fetch', { method: 'POST', body: JSON.stringify({ catalog_id: item.id }) })
        notice.value = `官方软件包已入库：${result.name}`
        await loadPackages()
      } catch (err) { error.value = err.message }
      finally { packageFetching.value = { ...packageFetching.value, [item.id]: false } }
    }
    async function verifyPackage(item) {
      const key = `${item.category}/${item.name}`
      packageFetching.value = { ...packageFetching.value, [key]: true }
      try {
        await api('/packages/verify', { method: 'POST', body: JSON.stringify({ category: item.category, name: item.name }) })
        notice.value = `SHA-256 校验完成：${item.name}`
        await loadPackages()
      } catch (err) { error.value = err.message }
      finally { packageFetching.value = { ...packageFetching.value, [key]: false } }
    }
    async function savePackageStorage() {
      const path = String(packageSettings.value.storage_path || '').trim()
      if (!path) { error.value = '安装包存放目录不能为空。'; return }
      try { packageSettings.value = await api('/package-settings', { method: 'PUT', body: JSON.stringify({ storage_path: path }) }); notice.value = '安装包存放位置已保存。'; await loadPackages() } catch (err) { error.value = err.message }
    }
    function packageDownloadURL(item) { return `/api/v1/packages/${encodeURIComponent(item.category)}/${encodeURIComponent(item.name)}` }
    function packageCategoryLabel(category) {
      return ({ 'gmha-manager': 'GMHA Manager', 'gmha-agent': 'GMHA Agent', mysql: 'MySQL', 'percona-toolkit': 'Percona Toolkit (PT)', 'mysql-router': 'MySQL Router', xtrabackup: 'XtraBackup', binlog2sql: 'binlog2sql', mycat: 'Mycat', proxysql: 'ProxySQL', sysbench: 'Sysbench', other: '第三方软件' })[category] || category
    }
    function packageChecksum(value) { return value ? `${value.slice(0, 12)}…${value.slice(-8)}` : '计算中' }
    function packageSize(value) { const size = Number(value || 0); return size < 1024 ? `${size} B` : size < 1048576 ? `${(size / 1024).toFixed(1)} KB` : `${(size / 1048576).toFixed(1)} MB` }
    function state(value) { return String(value || 'unknown').toLowerCase() }
    function label(value) { return value || '未知' }
    function date(value) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '—' }
    const machineBulkBindings = { machineKeyword, machineClusterFilter, selectedMachineIDs, showBatchOnboard, batchOnboardRows, batchOnboardShared, batchOnboardRunning, batchOnboardResults, showBulkDelete, bulkDeleteForm, bulkDeleteRunning, bulkDeleteResults, searchMachines, toggleCurrentMachinePage, changeMachinePage, openBatchOnboard, addBatchOnboardRow, removeBatchOnboardRow, batchOnboardCredentialChanged, submitBatchOnboard, bulkDeleteExpected, bulkDeleteClusterMembers, bulkDeleteClusterSummary, leaveBulkDeleteForClusters, openBulkDelete, submitBulkDelete }
    const topologyOverviewBindings = { clusterTopologyRefreshing, clusterTopologyLastUpdated, clusterTopologyAutoRefresh, clusterOverviewRange, startClusterTopologyAutoRefresh, stopClusterTopologyAutoRefresh, toggleClusterTopologyAutoRefresh, overviewTopologyRoots, overviewTopologyReplicas, overviewTopologyStandalone, overviewTopologyEdgeForReplica, overviewTopologySourceName, topologyMetric, clusterOverview, overviewNumber, overviewBytes, overviewChartPoints, overviewChartArea, overviewRangeLabel, changeClusterOverviewRange }
    const architectureBindings = { removeSelectedMachinesFromCluster, clusterMachinePageSelected, toggleClusterMachinePageSelection, testManagerDatabase, refreshManagerStatus, mysqlInstallArchitectures, mysqlInstallVersions, mysqlInstallMachineChanged, mysqlInstallArchitectureChanged, mysqlInstallTargetInfo, mysqlInstallCompatibility, architectureLinkSource, architectureSelectedNode, architectureRoleChangeDialog, architectureRoleChangeFeedback, architectureDraftHistory, undoArchitectureDraft, resetArchitectureDraft, architectureDraggingNode, architectureNodeMeta, architectureAvailableInstances, addArchitectureInstance, setArchitectureNodeRole, architectureRoleLabel, architectureTypeLabel, applyArchitecturePreset, requestArchitectureRoleChange, confirmArchitectureRoleChange, architectureRoleChangePromotedMachineID, architectureRoleChangeDemotedMachineID, architectureDraftEdges, startArchitectureNodeDrag, finishArchitectureNodeDrag, dropArchitectureNode, dropArchitectureLayer, startArchitectureLink, completeArchitectureLink, kickArchitectureNode, architectureRunStepResult, architectureRunStepStatus, architectureRunProgress, openArchitectureRunTask, showMachineDelete, machineDeleteForm, machineDeleteSubmitting, machineDeleteError, machineDeletePrechecking, machineDeletePrecheck, machineDeleteRegisteredPorts, machineDeleteRemoteMySQLDetected, machineDeleteMySQLResidues, machineDeleteResidueLabel, machineDeleteExpected, machineDeleteClusterName, leaveMachineDeleteForCluster, machineDeleteSteps, openMachineDelete, ...topologyOverviewBindings, ...machineBulkBindings }
    architectureBindings.taskSpec = taskSpec
	architectureBindings.machineDeleteSSHBlocked = machineDeleteSSHBlocked
    architectureBindings.relatedTaskIDs = relatedTaskIDs
    architectureBindings.taskChildren = taskChildren
    architectureBindings.taskChildDetails = taskChildDetails
    architectureBindings.taskFlowDetails = taskFlowDetails
    architectureBindings.taskFlowStepCount = taskFlowStepCount
    Object.assign(architectureBindings, { architecturePlanDialog, architectureAdjustmentTitle, architectureAdjustmentDetail, architectureOperationVIPOnly, architectureVIPOperationAction, architectureTopologyHasChanges, architectureVIPActionLabel, vipEditingAddress, vipEditingConfig, vipEditorState, vipEditorIsNew, selectVIPForEdit, beginNewVIP, refreshVIPEditorInterfaces, architectureVIPTargetChanged, vipDraggingAddress, vipMagnetTargetID, vipSnapTargetID, vipDriftDialog, vipCardTargetMachineID, selectArchitectureVIPCard, startArchitectureVIPDrag, finishArchitectureVIPDrag, setVIPMagnetTarget, clearVIPMagnetTarget, dropArchitectureCanvasItem, cancelVIPDrift, confirmVIPDrift })
    architectureBindings.taskFlowSuccessCount = taskFlowSuccessCount
    architectureBindings.selectedTaskFlowDetail = selectedTaskFlowDetail
    architectureBindings.selectedTaskFlowView = selectedTaskFlowView
    architectureBindings.taskFlowTabLabel = taskFlowTabLabel
    architectureBindings.selectTaskFlow = selectTaskFlow
    Object.assign(architectureBindings, { taskTypeFilter, taskPage, taskPageSize, taskTotal, taskDetailStack, selectedTaskIDs, changeTaskPage, changeTaskPageSize, canDeleteTask, deleteTaskRecord, toggleTaskSelection, selectCurrentTaskPage, deleteTaskRecords, openTaskChild, packageBundleID, packageBundleFetching, selectedPackageBundle, packageBundleCatalogItems, packageBundleInstalledCount, packageBundleAllInstalled, fetchPackageBundle, credentialSubmitting, upgradeOverview, upgradeJobs, upgradeForm, upgradeSubmitting, upgradeComponent, upgradeAgents, upgradeAgentTotal, upgradeAgentPage, upgradeAgentPageSize, upgradeAgentKeyword, upgradeAgentStatus, upgradeAgentVersion, upgradeAgentLoading, agentVersionDetecting, agentVersionBatchDetecting, detectAgentVersion, detectUnknownAgentVersions, loadUpgrades, loadUpgradeAgents, searchUpgradeAgents, changeUpgradeAgentPage, startManagerUpgrade, startAgentUpgrade, selectedManagerUpgradePackage, selectedAgentUpgradePackage, upgradeAgentRelation, upgradeAgentSelectable, upgradeCurrentPageSelected, toggleUpgradeCurrentPage, agentManagementUpgradePageSelected, toggleAgentManagementUpgradePage, upgradeAgentPackageChanged, upgradeStatusLabel })
    let taskFilterTimer = null
    watch([taskFilter, taskTypeFilter, taskKeyword], () => {
      taskPage.value = 1
      clearTimeout(taskFilterTimer)
      taskFilterTimer = setTimeout(loadTaskPage, 250)
    })
    watch([active, agentResourceRefreshSeconds], () => {
      startAgentResourceRefresh()
      if (active.value === 'agents') refreshAgentResources(true)
      clearInterval(upgradeTimer)
      upgradeTimer = null
      if (active.value === 'manager' || active.value === 'agents') { loadUpgrades(); upgradeTimer = setInterval(() => loadUpgrades(true), 2000) }
    })
    onMounted(() => { refresh(); loadPackages(); loadUpgrades(true); managerStatusTimer = setInterval(refreshManagerStatus, 3000); startAgentResourceRefresh() })
    onUnmounted(() => { stopTaskPolling(); stopAgentResourceRefresh(); clearTimeout(taskFilterTimer); clearTimeout(architecturePollTimer); clearInterval(managerStatusTimer); clearInterval(agentActionElapsedTimer); clearInterval(upgradeTimer); stopClusterTopologyAutoRefresh() })
    return { active, current, navGroups, expandedNav, toggleNavGroup, chooseNavigation, data, managerForm, metrics, recentTasks, filteredTasks, taskFilter, taskKeyword, selectedTaskDetail, selectedTaskStep, selectedTaskEvents, taskObject, taskSteps, taskEvents, taskTitle, taskTypeLabel, taskStatusLabel, stepStatusLabel, elapsed, safeLog, chooseTaskStep, selectCurrentTaskStep, openTaskDetail, refreshSelectedTaskDetail, closeTaskDetail, loading, error, notice, showOnboard, showOnboardFlow, showMachineDetail, showCredential, showAssign, showQuickClusterAssign, showAgentDetail, agentActionDialog, agentActionInput, agentActionSubmitting, agentActionError, agentActionElapsed, closeAgentAction, submitAgentAction, showMySQLInstall, showMySQLTask, mysqlView, packageItems, packageSettings, packageForm, packageKeyword, packageFetching, packageCatalogInstalled, fetchCatalogPackage, verifyPackage, packageChecksum, showClusterEditor, showClusterCleanup, showClusterMembers, clusterCandidatesLoading, clusterCandidatesError, mysqlTaskDetail, clusterCleanupResult, selectedClusterForMembers, clusterCandidates, clusterCandidatePage, clusterCandidateTotal, selectedClusterMachineIDs, clusterMemberAssignResult, clusterPage, clusterTotal, clusterKeyword, clusterPageItems, selectedClusterDetail, clusterTopology, clusterTopologyError, clusterMachineItems, clusterMachinePage, clusterMachineTotal, selectedClusterOperationMachineIDs, clusterMySQLDialog, clusterMySQLForm, clusterMySQLConfirm, automationSelectedClusters, automationForm, automationConfirm, automationRunning, automationResults, toggleAllAutomationClusters, submitAutomationTask, mysqlPrivilegeOptions, backupPolicies, backupRuns, showBackupPolicyEditor, backupPolicyForm, architectureForm, architectureCurrent, architectureHasChanges, architecturePlan, architectureRun, vipConfigs, vipStates, vipBusy, vipForm, vipTargetMachine, vipInterfaceOptions, vipStateFor, vipMachineName, vipStatusLabel, openVIPManagement, architectureSubmitting, applyArchitectureRoles, architectureNodeChanged, architectureNodeHasChanges, architectureNodeName, topologyEdgeForNode, openArchitectureAdjustment, previewArchitectureAdjustment, submitArchitectureAdjustment, confirmArchitectureForce, loadVIPConfigs, saveVIPConfig, deleteVIPConfig, openClusterBackup, loadClusterBackups, openBackupPolicyEditor, saveBackupPolicy, deleteBackupPolicy, runBackupPolicy, restoreBackup, backupScheduleLabel, backupMachines, backupInstancesForMachine, backupMachineChanged, backupMachineRole, weekdayName, toggleAllBackupWeekdays, backupTypeLabel, form, credentialForm, mysqlInstallForm, isCustomMySQLAccount, addCustomMySQLAccount, removeCustomMySQLAccount, clusterForm, selectedCredential, assignedMachineIDs, onboardingFlow, onboardingResult, onboardingDetected, canSkipPrecheck, machinePage, credentialPage, machineTotal, credentialTotal, pageSize, selectedMachine, selectedAgent, selectedMachineErrorExpanded, selectedMachineCluster, machineStaticInfo, machineDynamicInfo, machineInfoError, refresh, refreshAgentResources, agentResourceRefreshing, agentResourceUpdatedAt, agentResourceRefreshSeconds, onboard, cleanupTarget, recover, showAgent, retryAgent, upgradeAgent, uninstallAgent, repairMySQLAgentConfig, saveManagerConfig, managerAction, showMachine, showMySQLMachine, saveMachine, deleteMachine, assignMachineCluster, openQuickClusterAssign, quickAssignMachineCluster, collectMachineStaticInfo, loadMachineDynamicInfo, changePage, createCredential, createMySQLInstall, openMySQLInstall, saveMySQLAccountPresets, refreshMySQLTask, uninstallMySQL, forgetMySQL, openCreateCluster, openEditCluster, openClusterDetail, closeClusterDetail, refreshClusterTopology, installClusterMySQL, uninstallClusterMySQL, mysqlInstancesOnMachine, clusterMachineInterfaces, mysqlTopologyNode, mysqlRoleLabel, openClusterMySQLBatch, submitClusterMySQLBatch, removeMachineFromCluster, changeClusterMachinePage, showClusterCapability, saveCluster, deleteCluster, cleanupCluster, clusterMachines, clusterMachineCount, clusterAgentCount, loadClusterPage, searchClusterPage, changeClusterPage, openClusterMembers, changeClusterCandidatePage, assignClusterMembers, deleteCredential, assignCredential, chooseCredential, applyCredential, loadKeyFile, loadPackages, choosePackageFile, uploadPackage, deletePackage, savePackageStorage, packageDownloadURL, packageCategoryLabel, packageSize, flowReport, toggleFlowError, isFlowErrorExpanded, machineLastError, machineStatus, machineCluster, machineAgentInstallDir, agentStatus, agentResource, agentCPU, agentMemory, agentResourceAverage, agentResourceTotal, agentResourceCoverage, staticRows, dynamicMetrics, metricValue, errorSummary, ...architectureBindings, state, label, date }
  },
  template: `
    <main :class="['shell', { 'cluster-focus-mode': !!selectedClusterDetail }]">
      <header class="global-header">
        <div class="global-brand"><div class="brand-mark"><img src="/gmha-mark.svg" alt="GMHA" /></div><div><strong>GMHA 管理平台</strong><span>MySQL 高可用管理工具</span></div></div>
        <div class="global-actions">
          <button type="button" @click="chooseNavigation('tasks')"><span>☷</span>任务中心</button>
        </div>
      </header>
      <button v-if="selectedClusterDetail" type="button" class="cluster-sidebar-edge-trigger" aria-label="展开一级菜单"></button>
      <aside class="sidebar">
        <nav v-for="group in navGroups" :key="group.title" class="nav-group" :class="{ open: expandedNav[group.title], 'cluster-nav-group': group.title === '集群运维' }">
          <button type="button" class="nav-parent" :class="{ active: group.items.some(item => item.id === active) }" @click="toggleNavGroup(group.title)">
            <i>{{ group.icon }}</i><span>{{ group.title }}</span><b>⌄</b>
          </button>
          <div v-show="expandedNav[group.title]" class="nav-children">
            <button v-for="item in group.items" :key="item.id" class="nav-item" :class="{ active: active === item.id }" @click="chooseNavigation(item.id)">
              <i>{{ item.icon }}</i><span>{{ item.label }}</span><em v-if="item.soon">规划中</em>
            </button>
          </div>
        </nav>
        <div class="sidebar-footer"><span class="pulse"></span> Manager 服务已连接</div>
      </aside>
      <section class="content">
        <header class="topbar"><div><h1>{{ current.label }}</h1><span class="title-refresh" :class="{ spinning: loading }" @click="refresh" title="刷新当前数据">↻</span></div>
          <div class="top-actions"><span class="updated" v-if="loading">正在刷新…</span><button v-if="active === 'machines'" class="secondary top-secondary" @click="showCredential = true">＋ 添加凭证</button><button v-if="active === 'machines'" class="primary" @click="showOnboard = true">＋ 纳管机器</button><button v-if="active === 'mysql' && mysqlView !== 'install'" class="primary" @click="openMySQLInstall">＋ 安装 MySQL</button></div>
        </header>
        <div v-if="error" class="alert error"><b>操作未完成</b><span>{{ errorSummary(error) }}</span><small>完整执行日志请在任务中心对应任务详情中查看。</small><button @click="error = ''">×</button></div>
        <div v-if="notice" class="alert success"><b>操作已提交</b><span>{{ errorSummary(notice, 240) }}</span><button @click="notice = ''">×</button></div>
        <template v-if="active === 'alerts'"><AlertManagement :clusters="data.clusters" /></template>
        <template v-else-if="active === 'upgrades'">
          <section class="upgrade-hero">
            <div><span class="upgrade-eyebrow">RELEASE MANAGEMENT</span><h2>安全、可追踪的版本升级</h2><p>统一管理 Manager 与 Agent 版本，升级前自动校验，失败时按任务记录执行回滚。</p></div>
            <div class="upgrade-hero-actions"><button type="button" class="secondary" @click="active='packages'">管理安装包</button><button type="button" class="upgrade-refresh" :disabled="loading" @click="loadUpgrades"><span>↻</span>刷新状态</button></div>
          </section>
          <section class="upgrade-summary">
            <article><i class="upgrade-summary-icon manager">M</i><div><small>当前 Manager 版本</small><strong>{{ upgradeOverview.manager_version || data.manager.version || 'V0.0.1' }}</strong><span :class="['upgrade-health',data.manager.running?'online':'offline']"><i></i>{{ data.manager.running ? '服务运行正常' : '服务当前不可用' }}</span></div></article>
            <article><i class="upgrade-summary-icon agent">A</i><div><small>已纳管 Agent</small><strong>{{ upgradeOverview.agent_total || 0 }} <em>台</em></strong><span>{{ (upgradeOverview.agent_versions || []).map(item=>item.version+' · '+item.count+'台').join(' / ') || '暂无版本数据' }}</span></div></article>
            <article><i class="upgrade-summary-icon package">▣</i><div><small>可用升级制品</small><strong>{{ upgradeOverview.manager_packages.length + upgradeOverview.agent_packages.length }} <em>个</em></strong><span>Manager {{ upgradeOverview.manager_packages.length }} · Agent {{ upgradeOverview.agent_packages.length }}</span></div></article>
          </section>
          <section class="panel upgrade-console">
            <aside class="upgrade-component-nav"><header><span>升级对象</span><small>选择组件后配置目标版本</small></header><button type="button" :class="{active:upgradeComponent==='manager'}" @click="upgradeComponent='manager'"><i>M</i><span><b>Manager</b><small>当前 {{ upgradeOverview.manager_version || '未知' }}</small></span><em>{{ upgradeOverview.manager_packages.length }} 个制品</em></button><button type="button" :class="{active:upgradeComponent==='agent'}" @click="upgradeComponent='agent'"><i>A</i><span><b>Agent</b><small>{{ upgradeOverview.agent_total || 0 }} 台 · {{ (upgradeOverview.agent_versions || []).length }} 个版本</small></span><em>{{ upgradeOverview.agent_packages.length }} 个制品</em></button><footer><span>版本策略</span><b>仅允许向更高版本升级</b><small>相同版本与降级请求将在服务端拦截。</small></footer></aside>
            <div class="upgrade-component-content">
              <template v-if="upgradeComponent==='manager'">
                <header class="upgrade-content-head"><div><span>MANAGER UPGRADE</span><h3>选择 Manager 目标制品</h3><p>当前运行程序只替换二进制文件，配置、数据库和安装包仓库不受影响。</p></div><span :class="['upgrade-card-state',data.manager.running?'ready':'blocked']">{{ data.manager.running ? '服务正常' : '服务异常' }}</span></header>
                <div v-if="upgradeOverview.manager_packages.length" class="upgrade-package-list"><label v-for="pkg in upgradeOverview.manager_packages" :key="pkg.name" :class="['upgrade-package-option',{selected:upgradeForm.manager_package===pkg.name,blocked:pkg.relation!=='upgrade'}]"><input v-model="upgradeForm.manager_package" type="radio" :value="pkg.name"><span><b>{{ pkg.version || '版本未知' }}</b><small>{{ pkg.name }}</small><em>{{ pkg.arch }} · {{ packageSize(pkg.size) }} · SHA {{ packageChecksum(pkg.sha256) }}</em></span><strong :class="pkg.relation">{{ pkg.relation==='upgrade' ? '可升级' : pkg.relation==='current' ? '当前版本' : pkg.relation==='downgrade' ? '低于当前版本' : '无法比较' }}</strong></label></div>
                <div v-else class="upgrade-package-empty"><i>▣</i><div><b>尚未上传 Manager 升级制品</b><p>请上传形如 <code>gmha-manager-V0.0.2-linux-amd64.bin</code> 的 Linux 可执行文件。上传后系统会识别版本与架构并建立 SHA-256 索引。</p><small>存放目录：{{ upgradeOverview.storage?.manager_package_dir || 'software/gmha-manager' }}</small></div><button type="button" class="primary" @click="active='packages'">上传 Manager 制品</button></div>
                <section class="upgrade-version-decision"><div><small>当前运行版本</small><b>{{ upgradeOverview.manager_version || '未知' }}</b></div><i>→</i><div><small>目标制品版本</small><b>{{ selectedManagerUpgradePackage()?.version || '尚未选择' }}</b></div><span :class="selectedManagerUpgradePackage()?.relation || 'unselected'">{{ !selectedManagerUpgradePackage() ? '等待选择制品' : selectedManagerUpgradePackage().relation==='upgrade' ? '版本检查通过' : selectedManagerUpgradePackage().relation==='current' ? '无需重复升级' : selectedManagerUpgradePackage().relation==='downgrade' ? '禁止降级' : '版本格式无法识别' }}</span></section>
                <div class="upgrade-steps"><span><i>1</i>运行环境预检</span><b>→</b><span><i>2</i>候选程序验版</span><b>→</b><span><i>3</i>备份当前程序</span><b>→</b><span><i>4</i>原子替换</span><b>→</b><span><i>5</i>重启健康检查</span></div>
                <footer class="upgrade-submit-bar"><div><b>升级会短暂中断 Manager 服务</b><small>失败时自动使用同目录备份文件恢复。</small></div><button type="button" class="primary" :disabled="upgradeSubmitting || selectedManagerUpgradePackage()?.relation!=='upgrade' || !data.manager.running" @click="startManagerUpgrade">{{ upgradeSubmitting ? '正在提交…' : '确认升级 Manager' }}</button></footer>
              </template>
              <template v-else>
                <header class="upgrade-content-head"><div><span>AGENT FLEET UPGRADE</span><h3>Agent 版本与升级目标</h3><p>目标列表采用服务端检索和分页，每页最多加载 {{ upgradeAgentPageSize }} 台，选择结果可跨页保留。</p></div><span class="upgrade-card-state ready">已选择 {{ upgradeForm.targets.length }} 台</span></header>
                <div v-if="upgradeOverview.agent_packages.length" class="upgrade-agent-package-bar"><label><span>目标升级制品</span><select v-model="upgradeForm.agent_package" @change="upgradeAgentPackageChanged"><option value="">请选择 gmha-agent 制品</option><option v-for="pkg in upgradeOverview.agent_packages" :key="pkg.name" :value="pkg.name">{{ pkg.version }} · {{ pkg.arch }} · {{ pkg.name }}</option></select></label><div v-if="selectedAgentUpgradePackage()"><span><b>{{ selectedAgentUpgradePackage().upgradeable_count }}</b><small>低于目标版本</small></span><span><b>{{ selectedAgentUpgradePackage().current_count }}</b><small>已是当前版本</small></span><span class="danger"><b>{{ selectedAgentUpgradePackage().downgrade_count }}</b><small>高于目标版本</small></span><span><b>{{ selectedAgentUpgradePackage().unknown_count }}</b><small>版本未知</small></span></div></div>
                <div v-else class="upgrade-package-empty"><i>▣</i><div><b>尚未上传 Agent 升级制品</b><p>上传形如 <code>gmha-agent-V0.0.2-linux-amd64.bin</code> 的 Linux ELF 文件后，才可进行目标筛选和版本比较。</p><small>存放目录：{{ upgradeOverview.storage?.agent_package_dir || 'software/gmha-agent' }}</small></div><button type="button" class="primary" @click="active='packages'">上传 Agent 制品</button></div>
                <form class="upgrade-agent-filters" @submit.prevent="searchUpgradeAgents"><input v-model.trim="upgradeAgentKeyword" placeholder="搜索机器名称、IP 或集群"><select v-model="upgradeAgentStatus"><option value="online">仅在线</option><option value="all">全部状态</option><option value="error">异常 / 离线</option></select><select v-model="upgradeAgentVersion"><option value="all">全部当前版本</option><option v-for="item in (upgradeOverview.agent_versions || [])" :key="item.version" :value="item.version">{{ item.version }}（{{ item.count }} 台）</option></select><button class="secondary">查询</button></form>
                <div class="upgrade-agent-table-wrap"><table class="upgrade-agent-table"><thead><tr><th><input type="checkbox" :checked="upgradeCurrentPageSelected()" :disabled="!upgradeForm.agent_package" aria-label="选择当前页可升级 Agent" @change="toggleUpgradeCurrentPage"></th><th>机器 / IP</th><th>所属集群</th><th>当前版本</th><th>目标版本</th><th>心跳状态</th><th>版本判断</th></tr></thead><tbody><tr v-for="agent in upgradeAgents" :key="agent.ip" :class="{selected:upgradeForm.targets.includes(agent.ip),blocked:!upgradeAgentSelectable(agent)}"><td><input v-model="upgradeForm.targets" type="checkbox" :value="agent.ip" :disabled="!upgradeAgentSelectable(agent)" :aria-label="'选择 '+agent.name"></td><td><b>{{ agent.name || '未命名 Agent' }}</b><small>{{ agent.ip }}</small></td><td>{{ agent.cluster || '未分配集群' }}</td><td><b>{{ agent.version || '未知' }}</b></td><td>{{ selectedAgentUpgradePackage()?.version || '—' }}</td><td><span :class="['upgrade-agent-state',state(agent.heartbeat_state || agent.install_state)]"><i></i>{{ agent.heartbeat_state || agent.install_state || '未知' }}</span></td><td><span :class="['upgrade-relation',upgradeAgentRelation(agent)]">{{ upgradeAgentRelation(agent)==='upgrade' ? '可升级' : upgradeAgentRelation(agent)==='current' ? '已是目标版本' : upgradeAgentRelation(agent)==='downgrade' ? '禁止降级' : upgradeAgentRelation(agent)==='unknown' ? '版本未知' : '先选制品' }}</span></td></tr><tr v-if="upgradeAgentLoading"><td colspan="7" class="empty">正在加载 Agent…</td></tr><tr v-else-if="!upgradeAgents.length"><td colspan="7" class="empty">没有符合当前条件的 Agent。</td></tr></tbody></table></div>
                <div class="upgrade-agent-pager"><span>第 {{ upgradeAgentPage }} 页 · 共 {{ upgradeAgentTotal }} 台 · 已跨页选择 <b>{{ upgradeForm.targets.length }}</b> 台</span><div><button type="button" class="secondary" :disabled="upgradeAgentPage===1 || upgradeAgentLoading" @click="changeUpgradeAgentPage(-1)">上一页</button><button type="button" class="secondary" :disabled="upgradeAgentPage*upgradeAgentPageSize>=upgradeAgentTotal || upgradeAgentLoading" @click="changeUpgradeAgentPage(1)">下一页</button></div></div>
                <footer class="upgrade-submit-bar"><div><b>仅版本低于目标且在线的 Agent 可被选择</b><small>相同版本、版本未知和降级目标均不可提交。</small></div><button type="button" class="primary" :disabled="upgradeSubmitting || !upgradeForm.agent_package || !upgradeForm.targets.length" @click="startAgentUpgrade">{{ upgradeSubmitting ? '正在提交…' : '升级选中的 '+upgradeForm.targets.length+' 台 Agent' }}</button></footer>
              </template>
            </div>
          </section>
          <details class="panel upgrade-storage"><summary><span><i>⌁</i><b>制品仓库与运行文件目录</b><small>查看实际磁盘位置、索引与升级备份规则</small></span><em>展开目录结构</em></summary><div class="upgrade-storage-tree"><section><header><b>Manager 本机</b><span>当前运行与任务状态</span></header><p><i>运行文件</i><code>{{ upgradeOverview.storage?.manager_executable || '当前 gmha 可执行文件' }}</code></p><p><i>升级备份</i><code>{{ upgradeOverview.storage?.manager_backup_pattern || '&lt;gmha&gt;.backup-&lt;version&gt;' }}</code></p><p><i>任务记录</i><code>{{ upgradeOverview.storage?.job_state || '~/.gmha/upgrade-jobs.json' }}</code></p></section><section><header><b>安装包仓库</b><span>上传制品与元数据索引</span></header><p><i>仓库根目录</i><code>{{ upgradeOverview.storage?.root || 'software' }}</code></p><p><i>Manager 制品</i><code>{{ upgradeOverview.storage?.manager_package_dir || 'software/gmha-manager' }}</code></p><p><i>Agent 制品</i><code>{{ upgradeOverview.storage?.agent_package_dir || 'software/gmha-agent' }}</code></p><p><i>版本 / SHA 索引</i><code>{{ upgradeOverview.storage?.package_index || 'software/.gmha-package-index.json' }}</code></p></section><section><header><b>Agent 远端机器</b><span>每台机器独立安装与回退</span></header><p><i>运行文件</i><code>{{ upgradeOverview.storage?.agent_install_pattern || '&lt;Agent InstallDir&gt;/agentd' }}</code></p><p><i>升级备份</i><code>{{ upgradeOverview.storage?.agent_backup_pattern || '&lt;Agent InstallDir&gt;/agentd.backup-&lt;version&gt;' }}</code></p><p><i>默认目录</i><code>/home/gmha/agent</code></p><p><i>服务单元</i><code>/etc/systemd/system/gmha-agent.service</code></p></section></div></details>
          <section class="panel upgrade-history"><div class="upgrade-history-head"><div><span>UPGRADE ACTIVITY</span><h3>升级任务与执行进度</h3><p>跟踪版本校验、程序替换、服务重启和升级后检查。</p></div><button type="button" class="secondary" @click="loadUpgrades">↻ 刷新记录</button></div><article v-for="job in upgradeJobs" :key="job.id"><header><div class="upgrade-job-name"><i>{{ job.component === 'manager' ? 'M' : 'A' }}</i><span><b>{{ job.component === 'manager' ? 'Manager' : 'Agent' }} · {{ job.current_version || '未知' }} → {{ job.target_version || '未知' }}</b><small>{{ job.package_name }} · {{ (job.targets || []).join('、') }}</small></span></div><span :class="['status',job.status]">{{ upgradeStatusLabel(job.status) }}</span></header><div class="upgrade-progress-meta"><span>任务进度</span><b>{{ job.progress || 0 }}%</b></div><div class="progress"><i :style="{width:(job.progress || 0)+'%'}"></i></div><ol><li v-for="step in (job.steps || [])" :key="step.name" :class="step.status"><i>{{ step.status==='success'?'✓':step.status==='failed'?'!':step.status==='running'?'…':'○' }}</i><span><b>{{ step.name }}</b><small>{{ step.message || '等待执行' }}</small></span></li></ol><p v-if="job.error" class="upgrade-error"><b>升级异常</b>{{ job.error }}</p></article><div v-if="!upgradeJobs.length" class="upgrade-history-empty"><i>↑</i><b>暂无升级记录</b><span>选择上方版本包并创建升级任务后，执行进度会实时显示在这里。</span><button type="button" class="text-button" @click="active='packages'">前往安装包管理 →</button></div></section>
        </template>
        <template v-else-if="active === 'manager'"><section class="manager-hero"><div><p>MANAGER RUNTIME</p><h2>管理端运行控制</h2><span>配置服务监听、数据库和 Agent 二进制，并在此管理后台进程。</span></div><div class="manager-state"><span :class="['status', data.manager.running ? 'online' : 'offline']">{{ data.manager.unreachable ? '连接中断' : data.manager.running ? '运行中' : '未运行' }}</span><strong class="manager-runtime-version">{{ data.manager.version || upgradeOverview.manager_version || '版本未上报' }}</strong><b>{{ data.manager.unreachable ? '等待状态接口恢复' : data.manager.running ? 'PID ' + data.manager.pid : '等待启动' }}</b><small v-if="data.manager.started_at && !data.manager.unreachable">{{ date(data.manager.started_at) }} 启动</small><small v-if="data.manager.last_checked_at">{{ date(data.manager.last_checked_at) }} 检测</small></div></section><section class="manager-grid"><article class="panel manager-card"><div class="panel-head"><div><h3>服务控制</h3><p>操作由当前 Manager Runtime 执行</p></div></div><div class="control-body"><div><span>HTTP 监听</span><b>{{ managerForm.listen_http || '—' }}</b></div><div><span>gRPC 监听</span><b>{{ managerForm.listen_grpc || '—' }}</b></div><div><span>日志文件</span><small>{{ data.manager.log_path || '尚未生成' }}</small></div><div v-if="data.manager.unreachable" class="manager-offline-help"><b>Manager HTTP 服务不可达</b><small>Web 页面无法启动承载自身的后端，请在 Manager 主机执行：</small><code>./gmha serve --listen :8080 --grpc-listen :9100</code><small v-if="data.manager.last_error">{{ data.manager.last_error }}</small></div><div class="control-actions"><button v-if="data.manager.unreachable" class="secondary" type="button" @click="refreshManagerStatus">重新检测</button><button v-else class="secondary" @click="managerAction('restart')">重启 Manager</button></div></div></article><form class="panel manager-card" @submit.prevent="saveManagerConfig"><div class="panel-head"><div><h3>启动参数</h3><p>保存后，重启 Manager 生效</p></div><div class="manager-form-actions"><button type="button" class="secondary" @click="testManagerDatabase">测试连接</button><button class="text-button">保存配置</button></div></div><div class="config-form"><label>HTTP 监听<input v-model="managerForm.listen_http" placeholder=":8080"></label><label>gRPC 监听<input v-model="managerForm.listen_grpc" placeholder=":9100"></label><label>Manager HTTP 地址<input v-model="managerForm.manager_http_addr" placeholder="http://192.168.1.10:8080"></label><label>Manager gRPC 地址<input v-model="managerForm.manager_grpc_addr" placeholder="192.168.1.10:9100"></label><label>数据库驱动<select v-model="managerForm.database_driver" @change="managerForm.database_dsn = String()"><option value="sqlite">SQLite</option><option value="mysql">MySQL</option><option value="postgres">PostgreSQL</option></select></label><label v-if="managerForm.database_driver !== 'sqlite'" class="wide">数据库 DSN<input v-model="managerForm.database_dsn" :placeholder="managerForm.database_driver === 'mysql' ? 'gmha:password@tcp(127.0.0.1:3306)/gmha?parseTime=true' : 'postgres://gmha:password@127.0.0.1:5432/gmha?sslmode=disable'"></label><label v-else class="wide">SQLite 数据库路径<input v-model="managerForm.db_path" placeholder="./data/manager.db"></label><label class="wide">Agent 二进制路径<input v-model="managerForm.agent_binary_path" placeholder="./bin/agentd"></label><label class="wide">Manager SSH 公钥路径<input v-model="managerForm.manager_public_key" placeholder="/home/gmha/.ssh/id_ed25519.pub"><small>对应私钥用于验证已有 SSH 互信；保存后重启 Manager 生效。</small></label></div></form></section></template>
        <template v-else-if="active === 'overview'">
          <section class="panel overview-profile"><div class="panel-head"><div><h3>基本运行信息</h3><p>Manager 服务、数据库和资源接入状态</p></div><span :class="['status', data.manager.running ? 'online' : 'offline']">{{ data.manager.running ? 'Manager 正常运行' : 'Manager 未运行' }}</span></div><div class="overview-kv-grid"><div><small>Manager HTTP 地址</small><b>{{ managerForm.manager_http_addr || managerForm.listen_http || '—' }}</b></div><div><small>Manager gRPC 地址</small><b>{{ managerForm.manager_grpc_addr || managerForm.listen_grpc || '—' }}</b></div><div><small>数据库类型</small><b>{{ managerForm.database_driver || 'SQLite' }}</b></div><div><small>集群数量</small><b>{{ data.clusters.length }}</b></div><div><small>受管机器</small><b>{{ machineTotal }}</b></div><div><small>Agent 总数</small><b>{{ data.agents.length }}</b></div></div></section>
          <section class="metric-grid"><article v-for="item in metrics" :key="item.label" class="metric-card"><span :class="['metric-dot', item.tone]"></span><p>{{ item.label }}</p><strong>{{ item.value }}</strong><small>{{ item.hint }}</small></article></section>
          <section class="panel"><div class="panel-head"><div><h3>最近任务</h3><p>查看最近提交的自动化运维任务</p></div><button class="text-button" @click="active = 'tasks'">查看全部 →</button></div><TaskTable :items="recentTasks" :machines="data.machines" :state="state" :date="date" @select="openTaskDetail" /></section>
        </template>


        <template v-else-if="active === 'machines'">
          <section class="panel machine-management-panel">
            <div class="panel-head"><div><h3>受管机器</h3><p>按机器或集群筛选，支持跨页选择并批量管理</p></div><span class="count">{{ machineTotal }} 台</span></div>
            <form class="machine-filter-bar" @submit.prevent="searchMachines">
              <input v-model.trim="machineKeyword" placeholder="搜索机器名称、IP、机器 ID 或 SSH 用户">
              <select v-model="machineClusterFilter"><option value="all">全部集群</option><option value="__unassigned__">未分配集群</option><option v-for="cluster in data.clusters" :key="cluster.Name || cluster.name" :value="cluster.Name || cluster.name">{{ cluster.Name || cluster.name }}</option></select>
              <button class="secondary">筛选</button><button type="button" class="text-button" @click="machineKeyword='';machineClusterFilter='all';searchMachines()">重置</button>
            </form>
            <div class="machine-bulk-toolbar"><div><button type="button" class="secondary" @click="toggleCurrentMachinePage">{{ data.machines.length && data.machines.every(item => selectedMachineIDs.includes(item.ID || item.id)) ? '取消当前页全选' : '当前页全选' }}</button><span>已选择 <b>{{ selectedMachineIDs.length }}</b> 台</span></div><div><button class="secondary" @click="openBatchOnboard">＋ 批量纳管</button><button class="danger-button" :disabled="!selectedMachineIDs.length" @click="openBulkDelete">批量删除</button></div></div>
            <div class="machine-table-wrap"><table class="machine-pool-table"><thead><tr><th class="machine-check-column"><input type="checkbox" :checked="data.machines.length && data.machines.every(item => selectedMachineIDs.includes(item.ID || item.id))" @change="toggleCurrentMachinePage"></th><th>机器</th><th>地址</th><th>SSH</th><th>所属集群</th><th>凭证</th><th>状态</th><th>操作</th></tr></thead><tbody><tr v-for="item in data.machines" :key="item.ID || item.id" :class="{selected:selectedMachineIDs.includes(item.ID || item.id)}"><td class="machine-check-column"><input v-model="selectedMachineIDs" type="checkbox" :value="item.ID || item.id"></td><td><b>{{ label(item.Name || item.name) }}</b><small>{{ item.ID || item.id }}</small></td><td>{{ item.IP || item.ip }}</td><td>{{ item.SSHUser || item.ssh_user || 'root' }}:{{ item.SSHPort || item.ssh_port || 22 }}</td><td><button v-if="machineCluster(item) === '未分配集群'" class="cluster-label unassigned cluster-assign-button" @click="openQuickClusterAssign(item)">未分配集群</button><span v-else class="cluster-label">{{ machineCluster(item) }}</span></td><td>{{ data.credentials.find(c => c.id === (item.CredentialID || item.credential_id))?.name || (item.CredentialID || item.credential_id ? '已关联' : '未分配') }}</td><td><span :class="['status', machineStatus(item).code]">{{ machineStatus(item).label }}</span></td><td><button class="text-button" @click="showMachine(item.ID || item.id)">详情 / 编辑</button></td></tr><tr v-if="!data.machines.length"><td colspan="8" class="empty">{{ machineKeyword || machineClusterFilter !== 'all' ? '没有符合筛选条件的机器。' : '尚未纳管机器。点击“批量纳管”开始添加。' }}</td></tr></tbody></table></div>
            <div class="pager"><button :disabled="machinePage === 1" @click="changeMachinePage(-1)">上一页</button><span>第 {{ machinePage }} 页 · 共 {{ machineTotal }} 台</span><button :disabled="machinePage * pageSize >= machineTotal" @click="changeMachinePage(1)">下一页</button></div>
          </section>
          <section class="panel credentials-panel"><div class="panel-head"><div><h3>SSH 凭证库</h3><p>凭证独立管理，可批量分配至一台或多台机器；敏感内容不会回显。</p></div><button class="text-button" @click="showCredential = true">添加凭证 →</button></div><table><thead><tr><th>凭证名称</th><th>认证方式</th><th>SSH 用户</th><th>更新时间</th><th></th></tr></thead><tbody><tr v-for="item in data.credentials" :key="item.id"><td><b>{{ item.name }}</b><small>{{ item.id }}</small></td><td><span class="credential-type">{{ item.type === 'private_key' ? 'SSH 私钥文件' : '密码' }}</span></td><td>{{ item.ssh_user }}</td><td>{{ item.updated_at }}</td><td><button class="text-button" @click="chooseCredential(item.id)">分配机器</button><button class="danger-link" @click="deleteCredential(item)">删除</button></td></tr><tr v-if="!data.credentials.length"><td colspan="5" class="empty">暂无凭证。请先添加密码或 SSH 私钥文件凭证。</td></tr></tbody></table><div class="pager"><button :disabled="credentialPage === 1" @click="changePage(credentialPage, -1, credentialTotal)">上一页</button><span>第 {{ credentialPage }} 页 · 共 {{ credentialTotal }} 条</span><button :disabled="credentialPage * pageSize >= credentialTotal" @click="changePage(credentialPage, 1, credentialTotal)">下一页</button></div></section>
        </template>

        <template v-else-if="active === 'automation'">
          <section class="automation-page">
            <header class="automation-hero"><div><p>MULTI-CLUSTER AUTOMATION</p><h2>多集群自动化任务</h2><span>一次选择多个集群，使用统一参数并行创建独立任务；执行进度可在任务中心持续跟踪。</span></div><strong>{{ automationSelectedClusters.length }}<small>已选集群</small></strong></header>
            <form class="automation-workspace" @submit.prevent="submitAutomationTask">
              <section class="panel automation-targets"><div class="panel-head"><div><h3>1. 选择目标集群</h3><p>任务将同时提交到所选集群</p></div><button type="button" class="text-button" @click="toggleAllAutomationClusters">{{ automationSelectedClusters.length === data.clusters.length && data.clusters.length ? '取消全选' : '选择全部' }}</button></div><div class="automation-cluster-list"><label v-for="item in data.clusters" :key="item.Name || item.name" :class="{selected:automationSelectedClusters.includes(item.Name || item.name)}"><input v-model="automationSelectedClusters" type="checkbox" :value="item.Name || item.name"><span><b>{{ item.Name || item.name }}</b><small>{{ item.Description || item.description || '暂无描述' }}</small></span><em>{{ clusterMachineCount(item) }} 台机器</em></label><div v-if="!data.clusters.length" class="empty">尚未创建集群，请先在“集群列表”中创建。</div></div></section>
              <section class="panel automation-config">
                <div class="panel-head"><div><h3>2. 配置自动化操作</h3><p>按集群展开为独立 Agent 任务；数据库操作使用结构化参数生成命令。</p></div></div>
                <div class="automation-action-picker">
                  <label :class="{selected:automationForm.action==='install'}"><input v-model="automationForm.action" type="radio" value="install"><span><b>批量安装 MySQL</b><small>为所选集群内的机器创建安装任务</small></span></label>
                  <label class="danger" :class="{selected:automationForm.action==='uninstall'}"><input v-model="automationForm.action" type="radio" value="uninstall"><span><b>批量卸载 MySQL</b><small>卸载指定端口的已登记实例</small></span></label>
                  <label :class="{selected:automationForm.action==='collect_machine'}"><input v-model="automationForm.action" type="radio" value="collect_machine"><span><b>采集机器信息</b><small>采集系统、CPU、内存、磁盘和网络信息</small></span></label>
                  <label :class="{selected:automationForm.action==='collect_mysql'}"><input v-model="automationForm.action" type="radio" value="collect_mysql"><span><b>采集 MySQL 数据</b><small>采集版本、连接、QPS/状态等运行数据</small></span></label>
                  <label :class="{selected:automationForm.action==='mysql_user'}"><input v-model="automationForm.action" type="radio" value="mysql_user"><span><b>数据库用户与权限</b><small>批量增删改查用户，授予或回收权限</small></span></label>
                  <label :class="{selected:automationForm.action==='mysql_parameter'}"><input v-model="automationForm.action" type="radio" value="mysql_parameter"><span><b>修改数据库参数</b><small>选择动态生效或写配置后重启生效</small></span></label>
                  <label :class="{selected:automationForm.action==='backup'}"><input v-model="automationForm.action" type="radio" value="backup"><span><b>批量执行备份</b><small>触发所选集群内已启用的备份策略</small></span></label>
                  <label :class="{selected:automationForm.action==='shell'}"><input v-model="automationForm.action" type="radio" value="shell"><span><b>下发 Shell 脚本</b><small>在所选集群的全部在线 Agent 上执行脚本</small></span></label>
                </div>
                <div class="automation-form-grid">
                  <template v-if="automationForm.action==='install'"><label>MySQL 端口<input v-model.number="automationForm.port" type="number" min="1" required></label><label>起始 server_id<input v-model.number="automationForm.server_id_start" type="number" min="1" required></label><label>MySQL 运行用户<input v-model.trim="automationForm.mysql_user" required></label><label>参数 Profile<input v-model.trim="automationForm.profile" required></label><label class="wide">root 密码<input v-model="automationForm.root_password" type="password" required></label><label class="automation-check wide"><input v-model="automationForm.install_pt_tools" type="checkbox"><span><b>安装 PT 工具</b><small>MySQL 验证成功后安装 Percona Toolkit 及 Perl 依赖，默认关闭</small></span></label><label class="automation-check wide"><input v-model="automationForm.install_xtrabackup" type="checkbox"><span><b>安装 XtraBackup</b><small>根据各目标的 MySQL 主版本、架构和 glibc 自动匹配，默认关闭</small></span></label><label class="wide">内存分配器<select v-model="automationForm.memory_allocator"><option value="system">系统默认（推荐）</option><option value="tcmalloc">tcmalloc（显式启用并验证）</option></select><small>tcmalloc 不是内存泄漏修复方案；启用后会通过 systemd LD_PRELOAD 加载并校验。</small></label></template>
                  <template v-else-if="automationForm.action==='uninstall'"><label>MySQL 端口<input v-model.number="automationForm.port" type="number" min="1" required></label><div class="automation-danger wide"><b>高风险操作</b><p>将同时为 {{ automationSelectedClusters.length }} 个集群创建卸载任务。数据目录可能被删除，请确认已完成备份。</p></div><label class="wide">二次确认<input v-model="automationConfirm" :placeholder="'UNINSTALL '+automationSelectedClusters.length+' CLUSTERS'" required><small>请输入：<code>UNINSTALL {{ automationSelectedClusters.length }} CLUSTERS</code></small></label></template>
                  <template v-else-if="automationForm.action==='collect_mysql'"><label>MySQL 端口<input v-model.number="automationForm.port" type="number" min="1"></label><label>管理员用户名<input v-model.trim="automationForm.mysql_user"></label><label>管理员密码<input v-model="automationForm.mysql_password" type="password"></label><div class="automation-info wide"><b>采集内容</b><p>版本、端口、连接数、QPS/Queries、运行时长等会保存到任务输出，可下载汇总报告。</p></div></template>
                  <template v-else-if="automationForm.action==='mysql_user'"><label>MySQL 端口<input v-model.number="automationForm.port" type="number" min="1"></label><label>管理员用户名<input v-model.trim="automationForm.mysql_user"></label><label>管理员密码<input v-model="automationForm.mysql_password" type="password"></label><label>操作<select v-model="automationForm.user_action"><option value="create">创建或覆盖密码并授权</option><option value="update">修改密码</option><option value="delete">删除用户</option><option value="grant">增加权限</option><option value="revoke">回收权限</option><option value="query">查询用户授权</option><option value="list">列出全部用户</option></select></label><label v-if="automationForm.user_action!=='list'">目标用户名<input v-model.trim="automationForm.target_username" placeholder="app_user"></label><label v-if="automationForm.user_action!=='list'">来源 Host<input v-model.trim="automationForm.target_host" placeholder="%"></label><label v-if="['create','update'].includes(automationForm.user_action)">目标用户密码<input v-model="automationForm.target_password" type="password"></label><fieldset v-if="['create','grant','revoke'].includes(automationForm.user_action)" class="wide"><legend>权限选择</legend><label v-for="privilege in mysqlPrivilegeOptions" :key="privilege" class="check-label"><input v-model="automationForm.privileges" type="checkbox" :value="privilege">{{ privilege }}</label></fieldset><div v-if="automationForm.user_action==='list'" class="automation-info wide"><b>用户清单</b><p>会读取每台目标 MySQL 的用户、来源 Host 与锁定状态，并写入可下载的执行报告。</p></div></template>
                  <template v-else-if="automationForm.action==='mysql_parameter'"><label>MySQL 端口<input v-model.number="automationForm.port" type="number" min="1"></label><label>管理员用户名<input v-model.trim="automationForm.mysql_user"></label><label>管理员密码<input v-model="automationForm.mysql_password" type="password"></label><label>参数名称<input v-model.trim="automationForm.parameter_name" placeholder="max_connections"></label><label>参数值<input v-model.trim="automationForm.parameter_value" placeholder="500"></label><label>生效方式<select v-model="automationForm.apply_mode"><option value="dynamic">动态生效（SET GLOBAL）</option><option value="restart">写入配置并重启生效</option><option value="both">动态生效并写入配置</option></select></label><template v-if="['restart','both'].includes(automationForm.apply_mode)"><label>配置文件路径<input v-model.trim="automationForm.config_path"></label><label>systemd 服务名<input v-model.trim="automationForm.systemd_unit"></label></template><div class="automation-danger wide" v-if="['restart','both'].includes(automationForm.apply_mode)"><b>重启提示</b><p>将修改配置文件并重启 MySQL 服务。请确认业务允许重启窗口。</p></div></template>
                  <template v-else-if="automationForm.action==='shell'"><label class="wide">Shell 脚本<textarea v-model="automationForm.script" rows="10" spellcheck="false" placeholder="#!/usr/bin/env bash&#10;hostname&#10;df -h"></textarea><small>脚本会分别在各目标机器执行，标准输出和错误输出将保存到任务事件中。</small></label></template>
                  <template v-else-if="automationForm.action==='backup'"><div class="automation-info wide"><b>批量备份</b><p>会立即触发所选集群内全部“已启用”的备份策略；策略中保存的备份账号、目标路径、调度和安全阈值将被复用。</p></div></template>
                  <template v-else><div class="automation-info wide"><b>机器信息采集</b><p>将为所选集群内的每台机器创建采集任务。采集完成后可在机器详情与任务中心查看结果。</p></div></template>
                </div>
                <div class="automation-submit"><span>将操作 {{ automationSelectedClusters.length }} 个集群</span><button :class="automationForm.action==='uninstall' ? 'danger-button' : 'primary'" :disabled="automationRunning || !automationSelectedClusters.length">{{ automationRunning ? '正在提交…' : '创建自动化任务' }}</button></div>
              </section>
            </form>
            <section v-if="automationResults.length" class="panel automation-results"><div class="panel-head"><div><h3>最近提交结果</h3><p>按集群与机器展示任务创建结果；Shell 输出可汇总下载。</p></div><div><a v-if="automationResults.some(item=>item.task_id)" class="text-button" :href="'/api/v1/tasks/cluster-automation/report?task_ids='+automationResults.map(item=>item.task_id).filter(Boolean).join(',')" download>下载执行报告</a><button class="text-button" @click="active='tasks'">前往任务中心 →</button></div></div><table><thead><tr><th>集群</th><th>目标机器</th><th>提交状态</th><th>任务 ID</th><th>结果说明</th></tr></thead><tbody><tr v-for="item in automationResults" :key="item.task_id || item.cluster+item.machine"><td><b>{{ item.cluster }}</b></td><td>{{ item.machine || '集群任务' }}</td><td><span :class="['status',item.status]">{{ item.status==='success' ? '提交成功' : '提交失败' }}</span></td><td>{{ item.task_id || '—' }}</td><td :class="{'error-cell':item.status==='failed'}">{{ errorSummary(item.message, 140) }}</td></tr></tbody></table></section>
          </section>
        </template>

        <template v-else-if="active === 'agents'">
          <section class="agent-resource-intro">
            <div><span>低开销自监控</span><b>资源数据随 Agent 心跳上报</b><small>Agent 每 15 秒在本机读取一次 /proc；页面默认每 30 秒读取 Manager 快照，不会额外发起 SSH 或远程采集。</small></div>
            <div class="agent-resource-refresh"><label>页面刷新<select v-model.number="agentResourceRefreshSeconds"><option :value="15">15 秒</option><option :value="30">30 秒（推荐）</option><option :value="60">60 秒</option><option :value="120">2 分钟</option></select></label><button type="button" class="secondary" :disabled="agentResourceRefreshing" @click="refreshAgentResources(false)">{{ agentResourceRefreshing ? '刷新中…' : '立即刷新' }}</button><small>最近刷新：{{ agentResourceUpdatedAt ? date(agentResourceUpdatedAt) : '等待首次刷新' }}</small></div>
          </section>
          <section class="panel agent-upgrade-center"><div class="agent-upgrade-head"><div><span>AGENT VERSION CONTROL</span><h3>Agent 批量版本升级</h3><p>升级制品、版本判断和目标选择归属于 Agent 管理；列表继续使用当前搜索条件和 50 台分页。</p></div><div class="agent-version-head-actions"><button type="button" class="secondary" :disabled="agentVersionBatchDetecting" @click="detectUnknownAgentVersions">{{ agentVersionBatchDetecting ? '正在检测版本…' : '检测当前页未知版本' }}</button><button type="button" class="secondary" @click="active='packages';packageForm.category='gmha-agent'">管理 Agent 制品</button></div></div><div v-if="upgradeOverview.agent_packages.length" class="agent-upgrade-controls"><label>目标升级版本<select v-model="upgradeForm.agent_package" @change="upgradeAgentPackageChanged"><option value="">请选择 gmha-agent 制品</option><option v-for="pkg in upgradeOverview.agent_packages" :key="pkg.name" :value="pkg.name">{{ pkg.version }} · {{ pkg.arch }} · {{ pkg.name }}</option></select></label><div v-if="selectedAgentUpgradePackage()" class="agent-version-stats"><span><b>{{ selectedAgentUpgradePackage().upgradeable_count }}</b><small>可升级</small></span><span><b>{{ selectedAgentUpgradePackage().current_count }}</b><small>同版本</small></span><span class="danger"><b>{{ selectedAgentUpgradePackage().downgrade_count }}</b><small>禁止降级</small></span><span><b>{{ selectedAgentUpgradePackage().unknown_count }}</b><small>版本未知</small></span></div><div class="agent-upgrade-actions"><span>已跨页选择 <b>{{ upgradeForm.targets.length }}</b> 台</span><button type="button" class="secondary" :disabled="!upgradeForm.agent_package" @click="toggleAgentManagementUpgradePage">{{ agentManagementUpgradePageSelected() ? '取消当前页选择' : '选择当前页可升级项' }}</button><button type="button" class="primary" :disabled="upgradeSubmitting || !upgradeForm.agent_package || !upgradeForm.targets.length" @click="startAgentUpgrade">{{ upgradeSubmitting ? '正在提交…' : '升级选中的 '+upgradeForm.targets.length+' 台' }}</button></div></div><div v-else class="agent-upgrade-empty"><span>▣</span><div><b>尚未上传 Agent 升级制品</b><small>先上传带版本号的 Linux ELF 文件，系统才能比较当前版本并启用批量选择。</small><code>{{ upgradeOverview.storage?.agent_package_dir || 'software/gmha-agent' }}</code></div><button type="button" class="primary" @click="active='packages';packageForm.category='gmha-agent'">上传 Agent 制品</button></div></section>
          <section class="metric-grid agent-metrics">
            <article class="metric-card"><span class="metric-dot green"></span><p>在线 Agent</p><strong>{{ data.agents.filter(item => agentStatus(item).code === 'agent_online').length }}</strong><small>当前页心跳正常</small></article>
            <article class="metric-card"><span class="metric-dot blue"></span><p>Agent 平均 CPU</p><strong>{{ agentResourceAverage('cpu') === null ? '—' : agentResourceAverage('cpu').toFixed(2) + '%' }}</strong><small>进程占目标机器总 CPU</small></article>
            <article class="metric-card"><span class="metric-dot amber"></span><p>Agent 内存合计</p><strong>{{ agentResourceTotal('memory') === null ? '—' : agentResourceTotal('memory').toFixed(1) + ' MB' }}</strong><small>当前页进程 RSS 合计</small></article>
            <article class="metric-card"><span class="metric-dot red"></span><p>资源数据覆盖</p><strong>{{ agentResourceCoverage() }}/{{ data.agents.length }}</strong><small>旧版本 Agent 可通过升级启用</small></article>
          </section>
          <section class="panel agent-resource-panel"><div class="panel-head"><div><h3>Agent 管理</h3><p>查看版本、进程资源与心跳状态；选择升级制品后可跨页勾选兼容目标</p></div><span class="count">{{ data.agentTotal }} 个</span></div><form class="agent-management-filter" @submit.prevent="data.agentPage=1;refresh()"><input v-model.trim="data.agentKeyword" placeholder="搜索机器名、IP 或集群"><select v-model="data.agentStatus"><option value="all">全部状态</option><option value="online">在线</option><option value="error">异常 / 离线</option><option value="pending">待安装</option></select><select v-model="data.agentVersion"><option value="all">全部版本</option><option v-for="item in (upgradeOverview.agent_versions || [])" :key="item.version" :value="item.version">{{ item.version }}（{{ item.count }} 台）</option></select><button class="secondary">查询</button></form><div class="agent-table-wrap"><table><thead><tr><th class="agent-upgrade-check"><input type="checkbox" :checked="agentManagementUpgradePageSelected()" :disabled="!upgradeForm.agent_package" aria-label="选择当前页可升级 Agent" @change="toggleAgentManagementUpgradePage"></th><th>机器</th><th>所属集群</th><th>Agent 状态</th><th>当前版本</th><th>CPU 占用</th><th>RSS 内存</th><th>心跳 / 健康</th><th>操作</th></tr></thead><tbody><tr v-for="item in data.agents" :key="item.IP || item.ip" :class="{selected:upgradeForm.targets.includes(item.IP || item.ip)}"><td class="agent-upgrade-check"><input v-model="upgradeForm.targets" type="checkbox" :value="item.IP || item.ip" :disabled="!upgradeAgentSelectable(item)" :aria-label="'选择 '+(item.Name || item.name || item.IP || item.ip)"></td><td><b>{{ item.Name || item.name || item.IP || item.ip }}</b><small>{{ item.IP || item.ip }}</small><small class="install-dir">{{ item.InstallDir || item.install_dir || '—' }}</small></td><td><span :class="['cluster-label', { unassigned: machineCluster(item) === '未分配集群' }]">{{ machineCluster(item) }}</span></td><td><span :class="['status', agentStatus(item).code]">{{ agentStatus(item).label }}</span><small>{{ item.RecoveryState || item.recovery_state || '未执行恢复' }}</small></td><td><b>{{ item.Version || item.version || '未知' }}</b><small :class="['upgrade-relation',upgradeAgentRelation(item)]">{{ upgradeAgentRelation(item)==='upgrade' ? '可升级到 '+selectedAgentUpgradePackage()?.version : upgradeAgentRelation(item)==='current' ? '已是目标版本' : upgradeAgentRelation(item)==='downgrade' ? '目标版本较低' : upgradeAgentRelation(item)==='unknown' ? '版本无法比较' : '请选择制品' }}</small></td><td><div :class="['agent-resource-value', { unavailable: agentResource(item).cpu === null }]"><b>{{ agentCPU(item) }}</b><i><span :style="{width: Math.min(100, Math.max(0, agentResource(item).cpu || 0)) + '%'}"></span></i></div></td><td><div :class="['agent-resource-value memory', { unavailable: agentResource(item).memory === null }]"><b>{{ agentMemory(item) }}</b><i><span :style="{width: Math.min(100, Math.max(0, (agentResource(item).memory || 0) / 2.56)) + '%'}"></span></i><small v-if="agentResource(item).needsUpgrade">升级 Agent 后可采集</small><small v-else-if="agentResource(item).error">指标暂不可用</small><small v-else>{{ agentResource(item).collectedAt ? date(agentResource(item).collectedAt) : '等待采集' }}</small></div></td><td><b>{{ item.HeartbeatState || item.heartbeat_state || 'INIT' }}</b><small>{{ item.OverallHealth || item.overall_health || '—' }} · {{ item.LastHeartbeatAt || item.last_heartbeat_at || '暂无心跳' }}</small></td><td class="agent-actions"><button class="text-button" @click="showAgent(item.IP || item.ip)">详情</button><button v-if="agentResource(item).needsUpgrade || agentStatus(item).code === 'agent_online'" class="text-button" @click="upgradeAgent(item)">单机升级</button><button v-if="agentStatus(item).code !== 'agent_online'" class="text-button" @click="retryAgent(item)">重试安装</button><button v-if="agentStatus(item).code === 'agent_error'" class="danger-link" @click="recover(item.IP || item.ip)">手动拉起</button></td></tr><tr v-if="!data.agents.length"><td colspan="9" class="empty">暂无 Agent 信息。</td></tr></tbody></table></div><div class="upgrade-agent-list-pager"><span>第 {{ data.agentPage }} 页 · 共 {{ data.agentTotal }} 台 · 每页 {{ data.agentPageSize }} 台</span><div><button type="button" class="secondary" :disabled="data.agentPage===1" @click="data.agentPage--;refresh()">上一页</button><button type="button" class="secondary" :disabled="data.agentPage*data.agentPageSize>=data.agentTotal" @click="data.agentPage++;refresh()">下一页</button></div></div></section>
          <details class="panel component-upgrade-history agent-upgrade-history"><summary>最近 Agent 批量升级记录 <span>{{ upgradeJobs.filter(item=>item.component==='agent').length }} 条</span></summary><div><p v-for="job in upgradeJobs.filter(item=>item.component==='agent').slice(0,5)" :key="job.id"><b>{{ job.current_version }} → {{ job.target_version }}</b><small>{{ date(job.created_at) }} · {{ (job.targets || []).length }} 台 · {{ job.package_name }}</small><span :class="['status',job.status]">{{ upgradeStatusLabel(job.status) }}</span></p><p v-if="!upgradeJobs.some(item=>item.component==='agent')" class="empty">暂无 Agent 批量升级记录。</p></div></details>
          <section class="panel recovery-panel"><div class="panel-head"><div><h3>最近恢复任务</h3><p>展示 CLI“查看恢复任务”相同的真实恢复记录</p></div><span class="count">最近 {{ data.recovery.length }} 条</span></div><table><thead><tr><th>机器</th><th>状态</th><th>触发方式</th><th>动作</th><th>次数</th><th>时间</th><th>错误</th></tr></thead><tbody><tr v-for="item in data.recovery" :key="item.ID || item.id"><td>{{ item.MachineIP || item.machine_ip }}</td><td><span :class="['status', state(item.Status || item.status)]">{{ label(item.Status || item.status) }}</span></td><td>{{ item.Trigger || item.trigger || '—' }}</td><td>{{ item.Action || item.action || '—' }}</td><td>{{ item.Attempt || item.attempt || 0 }}</td><td>{{ item.CreatedAt || item.created_at || '—' }}</td><td><small class="error-cell">{{ errorSummary(item.LastError || item.last_error || '') || '—' }}</small></td></tr><tr v-if="!data.recovery.length"><td colspan="7" class="empty">暂无恢复任务。</td></tr></tbody></table></section>
        </template>

        <template v-else-if="active === 'tasks'"><section v-if="selectedTaskDetail" class="task-detail-page"><div class="task-detail-titlebar"><div><button class="task-back" @click="closeTaskDetail">{{ taskDetailStack.length ? '← 返回父任务' : '← 返回任务列表' }}</button><h2>{{ taskTitle(taskObject()) }}</h2></div><div><button class="secondary" @click="selectCurrentTaskStep">定位当前进度</button><button class="primary" @click="refreshSelectedTaskDetail(false)">刷新进度</button></div></div><section class="task-meta-panel"><div><small>任务 ID</small><b>{{ taskObject()?.ID || taskObject()?.id }}</b></div><div><small>任务状态</small><span :class="['status', state(taskObject()?.Status || taskObject()?.status)]">{{ taskStatusLabel(taskObject()?.Status || taskObject()?.status) }}</span></div><div><small>操作对象</small><b>{{ selectedTaskDetail.machine_name || selectedTaskDetail.MachineName || taskObject()?.MachineID || taskObject()?.machine_id || '—' }}</b><em>{{ selectedTaskDetail.machine_ip || selectedTaskDetail.MachineIP || '' }}</em></div><div><small>开始时间</small><b>{{ date(taskObject()?.StartedAt || taskObject()?.started_at || taskObject()?.CreatedAt || taskObject()?.created_at) }}</b></div><div><small>任务耗时</small><b>{{ elapsed(taskObject()?.StartedAt || taskObject()?.started_at, taskObject()?.FinishedAt || taskObject()?.finished_at) }}</b></div><div><small>当前进度</small><b>{{ taskObject()?.ProgressPercent ?? taskObject()?.progress_percent ?? 0 }}% · {{ taskFlowSuccessCount() }}/{{ taskFlowStepCount() }}</b></div></section><div class="task-detail-progress"><i :style="{width:(taskObject()?.ProgressPercent ?? taskObject()?.progress_percent ?? 0)+'%'}"></i></div><div class="task-detail-workspace"><aside class="task-step-panel task-flow-panel"><header><div><b>执行流程</b><small>{{ taskFlowStepCount() }} 个执行步骤 · {{ taskFlowDetails().length }} 个任务，按任务关系完整展示</small></div><span>{{ taskFlowSuccessCount() }}/{{ taskFlowStepCount() }}</span></header><nav v-if="taskFlowDetails().length>1" class="task-flow-switcher" aria-label="执行任务切换"><button v-for="(detail,index) in taskFlowDetails()" :key="taskObject(detail)?.ID || taskObject(detail)?.id" :class="{active:(taskObject(selectedTaskFlowView())?.ID || taskObject(selectedTaskFlowView())?.id)===(taskObject(detail)?.ID || taskObject(detail)?.id)}" @click="selectTaskFlow(detail)"><i :class="state(taskObject(detail)?.Status || taskObject(detail)?.status)"></i><span><b>{{ taskFlowTabLabel(detail,index) }}</b><small>{{ index ? taskTitle(taskObject(detail)) : '业务编排总流程' }}</small></span><em>{{ taskStatusLabel(taskObject(detail)?.Status || taskObject(detail)?.status) }} · {{ taskObject(detail)?.ProgressPercent ?? taskObject(detail)?.progress_percent ?? 0 }}%</em></button></nav><div class="task-flow-tree"><section class="task-flow-node"><div :class="['task-flow-task',state(taskObject(selectedTaskFlowView())?.Status || taskObject(selectedTaskFlowView())?.status)]"><i></i><span><b>{{ taskTitle(taskObject(selectedTaskFlowView())) }}</b><small>{{ taskObject(selectedTaskFlowView())?.MachineID || taskObject(selectedTaskFlowView())?.machine_id || '业务任务' }} · #{{ taskObject(selectedTaskFlowView())?.ID || taskObject(selectedTaskFlowView())?.id }}</small></span><em>{{ taskStatusLabel(taskObject(selectedTaskFlowView())?.Status || taskObject(selectedTaskFlowView())?.status) }} · {{ taskObject(selectedTaskFlowView())?.ProgressPercent ?? taskObject(selectedTaskFlowView())?.progress_percent ?? 0 }}%</em></div><div class="task-flow-steps"><button v-for="step in taskSteps(selectedTaskFlowView())" :key="step.ID || step.id" :class="['task-step-item',state(step.Status || step.status),{active:(selectedTaskStep?.ID || selectedTaskStep?.id)===(step.ID || step.id)}]" @click="chooseTaskStep(step,selectedTaskFlowView())"><i>{{ ['success','completed'].includes(state(step.Status || step.status)) ? '✓' : ['failed','error'].includes(state(step.Status || step.status)) ? '!' : state(step.Status || step.status)==='running' ? '…' : '' }}</i><span><b>{{ errorSummary(step.Message || step.message || step.StepName || step.step_name,120) }}</b><small>{{ step.StepName || step.step_name }}</small></span><em>{{ stepStatusLabel(step.Status || step.status) }}</em><time>{{ elapsed(step.StartedAt || step.started_at,step.FinishedAt || step.finished_at) }}</time></button><div v-if="!taskSteps(selectedTaskFlowView()).length" class="task-flow-empty">该任务暂无独立执行步骤</div></div></section></div></aside><section class="task-log-panel"><header><div><b>{{ errorSummary(selectedTaskStep?.Message || selectedTaskStep?.message || selectedTaskStep?.StepName || selectedTaskStep?.step_name || '任务日志', 140) }}</b><small v-if="selectedTaskStep">{{ taskTitle(taskObject(selectedTaskFlowDetail)) }} · #{{ taskObject(selectedTaskFlowDetail)?.ID || taskObject(selectedTaskFlowDetail)?.id }} · {{ selectedTaskStep.StepName || selectedTaskStep.step_name }} · {{ stepStatusLabel(selectedTaskStep.Status || selectedTaskStep.status) }}</small></div><span>{{ selectedTaskEvents.length }} 条事件</span></header><div class="task-log-content"><div v-if="['failed','error'].includes(state(selectedTaskStep?.Status || selectedTaskStep?.status))" class="task-log-error-summary"><b>失败原因</b><pre>{{ safeLog(selectedTaskStep?.Message || selectedTaskStep?.message || 'Agent 未返回具体错误，请检查下方 ERROR 事件和 Agent 日志。') }}</pre><small>所属任务：{{ taskTitle(taskObject(selectedTaskFlowDetail)) }} · #{{ taskObject(selectedTaskFlowDetail)?.ID || taskObject(selectedTaskFlowDetail)?.id }}</small></div><article v-for="(event,index) in selectedTaskEvents" :key="event.ID || event.id || index" :class="['task-log-line', state(event.EventType || event.event_type)]"><i>{{ index + 1 }}</i><time>{{ date(event.CreatedAt || event.created_at) }}</time><b>{{ String(event.EventType || event.event_type || 'log').toUpperCase() }}</b><pre>{{ safeLog(event.Content || event.content) }}</pre></article><div v-if="!selectedTaskEvents.length" class="task-log-empty"><b>当前步骤暂无独立日志</b><p>{{ safeLog(selectedTaskStep?.Message || selectedTaskStep?.message || '步骤尚未开始，或 Agent 未上报日志事件。') }}</p></div></div></section></div></section><section v-else class="panel task-center-panel"><section class="task-bulk-toolbar"><span>当前页已选择 <b>{{ selectedTaskIDs.length }}</b> 条已完成任务</span><div><button class="secondary" @click="selectCurrentTaskPage">{{ selectedTaskIDs.length ? '取消当前页选择' : '选择当前页已完成任务' }}</button><button class="danger-button" :disabled="!selectedTaskIDs.length" @click="deleteTaskRecords(false)">批量清理选中记录</button><button class="danger-link" :disabled="!taskTotal" @click="deleteTaskRecords(true)">清理筛选结果</button></div></section><div class="panel-head"><div><h3>任务列表</h3><p>一个用户操作只展示一个业务父任务；各机器执行子任务收纳在详情中</p></div><div class="task-toolbar"><select v-model="taskTypeFilter"><option value="all">全部类型</option><option value="collect_machine_info">机器信息采集</option><option value="collect_static_info">静态资产采集</option><option value="mysql_install">MySQL 安装</option><option value="mysql_uninstall">MySQL 卸载</option><option value="mysql_upgrade">MySQL 升级</option><option value="mysql_topology">MySQL 拓扑</option><option value="mysql_cluster_bootstrap">集群初始化</option><option value="batch_operation">批量业务操作</option><option value="architecture_adjustment">架构调整</option><option value="exec">自动化命令</option><option value="platform_operation">平台操作</option></select><div class="task-status-filter"><button :class="{active:taskFilter==='all'}" @click="taskFilter='all'">全部</button><button :class="{active:taskFilter==='running'}" @click="taskFilter='running'">运行中</button><button :class="{active:taskFilter==='success'}" @click="taskFilter='success'">成功</button><button :class="{active:taskFilter==='failed'}" @click="taskFilter='failed'">失败</button></div><input v-model.trim="taskKeyword" placeholder="搜索任务 ID、类型或机器"></div></div><TaskTable :items="filteredTasks" :machines="data.machines" :state="state" :date="date" :page="taskPage" :total="taskTotal" :page-size="taskPageSize" @select="openTaskDetail" @page="changeTaskPage" /></section></template>
        <template v-else-if="active === 'packages'">
          <section class="panel package-panel">
            <div class="panel-head"><div><h3>安装包仓库</h3><p>统一管理 MySQL、路由、中间件、备份和诊断工具，保存版本、来源与 SHA-256 校验信息。</p></div></div>
            <div class="package-config-grid">
              <section class="package-storage"><header><div><b>存储位置</b><small>系统按软件类型自动创建分类目录</small></div><span>{{ packageItems.length }} 个安装包</span></header><div class="storage-path-editor"><input v-model.trim="packageSettings.storage_path" required placeholder="/data/gmha/software"><button class="secondary" type="button" @click="savePackageStorage">保存目录</button></div><div class="architecture-list"><small>支持的 Linux 架构</small><span v-for="arch in (packageSettings.supported_architectures || [])" :key="arch">{{ arch }}</span></div></section>
              <form class="package-upload" @submit.prevent="uploadPackage"><header><div><b>上传安装包</b><small>持久化版本、架构与 SHA-256；Manager / Agent 制品必须提供可比较版本。</small></div></header><div class="package-upload-fields package-upload-fields-extended"><label class="package-field">软件类型<span class="select-control"><select v-model="packageForm.category"><option v-for="category in (packageSettings.categories || [])" :key="category" :value="category">{{ packageCategoryLabel(category) }}</option></select></span></label><label class="package-field">Linux 架构<span class="select-control"><select v-model="packageForm.arch"><option value="未识别">自动识别</option><option value="x86_64">x86_64</option><option value="aarch64">aarch64</option><option value="noarch">noarch</option></select></span></label><label class="package-field">版本号<input v-model.trim="packageForm.version" :required="['gmha-manager','gmha-agent'].includes(packageForm.category) && !/V?\d+\.\d+(\.\d+)?/i.test(packageForm.file?.name || '')" placeholder="例如 V0.0.2（可从文件名识别）"></label><label class="package-field">制品说明<input v-model.trim="packageForm.description" placeholder="变更说明或发布渠道"></label><div class="package-field package-file"><span>安装包文件</span><div class="file-picker"><input id="package-upload-file" type="file" required @change="choosePackageFile"><label for="package-upload-file" class="file-picker-button">选择文件</label><span class="file-picker-name" :title="packageForm.file?.name || ''">{{ packageForm.file?.name || '未选择文件' }}</span></div></div><button class="primary package-upload-button" :disabled="!packageForm.file || (['gmha-manager','gmha-agent'].includes(packageForm.category) && !packageForm.version && !/V?\d+\.\d+(\.\d+)?/i.test(packageForm.file?.name || ''))"><span aria-hidden="true">↑</span>上传并建立版本索引</button></div></form>
            </div>
            <section class="package-quickstart">
              <header><div><span>首次使用推荐</span><h3>一键准备 MySQL 与兼容工具</h3><p>选择 MySQL 版本后，系统会自动推荐并从各项目官网下载匹配的软件包。</p></div><div class="package-bundle-progress"><b>{{ packageBundleInstalledCount() }}/{{ packageBundleCatalogItems().length }}</b><small>推荐包已入库</small></div></header>
              <div class="package-quickstart-controls"><label>MySQL 版本与架构<span class="select-control"><select v-model="packageBundleID"><option v-for="bundle in (packageSettings.bundles || [])" :key="bundle.id" :value="bundle.id">{{ bundle.label }}{{ bundle.default ? '（默认）' : '' }}</option></select></span></label><button class="primary" type="button" :disabled="packageBundleFetching || packageBundleAllInstalled()" @click="fetchPackageBundle"><span aria-hidden="true">↓</span>{{ packageBundleAllInstalled() ? '推荐组合已就绪' : packageBundleFetching ? '正在从官网下载…' : packageBundleInstalledCount() ? '一键补齐推荐工具' : '一键下载 MySQL 与推荐工具' }}</button></div>
              <p v-if="selectedPackageBundle" class="package-compatibility-note"><b>版本推荐：</b>{{ selectedPackageBundle.compatibility_note }}</p>
              <div class="package-recommendation-grid"><article v-for="item in packageBundleCatalogItems()" :key="item.id" :class="{installed:packageCatalogInstalled(item)}"><div><span class="package-category">{{ packageCategoryLabel(item.category) }}</span><em>{{ packageCatalogInstalled(item) ? '✓ 已入库' : item.arch }}</em></div><b>{{ item.category === 'mysql' ? 'MySQL Server ' + item.version : item.description }}</b><small>{{ item.name }}</small></article></div>
              <details v-if="packageBundleCatalogItems(true).length" class="package-optional-tools"><summary>查看可选工具推荐</summary><div><span v-for="item in packageBundleCatalogItems(true)" :key="item.id">{{ packageCategoryLabel(item.category) }} {{ item.version }}{{ packageCatalogInstalled(item) ? ' · 已入库' : '' }}</span></div><small>ProxySQL/Mycat 属于路由或中间件替代方案，binlog2sql 为旧版脚本工具，因此不默认加入组合，可在下方按需下载。</small></details>
            </section>
            <section class="package-catalog"><header><div><b>官方软件源</b><small>从 Oracle MySQL 与项目官方 GitHub 发布源直接下载，入库后自动校验。</small></div><span>{{ (packageSettings.catalog || []).filter(packageCatalogInstalled).length }}/{{ (packageSettings.catalog || []).length }} 已入库</span></header><div class="package-catalog-grid"><article v-for="item in (packageSettings.catalog || [])" :key="item.id" :class="{installed:packageCatalogInstalled(item)}"><div><span class="package-category">{{ packageCategoryLabel(item.category) }}</span><em>{{ item.arch }}</em></div><b>{{ item.name }}</b><small>{{ item.description }}</small><footer><span>v{{ item.version }}</span><button type="button" :class="packageCatalogInstalled(item) ? 'secondary' : 'primary'" :disabled="packageCatalogInstalled(item) || packageFetching[item.id]" @click="fetchCatalogPackage(item)">{{ packageCatalogInstalled(item) ? '已入库' : packageFetching[item.id] ? '下载中…' : '下载入库' }}</button></footer></article></div></section>
            <section class="package-list"><div class="package-list-toolbar"><div><b>安装包列表</b><small>支持查询、校验、下载和删除已入库文件</small></div><form @submit.prevent="loadPackages"><input v-model.trim="packageKeyword" placeholder="搜索文件名或软件类型"><button class="secondary">查询</button></form></div><div class="package-table-wrap"><table><thead><tr><th>软件类型</th><th>文件 / 版本</th><th>格式 / 架构</th><th>文件大小</th><th>SHA-256</th><th>来源 / 更新时间</th><th>操作</th></tr></thead><tbody><tr v-for="item in packageItems" :key="item.category + item.name"><td><span class="package-category">{{ packageCategoryLabel(item.category) }}</span></td><td><b class="package-name">{{ item.name }}</b><small>版本 {{ item.version || '未识别' }}</small></td><td><b>{{ item.format }}</b><small>{{ item.arch }}</small></td><td>{{ packageSize(item.size) }}</td><td><code class="package-checksum" :title="item.sha256">{{ packageChecksum(item.sha256) }}</code></td><td><a v-if="item.source_url" :href="item.source_url" target="_blank" rel="noreferrer">官方来源 ↗</a><span v-else>本地上传</span><small>{{ date(item.updated_at) }}</small></td><td class="package-actions"><button v-if="!item.sha256" type="button" class="text-button" :disabled="packageFetching[item.category + '/' + item.name]" @click="verifyPackage(item)">{{ packageFetching[item.category + '/' + item.name] ? '校验中…' : '计算校验' }}</button><a class="text-button" :href="packageDownloadURL(item)" download>下载</a><button type="button" class="danger-link" @click="deletePackage(item)">删除</button></td></tr><tr v-if="!packageItems.length"><td colspan="7" class="empty">暂无安装包，可从官方软件源一键下载或手动上传。</td></tr></tbody></table></div></section>
          </section>
        </template>
        <template v-else-if="active === 'mysql'">
          <section :class="['mysql-page', { 'install-mode': mysqlView === 'install' }]">
            <section v-if="mysqlView === 'install'" class="mysql-install-page">
              <header class="mysql-install-page-head"><div><p>MySQL 管理 / 创建安装</p><h2><button type="button" class="mysql-install-back" aria-label="返回 MySQL 概览" @click="mysqlView='overview'">←</button>创建 MySQL 安装任务</h2><span>选择目标机器与安装版本，配置实例参数后由 Agent 异步执行完整安装流程。</span></div></header>
              <form class="mysql-install-form" @submit.prevent="createMySQLInstall">
                <section class="install-section"><h3>实例基础信息</h3><p>选择机器后立即检查 CPU 架构与 glibc，并锁定实际使用的本地安装包。</p><label>目标机器<select v-model="mysqlInstallForm.machine" required @change="mysqlInstallMachineChanged"><option value="">选择机器</option><option v-for="m in data.machines" :key="m.ID||m.id" :value="m.IP||m.ip">{{ m.Name||m.name }} · {{ m.IP||m.ip }}</option></select></label><div class="form-row"><label>目标架构<select v-model="mysqlInstallForm.architecture" @change="mysqlInstallArchitectureChanged"><option value="">自动匹配机器架构</option><option v-for="arch in mysqlInstallArchitectures" :key="arch" :value="arch">{{ arch }}</option></select></label><label>MySQL 版本<select v-model="mysqlInstallForm.version"><option value="">自动选择兼容版本</option><option v-for="pkg in mysqlInstallVersions" :key="pkg.version" :value="pkg.version">{{ pkg.version }} · {{ pkg.release_track }}</option></select><small v-if="mysqlInstallForm._selected_package">版本专属参数 {{ mysqlInstallForm._version_parameter_groups.reduce((total,group)=>total+(group.fields||[]).length,0) }} 项</small></label></div><div :class="['mysql-package-compatibility',mysqlInstallCompatibility.status]"><b>{{ mysqlInstallCompatibility.status==='compatible' ? '✓ 安装包兼容' : mysqlInstallCompatibility.status==='incompatible' ? '! 缺少兼容安装包' : mysqlInstallCompatibility.status==='checking' ? '… 正在检查' : '兼容性检查' }}</b><span>{{ mysqlInstallCompatibility.message }}</span><button v-if="mysqlInstallCompatibility.status==='incompatible'" type="button" class="text-button" @click="active='packages';mysqlView='overview'">前往安装包管理</button></div><p v-if="!data.mysqlPackages.length" class="form-note">安装包库中没有可选 MySQL 包，请先在安装包管理中上传。</p><div class="form-row"><label>MySQL 端口<input v-model.number="mysqlInstallForm.port" type="number" min="1" required></label><label>server_id<input v-model.number="mysqlInstallForm.server_id" type="number" min="1" required></label></div><div class="form-row"><label>MySQL 运行用户<input v-model="mysqlInstallForm.mysql_user" required></label><label>参数 Profile<input v-model="mysqlInstallForm.profile" required></label></div><label>root 密码<input v-model="mysqlInstallForm.root_password" type="password" required></label></section>
                <section class="install-section install-directory"><h3>目录与运行文件</h3><p>全部路径均可选；留空时按安装逻辑自动生成。</p><div class="directory-grid"><label>实例根目录<input v-model="mysqlInstallForm.instance_dir" placeholder="/data/3306"></label><label>数据目录<input v-model="mysqlInstallForm.data_dir" placeholder="实例根目录/data"></label><label>binlog 目录<input v-model="mysqlInstallForm.binlog_dir" placeholder="实例根目录/binlog"></label><label>redo 目录<input v-model="mysqlInstallForm.redo_dir" placeholder="实例根目录/redo"></label><label>undo 目录<input v-model="mysqlInstallForm.undo_dir" placeholder="实例根目录/undo"></label><label>tmp 目录<input v-model="mysqlInstallForm.tmp_dir" placeholder="实例根目录/tmp"></label><label>安装目录<input v-model="mysqlInstallForm.base_dir" placeholder="/usr/local/mysql"></label><label>my.cnf 路径<input v-model="mysqlInstallForm.my_cnf_path" placeholder="实例根目录/my.cnf"></label><label>Socket 文件<input v-model="mysqlInstallForm.socket_path" placeholder="数据目录/mysql.sock"></label><label>错误日志<input v-model="mysqlInstallForm.error_log" placeholder="数据目录/mysqld.log"></label><label>PID 文件<input v-model="mysqlInstallForm.pid_file" placeholder="数据目录/mysqld.pid"></label><label>字符集目录<input v-model="mysqlInstallForm.character_sets_dir" placeholder="安装目录/share/charsets"></label><label>插件目录<input v-model="mysqlInstallForm.plugin_dir" placeholder="安装目录/lib/plugin"></label></div></section>
                <details class="install-section install-runtime"><summary>MySQL 运行参数调整（可选）</summary><p>参数留空时沿用安装逻辑自动生成的配置；选择版本后，会追加该版本独有且经官方文档核验的参数。</p><p v-if="!mysqlInstallForm.version" class="version-parameter-note">当前为自动选版：通用参数可编辑；如需版本专属参数，请先明确选择版本。</p><article v-for="group in mysqlInstallForm._parameter_groups" :key="group.name" :class="['runtime-parameter-group', {'version-specific': mysqlInstallForm._version_parameter_groups.includes(group)}]"><header><b>{{ group.name }}</b><small>{{ group.fields.length }} 项</small></header><div class="runtime-parameter-grid"><label v-for="field in group.fields" :key="field.key">{{ field.label }}<select v-if="field.options" v-model="mysqlInstallForm.runtime_parameters[field.key]"><option value=""></option><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select><input v-else v-model.trim="mysqlInstallForm.runtime_parameters[field.key]" :placeholder="field.placeholder || ''"><small v-if="field.description || field.placeholder">{{ field.description || field.placeholder }}</small></label></div></article></details>
                <section class="install-section install-tools"><h3>可选工具与内存分配器</h3><p>工具默认不安装；启用后由 Manager 按最终 MySQL 版本、目标架构和 glibc 匹配本地制品并补齐依赖。</p><label class="install-option"><input v-model="mysqlInstallForm.install_pt_tools" type="checkbox" :disabled="!!mysqlInstallForm._selected_package && !mysqlInstallForm._selected_package.pt_tools_supported"><span><b>安装 PT 工具</b><small v-if="!mysqlInstallForm._selected_package">自动兼容模式：任务创建时确定 MySQL 版本并校验 PT 兼容性</small><small v-else-if="!mysqlInstallForm._selected_package.pt_tools_supported">MySQL {{ mysqlInstallForm._selected_package.version }} 不支持自动 PT 安装</small><small v-else>安装 Percona Toolkit 及 Perl 运行依赖，并验证核心 pt-* 命令</small></span></label><label class="install-option"><input v-model="mysqlInstallForm.install_xtrabackup" type="checkbox"><span><b>安装 Percona XtraBackup</b><small>匹配 MySQL 主版本并安装 libaio、libev、libgcrypt、zstd、lz4 等依赖；启用的备份账号自动补充 BACKUP_ADMIN</small></span></label><label class="install-allocator-field">内存分配器<select v-model="mysqlInstallForm.memory_allocator"><option value="system">系统默认（推荐）</option><option value="tcmalloc">tcmalloc（显式启用并验证）</option></select><small>tcmalloc 不等同于修复内存泄漏；启用后通过 systemd LD_PRELOAD 加载，并在启动后验证实际映射。</small></label></section>
                <section class="install-section install-accounts"><header class="install-section-title"><div><h3>初始化账号与权限</h3><p>预设账号默认显示；自定义账号仅在点击添加后出现，并可添加多个。</p></div><button type="button" class="secondary add-account-button" @click="addCustomMySQLAccount">＋ 增加自定义用户</button></header><article v-for="(account,index) in mysqlInstallForm.accounts" :key="account.role" :class="['mysql-account', {custom:isCustomMySQLAccount(account)}]"><header><div><b>{{ isCustomMySQLAccount(account) ? '自定义数据库用户' : ({monitor:'监控账号',mha:'MHA 管理账号',backup:'备份账号'})[account.role] }}</b><small>{{ isCustomMySQLAccount(account) ? '自定义名称、权限与访问白名单' : account.role }}</small></div><button v-if="isCustomMySQLAccount(account)" type="button" class="danger-link" @click="removeCustomMySQLAccount(index)">删除</button><label v-else class="switch"><input v-model="account.enabled" type="checkbox"><span>启用</span></label></header><div class="account-inputs"><label>用户名<input v-model.trim="account.username" :placeholder="isCustomMySQLAccount(account) ? '输入新用户名' : account.role" :required="isCustomMySQLAccount(account)"></label><label>密码<input v-model="account.password" type="password" :placeholder="isCustomMySQLAccount(account) ? '输入用户密码' : '默认 3306niubi'" :required="isCustomMySQLAccount(account)"></label><label>访问白名单（Host）<input v-model.trim="account.host" :placeholder="isCustomMySQLAccount(account) ? '例如 10.0.0.% 或 %' : '默认 %'" :required="isCustomMySQLAccount(account)"></label></div><div class="privilege-picker"><b>权限选择</b><div><label v-for="privilege in mysqlPrivilegeOptions" :key="privilege"><input v-model="account.privileges" type="checkbox" :value="privilege"> {{ privilege }}</label></div></div></article></section>
                <div class="mysql-install-actions"><button type="button" class="secondary" @click="mysqlView='overview'">取消</button><button class="primary" :disabled="mysqlInstallCompatibility.status==='checking'||mysqlInstallCompatibility.status==='incompatible'">创建安装任务</button></div>
              </form>
            </section>
            <nav v-if="mysqlView !== 'install'" class="mysql-tabs"><button :class="{active:mysqlView==='overview'}" @click="mysqlView='overview'">概览</button><button :class="{active:mysqlView==='instances'}" @click="mysqlView='instances'">实例 <span>{{ data.mysqlInstances.length }}</span></button><button @click="openMySQLInstall">创建安装</button><button :class="{active:mysqlView==='tasks'}" @click="mysqlView='tasks'">安装任务</button><button :class="{active:mysqlView==='accounts'}" @click="mysqlView='accounts'">预设账号</button></nav>
            <template v-if="mysqlView === 'overview'"><div class="mysql-summary"><article><small>已登记实例</small><b>{{ data.mysqlInstances.length }}</b></article><article><small>在线心跳</small><b>{{ data.mysqlInstances.filter(i => ['ok','success'].includes(state(i.HeartbeatStatus || i.heartbeat_status))).length }}</b></article><article><small>关联集群</small><b>{{ new Set(data.mysqlInstances.map(i => i.Cluster || i.cluster).filter(Boolean)).size }}</b></article></div><div class="mysql-shortcuts"><button @click="mysqlView='instances'"><b>实例管理</b><span>查看运行状态、配置和实例操作</span><i>→</i></button><button @click="mysqlView='tasks'"><b>安装任务</b><span>跟踪 MySQL 安装的执行进度</span><i>→</i></button></div></template>
            <section v-else-if="mysqlView === 'instances'" class="mysql-workspace"><div class="panel-head"><div><h3>MySQL 实例</h3><p>状态由 Manager 记录与 Agent 心跳共同提供。</p></div><button class="icon-button" @click="refresh">↻</button></div><table v-if="data.mysqlInstances.length"><thead><tr><th>实例</th><th>机器</th><th>集群</th><th>版本 / 端口</th><th>心跳</th><th>操作</th></tr></thead><tbody><tr v-for="item in data.mysqlInstances" :key="item.MachineID + ':' + item.Port"><td><b>{{ item.Name || item.name || 'MySQL' }}</b><small>{{ item.InstanceDir || item.instance_dir || '—' }}</small></td><td>{{ item.MachineName || item.machine_name || item.MachineIP || item.machine_ip }}</td><td>{{ item.Cluster || item.cluster || '未分配集群' }}</td><td>{{ item.Version || item.version || '—' }} · {{ item.Port || item.port }}</td><td><span :class="['status', state(item.HeartbeatStatus || item.heartbeat_status)]">{{ item.HeartbeatStatus || item.heartbeat_status || '—' }}</span></td><td class="mysql-ops"><button class="text-button" @click="showMySQLMachine(item)">数据</button><button class="danger-link" @click="uninstallMySQL(item)">卸载</button><button class="text-button" @click="forgetMySQL(item)">遗忘</button></td></tr></tbody></table><div v-else class="mysql-empty"><b>暂未发现 MySQL 实例</b><small>选择已纳管且 Agent 在线的机器，创建真实安装任务后将在此展示实例与心跳。</small><button class="primary" @click="openMySQLInstall">创建安装任务</button></div></section>
            <section v-else-if="mysqlView === 'tasks'" class="panel mysql-task-panel"><div class="panel-head"><div><h3>MySQL 安装任务</h3><p>仅展示 MySQL 安装任务；全部任务可在任务中心查看。</p></div><button class="text-button" @click="active='tasks'">前往任务中心 →</button></div><TaskTable :items="data.tasks.filter(item => String(item.Type || item.type || '').toLowerCase().includes('mysql'))" :machines="data.machines" :state="state" :date="date" @select="openTaskDetail" /></section>
            <section v-else-if="mysqlView === 'accounts'" class="panel mysql-preset-panel"><div class="panel-head"><div><h3>预设账号</h3><p>保存后，新建 MySQL 安装任务会自动带入账号、连接范围与权限选择。</p></div><button class="primary" @click="saveMySQLAccountPresets">保存预设</button></div><div class="mysql-preset-list"><article v-for="account in data.accountPresets" :key="account.role"><header><div><b>{{ ({monitor:'监控账号',mha:'MHA 管理账号',backup:'备份账号'})[account.role] }}</b><small>{{ account.role }}</small></div><label class="switch"><input v-model="account.enabled" type="checkbox"><span>启用</span></label></header><p>{{ account.role === 'monitor' ? '用于监控和健康检查。' : account.role === 'mha' ? '用于 MHA 拓扑管理和切换。' : '用于备份任务。' }}</p><div class="form-row"><label>账号名称<input v-model="account.username" required></label><label>密码<input v-model="account.password" type="password" required></label></div><label>允许来源 Host<input v-model="account.host" required></label><div class="privilege-picker"><b>授权权限</b><small>仅允许选择 GMHA 支持的权限；保存后将生成对应 GRANT 语句。</small><div><label v-for="privilege in mysqlPrivilegeOptions" :key="privilege"><input v-model="account.privileges" type="checkbox" :value="privilege"> {{ privilege }}</label></div></div></article></div></section>
          </section>
        </template><template v-else><section class="coming"><div class="coming-icon">↯</div><p>模块已规划</p><h2>{{ current.label }}</h2></section></template>
      </section>
	  <div v-if="showOnboard" class="modal-mask onboard-machine-mask" @click.self="showOnboard = false"><form class="modal onboard-machine-modal" @submit.prevent="onboard"><div class="modal-head"><div><p>资源管理</p><h2>纳管机器</h2></div><button type="button" @click="showOnboard = false">×</button></div><label>机器名称<input v-model="form.name" required placeholder="mysql-prod-01"></label><label>IP 地址<input v-model="form.ip" required placeholder="10.0.0.11"></label><label>已有 SSH 凭证<select v-model="form.credential_id" @change="applyCredential"><option value="">不使用已有凭证（直接输入密码）</option><option v-for="item in data.credentials" :key="item.id" :value="item.id">{{ item.name }} · {{ item.ssh_user }} · {{ item.type === 'private_key' ? '私钥文件' : '密码' }}</option></select></label><div class="form-row"><label>SSH 端口<input v-model.number="form.ssh_port" type="number" required></label><label>SSH 用户<input v-model="form.ssh_user" required :readonly="!!form.credential_id"></label></div><label v-if="!form.credential_id">SSH 密码<input v-model="form.ssh_password" type="password" required autocomplete="new-password"></label><p class="form-note" v-if="form.credential_id">将使用所选凭证完成连接与纳管；密码、私钥和口令均不会在此页面显示。</p><p class="form-note" v-else>直接输入的密码仅用于本次纳管。建议常用凭证先保存到凭证库。</p><section class="onboard-preserve-options"><b>发现已有组件时</b><label><input v-model="form.preserve_agent" type="checkbox"><span><strong>保留现有 Agent</strong><small>优先保留并重新登记；若旧 Agent 无法启动，自动使用 Manager 当前版本修复，不影响 MySQL。</small></span></label><label><input v-model="form.preserve_mysql" type="checkbox"><span><strong>保留现有 MySQL 实例</strong><small>不停止服务、不删除数据；从现有 Agent 配置恢复实例记录。</small></span></label></section><div class="modal-actions"><button type="button" class="secondary" @click="showOnboard = false">取消</button><button class="primary">开始纳管</button></div></form></div>
      <div v-if="showOnboardFlow" class="modal-mask"><section class="modal flow-modal"><div class="modal-head"><div><p>纳管流程</p><h2>{{ onboardingResult ? '纳管结果' : '正在接入机器' }}</h2></div></div><div class="flow-step" v-for="item in onboardingFlow" :key="item.title"><i :class="item.state">{{ item.state === 'success' ? '✓' : item.state === 'error' ? '!' : item.state === 'running' ? '…' : '○' }}</i><div class="flow-content"><b>{{ item.title }}</b><span>{{ item.state === 'error' ? errorSummary(item.detail) : item.detail }}</span><div v-if="item.details" class="agent-details"><div v-for="detail in item.details" :key="detail.title" :class="['agent-detail', detail.state]"><i>{{ detail.state === 'success' ? '✓' : detail.state === 'error' ? '!' : detail.state === 'running' ? '…' : '○' }}</i><span>{{ detail.title }}</span><div v-if="detail.error" class="flow-error"><small>{{ errorSummary(detail.errorSummary || detail.error) }}</small></div></div></div></div></div><div v-if="onboardingFlow.some(item => item.state === 'error')" class="flow-warning">检测到已有组件时，可选择保留并重新纳管；基础环境错误仍需先处理。</div><section v-if="canSkipPrecheck" class="flow-preserve-options"><label v-if="onboardingDetected.agent"><input v-model="form.preserve_agent" type="checkbox"><span><b>保留现有 Agent</b><small>重新登记并重启，不卸载、不覆盖。</small></span></label><label v-if="onboardingDetected.mysql"><input v-model="form.preserve_mysql" type="checkbox"><span><b>保留现有 MySQL</b><small>恢复实例记录，不停止服务、不删除数据。</small></span></label></section><p v-if="onboardingFlow.some(item => item.state === 'error')" class="flow-log-hint">完整执行日志请在任务中心对应任务详情中查看。</p><div class="modal-actions"><button v-if="canSkipPrecheck" class="danger-button" @click="cleanupTarget">清理目标机器</button><button v-if="canSkipPrecheck" class="secondary" :disabled="(onboardingDetected.agent && !form.preserve_agent) || (onboardingDetected.mysql && !form.preserve_mysql)" @click="onboard(true)">保留所选组件并重新纳管</button><button class="primary" @click="showOnboardFlow = false">完成</button></div></section></div>
      <div v-if="showMachineDetail && selectedMachine" class="modal-mask" @click.self="showMachineDetail = false"><form class="modal machine-detail-modal" @submit.prevent="saveMachine"><div class="modal-head"><div><p>机器详情</p><h2>{{ selectedMachine.Name || selectedMachine.name }}</h2></div><button type="button" @click="showMachineDetail = false">×</button></div><div class="machine-detail-body"><div class="machine-profile-grid"><p><small>机器状态</small><span :class="['status', machineStatus(selectedMachine).code]">{{ machineStatus(selectedMachine).label }}</span></p><p><small>所属集群</small><b>{{ machineCluster(selectedMachine) }}</b></p><p><small>关联凭证</small><b>{{ selectedMachine.CredentialID || selectedMachine.credential_id || '未分配' }}</b></p><p><small>Agent 安装目录</small><b>{{ machineAgentInstallDir(selectedMachine) }}</b></p><p><small>创建时间</small><b>{{ date(selectedMachine.CreatedAt || selectedMachine.created_at) }}</b></p><p><small>更新时间</small><b>{{ date(selectedMachine.UpdatedAt || selectedMachine.updated_at) }}</b></p></div><label>机器名称<input v-model="selectedMachine.Name" required></label><label>IP 地址<input v-model="selectedMachine.IP" required></label><div class="form-row"><label>SSH 端口<input v-model.number="selectedMachine.SSHPort" type="number" required></label><label>SSH 用户<input v-model="selectedMachine.SSHUser" required></label></div><div class="machine-tools"><label>分配至集群<select v-model="selectedMachineCluster"><option value="">选择集群</option><option v-for="cluster in data.clusters" :key="cluster.Name || cluster.name" :value="cluster.Name || cluster.name">{{ cluster.Name || cluster.name }}</option></select></label><button type="button" class="secondary" :disabled="!selectedMachineCluster" @click="assignMachineCluster">分配集群</button></div><section class="machine-data"><div><b>静态信息</b><button type="button" class="text-button" @click="collectMachineStaticInfo">重新采集</button></div><pre v-if="machineStaticInfo">{{ JSON.stringify(machineStaticInfo, null, 2) }}</pre><p v-else>尚未采集静态信息。</p></section><section class="machine-data"><div><b>动态指标</b><button type="button" class="text-button" @click="loadMachineDynamicInfo">查看动态数据</button></div><pre v-if="machineDynamicInfo">{{ JSON.stringify(machineDynamicInfo, null, 2) }}</pre><p v-else>点击按钮从 Agent 获取最新动态指标。</p></section><p v-if="machineInfoError" class="machine-info-error">{{ errorSummary(machineInfoError) }}</p><p class="form-note">机器 ID：{{ selectedMachine.ID || selectedMachine.id }}</p><div v-if="machineLastError(selectedMachine)" class="machine-last-error"><b>最后错误</b><p>{{ errorSummary(machineLastError(selectedMachine)) }}</p><small>完整日志请到任务中心对应任务详情查看。</small></div><p v-else class="form-note">最后错误：无</p></div><div class="modal-actions"><button type="button" class="danger-button" @click="deleteMachine">删除机器</button><button type="button" class="secondary" @click="showMachineDetail = false">取消</button><button class="primary">保存修改</button></div></form></div>
      <div v-if="showAgentDetail && selectedAgent && !agentActionDialog" class="modal-mask" @click.self="showAgentDetail = false"><section class="modal agent-detail-modal"><div class="modal-head"><div><p>Agent 管理</p><h2>{{ selectedAgent.Name || selectedAgent.name || selectedAgent.IP || selectedAgent.ip }}</h2></div><button type="button" @click="showAgentDetail = false">×</button></div><div class="agent-detail-body"><div class="agent-info-grid"><p><small>IP 地址</small><b>{{ selectedAgent.IP || selectedAgent.ip }}</b></p><p><small>所属集群</small><b>{{ machineCluster(selectedAgent) }}</b></p><p><small>安装目录</small><b>{{ selectedAgent.InstallDir || selectedAgent.install_dir || '—' }}</b></p><p><small>Agent CPU</small><b>{{ agentCPU(selectedAgent) }}</b></p><p><small>Agent RSS 内存</small><b>{{ agentMemory(selectedAgent) }}</b></p><p><small>资源采集时间</small><b>{{ agentResource(selectedAgent).collectedAt ? date(agentResource(selectedAgent).collectedAt) : '等待采集' }}</b></p><p><small>心跳状态</small><b>{{ selectedAgent.HeartbeatState || selectedAgent.heartbeat_state || 'INIT' }}</b></p><p><small>整体健康</small><b>{{ selectedAgent.OverallHealth || selectedAgent.overall_health || '—' }}</b></p><p><small>最近心跳</small><b>{{ selectedAgent.LastHeartbeatAt || selectedAgent.last_heartbeat_at || '暂无' }}</b></p></div><p v-if="agentResource(selectedAgent).needsUpgrade" class="agent-resource-upgrade-note">当前机器上的 Agent 版本不包含进程资源采集器，请点击“升级 Agent”后查看 CPU 与 RSS 内存。</p><div v-if="selectedAgent.Checks?.length" class="agent-check-list"><b>健康检查</b><p v-for="check in selectedAgent.Checks" :key="check.Name"><span :class="['status', state(check.Status)]">{{ check.Status }}</span>{{ check.Name }}<small>{{ errorSummary(check.Detail, 140) }}</small></p></div><div v-if="selectedAgent.LastError || selectedAgent.last_error" class="machine-last-error"><b>最近错误</b><p>{{ errorSummary(selectedAgent.LastError || selectedAgent.last_error) }}</p><small>完整日志请到任务中心对应任务详情查看。</small></div></div><div class="modal-actions"><button class="danger-button" @click="uninstallAgent(selectedAgent)">卸载 Agent</button><button class="secondary" @click="repairMySQLAgentConfig(selectedAgent)">修复 MySQL 采集</button><button class="secondary" @click="upgradeAgent(selectedAgent)">升级 Agent</button><button class="primary" @click="recover(selectedAgent.IP || selectedAgent.ip)">手动拉起</button></div></section></div>
      <div v-if="showCredential" class="modal-mask" @click.self="showCredential = false"><form class="modal" @submit.prevent="createCredential"><div class="modal-head"><div><p>凭证库</p><h2>添加 SSH 凭证</h2></div><button type="button" :disabled="credentialSubmitting" @click="showCredential = false">×</button></div><label>凭证名称<input v-model="credentialForm.name" required placeholder="生产环境 root 密钥"></label><label>SSH 用户<input v-model="credentialForm.ssh_user" required></label><label>认证方式<select v-model="credentialForm.type"><option value="password">密码</option><option value="private_key">SSH 私钥文件</option></select></label><label v-if="credentialForm.type === 'password'">密码<input v-model="credentialForm.ssh_password" type="password" required></label><template v-else><label>私钥文件<input type="file" accept=".pem,.key,.ppk,*" required @change="loadKeyFile"></label><label>私钥口令（可选）<input v-model="credentialForm.passphrase" type="password"></label></template><p class="form-note">凭证内容仅发送至本机 Manager 数据库，列表中不会返回或显示密码、私钥和口令。</p><div class="modal-actions"><button type="button" class="secondary" :disabled="credentialSubmitting" @click="showCredential = false">取消</button><button class="primary" :disabled="credentialSubmitting">{{ credentialSubmitting ? '正在保存…' : '保存凭证' }}</button></div></form></div>
      <div v-if="showAssign" class="modal-mask" @click.self="showAssign = false"><form class="modal" @submit.prevent="assignCredential"><div class="modal-head"><div><p>凭证分配</p><h2>选择机器</h2></div><button type="button" @click="showAssign = false">×</button></div><p class="form-note">可选择一台或多台机器。分配会同步该凭证的 SSH 用户，不会建立远程连接。</p><label v-for="item in data.machines" :key="item.ID || item.id" class="machine-choice"><input type="checkbox" :value="item.ID || item.id" v-model="assignedMachineIDs"><span><b>{{ item.Name || item.name }}</b><small>{{ item.IP || item.ip }}</small></span></label><p v-if="!data.machines.length" class="form-note">请先完成机器纳管。</p><div class="modal-actions"><button type="button" class="secondary" @click="showAssign = false">取消</button><button class="primary" :disabled="!assignedMachineIDs.length">分配凭证</button></div></form></div>
      <div v-if="showQuickClusterAssign && selectedMachine" class="modal-mask" @click.self="showQuickClusterAssign=false"><form class="modal quick-cluster-modal" @submit.prevent="quickAssignMachineCluster"><div class="modal-head"><div><p>集群分配</p><h2>分配机器到集群</h2></div><button type="button" @click="showQuickClusterAssign=false">×</button></div><p class="form-note"><b>{{ selectedMachine.Name || selectedMachine.name }}</b> · {{ selectedMachine.IP || selectedMachine.ip }}</p><label>目标集群<select v-model="selectedMachineCluster" required><option value="">请选择集群</option><option v-for="cluster in data.clusters" :key="cluster.Name || cluster.name" :value="cluster.Name || cluster.name">{{ cluster.Name || cluster.name }}</option></select></label><p class="form-note">分配后将按既有流程同步机器的集群归属。</p><div class="modal-actions"><button type="button" class="secondary" @click="showQuickClusterAssign=false">取消</button><button class="primary" :disabled="!selectedMachineCluster">确认分配</button></div></form></div>
      <div v-if="showMachineDetail && selectedMachine" class="modal-mask machine-detail-dashboard-mask"><form class="modal machine-detail-dashboard" @submit.prevent="saveMachine"><div class="modal-head"><div><p>机器详情</p><h2>{{ selectedMachine.Name || selectedMachine.name }}</h2></div><button type="button" @click="showMachineDetail = false">×</button></div><div class="dashboard-body"><section class="machine-profile-grid"><p><small>机器状态</small><span :class="['status', machineStatus(selectedMachine).code]">{{ machineStatus(selectedMachine).label }}</span></p><p><small>所属集群</small><b>{{ machineCluster(selectedMachine) }}</b></p><p><small>关联凭证</small><b>{{ selectedMachine.CredentialID || selectedMachine.credential_id || '未分配' }}</b></p><p><small>Agent 安装目录</small><b>{{ machineAgentInstallDir(selectedMachine) }}</b></p></section><section class="machine-data"><div><span><b>静态资产</b><small>主机与 MySQL 配置快照</small></span><button type="button" class="text-button" @click="collectMachineStaticInfo">重新采集</button></div><dl v-if="machineStaticInfo" class="asset-grid"><template v-for="row in staticRows(machineStaticInfo)" :key="row[0]"><dt>{{ row[0] }}</dt><dd>{{ row[1] }}</dd></template></dl><p v-else>尚未采集静态信息。</p></section><section class="machine-data"><div><span><b>动态指标</b><small>Agent 最近一次上报的数据</small></span><button type="button" class="text-button" @click="loadMachineDynamicInfo">刷新指标</button></div><div v-if="machineDynamicInfo" class="metric-summary"><span>心跳：{{ machineDynamicInfo.HeartbeatState || machineDynamicInfo.heartbeat_state || '—' }}</span><span>{{ dynamicMetrics(machineDynamicInfo).length }} 项指标</span></div><table v-if="machineDynamicInfo && dynamicMetrics(machineDynamicInfo).length" class="metric-table"><thead><tr><th>指标</th><th>分类</th><th>值</th><th>状态</th><th>采集时间</th></tr></thead><tbody><tr v-for="metric in dynamicMetrics(machineDynamicInfo)" :key="metric.Name || metric.name"><td>{{ metric.Name || metric.name }}</td><td>{{ metric.Category || metric.category || '主机' }}</td><td class="metric-value">{{ metricValue(metric) }}</td><td><span :class="['status', (metric.Success ?? metric.success) ? 'success' : 'error']">{{ (metric.Success ?? metric.success) ? '成功' : '失败' }}</span></td><td>{{ date(metric.CollectedAt || metric.collected_at) }}</td></tr></tbody></table><p v-else-if="machineDynamicInfo">当前 Agent 未上报动态指标。</p><p v-else>点击“刷新指标”从 Agent 获取最新动态数据。</p></section><section class="edit-section"><b>连接与归属</b><label>机器名称<input v-model="selectedMachine.Name" required></label><label>IP 地址<input v-model="selectedMachine.IP" required></label><div class="form-row"><label>SSH 端口<input v-model.number="selectedMachine.SSHPort" type="number" required></label><label>SSH 用户<input v-model="selectedMachine.SSHUser" required></label></div><div class="machine-tools"><label>分配至集群<select v-model="selectedMachineCluster"><option value="">选择集群</option><option v-for="cluster in data.clusters" :key="cluster.Name || cluster.name" :value="cluster.Name || cluster.name">{{ cluster.Name || cluster.name }}</option></select></label><button type="button" class="secondary" :disabled="!selectedMachineCluster" @click="assignMachineCluster">分配集群</button></div></section><p v-if="machineInfoError" class="machine-info-error">{{ errorSummary(machineInfoError) }}</p></div><div class="modal-actions"><button type="button" class="danger-button" @click="deleteMachine">删除机器</button><button type="button" class="secondary" @click="showMachineDetail = false">取消</button><button class="primary">保存修改</button></div></form></div>
      <div v-if="showMySQLInstall" class="modal-mask" @click.self="showMySQLInstall=false"><form class="modal mysql-install-modal" @submit.prevent="createMySQLInstall"><div class="modal-head"><div><p>MySQL 管理</p><h2>创建安装任务</h2></div><button type="button" @click="showMySQLInstall=false">×</button></div><p class="form-note">安装版本来自 Manager 当前 MySQL 安装包库；提交时仍会校验目标机器的 Linux 架构与 glibc 兼容性。</p><section class="install-section"><h3>实例基础信息</h3><label>目标机器<select v-model="mysqlInstallForm.machine" required><option value="">选择机器</option><option v-for="m in data.machines" :key="m.ID||m.id" :value="m.IP||m.ip">{{ m.Name||m.name }} · {{ m.IP||m.ip }}</option></select></label><label>MySQL 安装版本<select v-model="mysqlInstallForm.package_name"><option value="">自动选择兼容版本</option><option v-for="pkg in data.mysqlPackages" :key="pkg.file_name" :value="pkg.file_name">{{ pkg.version }} · {{ pkg.arch }} · glibc {{ pkg.glibc_version }}</option></select></label><p v-if="!data.mysqlPackages.length" class="form-note">安装包库中没有可选 MySQL 包，请先在安装包管理中上传。</p><div class="form-row"><label>MySQL 端口<input v-model.number="mysqlInstallForm.port" type="number" min="1" required></label><label>server_id<input v-model.number="mysqlInstallForm.server_id" type="number" min="1" required></label></div><div class="form-row"><label>MySQL 运行用户<input v-model="mysqlInstallForm.mysql_user" required></label><label>参数 Profile<input v-model="mysqlInstallForm.profile" required></label></div><label>root 密码<input v-model="mysqlInstallForm.root_password" type="password" required></label></section><details class="install-section" open><summary>目录与运行文件</summary><p>所有路径均可选；留空时使用 CLI 的默认规则。</p><label>实例根目录<input v-model="mysqlInstallForm.instance_dir" placeholder="/data/3306"></label><div class="form-row"><label>数据目录<input v-model="mysqlInstallForm.data_dir" placeholder="实例根目录/data"></label><label>binlog 目录<input v-model="mysqlInstallForm.binlog_dir" placeholder="实例根目录/binlog"></label></div><div class="form-row"><label>redo 目录<input v-model="mysqlInstallForm.redo_dir" placeholder="实例根目录/redo"></label><label>undo 目录<input v-model="mysqlInstallForm.undo_dir" placeholder="实例根目录/undo"></label></div><div class="form-row"><label>tmp 目录<input v-model="mysqlInstallForm.tmp_dir" placeholder="实例根目录/tmp"></label><label>安装目录<input v-model="mysqlInstallForm.base_dir" placeholder="/usr/local/mysql"></label></div><label>my.cnf 路径<input v-model="mysqlInstallForm.my_cnf_path" placeholder="实例根目录/my.cnf"></label><div class="form-row"><label>Socket 文件<input v-model="mysqlInstallForm.socket_path" placeholder="数据目录/mysql.sock"></label><label>错误日志<input v-model="mysqlInstallForm.error_log" placeholder="数据目录/mysqld.log"></label></div><div class="form-row"><label>PID 文件<input v-model="mysqlInstallForm.pid_file" placeholder="数据目录/mysqld.pid"></label><label>字符集目录<input v-model="mysqlInstallForm.character_sets_dir" placeholder="安装目录/share/charsets"></label></div><label>插件目录<input v-model="mysqlInstallForm.plugin_dir" placeholder="安装目录/lib/plugin"></label></details><details class="install-section"><summary>初始化账号与权限</summary><p>每个账号的权限可单独选择；保存的“预设账号”会自动带入此处。</p><article v-for="account in mysqlInstallForm.accounts" :key="account.role" class="mysql-account"><div><b>{{ account.role }}</b><label class="switch"><input v-model="account.enabled" type="checkbox"><span>启用</span></label></div><div class="form-row"><label>用户名<input v-model="account.username" :placeholder="account.role"></label><label>密码<input v-model="account.password" type="password" placeholder="默认 3306niubi"></label></div><label>允许来源 Host<input v-model="account.host" placeholder="默认 %"></label><div class="privilege-picker"><b>权限选择</b><label v-for="privilege in mysqlPrivilegeOptions" :key="privilege"><input v-model="account.privileges" type="checkbox" :value="privilege"> {{ privilege }}</label></div></article></details><div class="modal-actions"><button type="button" class="secondary" @click="showMySQLInstall=false">取消</button><button class="primary">创建安装任务</button></div></form></div>
      <div v-if="showMySQLTask && mysqlTaskDetail" class="modal-mask"><section class="modal flow-modal"><div class="modal-head"><div><p>MySQL 安装流程</p><h2>{{ mysqlTaskDetail.MachineName || mysqlTaskDetail.machine_name || mysqlInstallForm.machine }}</h2></div><button type="button" @click="showMySQLTask=false">×</button></div><p class="form-note">任务：{{ mysqlTaskDetail.Task?.ID || mysqlTaskDetail.task?.id }} · 状态：{{ mysqlTaskDetail.Task?.Status || mysqlTaskDetail.task?.status }}</p><div class="flow-step" v-for="step in (mysqlTaskDetail.Steps || mysqlTaskDetail.steps || [])" :key="step.ID || step.id"><i :class="state(step.Status || step.status)">{{ ['success','completed'].includes(state(step.Status || step.status)) ? '✓' : ['failed','error'].includes(state(step.Status || step.status)) ? '!' : '…' }}</i><div class="flow-content"><b>{{ step.StepName || step.step_name }}</b><span>{{ step.Status || step.status }}</span><small v-if="step.Error || step.error" class="ui-error-summary">{{ errorSummary(step.Error || step.error) }}</small></div></div><div class="modal-actions"><button class="secondary" @click="refreshMySQLTask">刷新进度</button><button class="primary" @click="showMySQLTask=false">完成</button></div></section></div>
      <section v-if="active === 'clusters'" :class="['cluster-dashboard', { 'cluster-overview-mode': data.clusterSection==='overview', 'cluster-machine-mode': data.clusterSection==='machines', 'cluster-instance-mode': data.clusterSection==='instances' }]">
        <aside v-if="selectedClusterDetail" class="cluster-context-nav">
          <div class="context-cluster"><b>{{ selectedClusterDetail.Name || selectedClusterDetail.name }}</b><small>集群 ID：{{ selectedClusterDetail.ID || selectedClusterDetail.id || '—' }}</small><span><i></i>正常运行</span></div>
          <button :class="{active: (data.clusterSection || 'overview')==='overview'}" type="button" @click="stopClusterTopologyAutoRefresh(); data.clusterSection='overview'; refresh()">概览</button>
          <button :class="{active: data.clusterSection==='machines'}" type="button" @click="stopClusterTopologyAutoRefresh(); data.clusterSection='machines'; refreshClusterTopology({includeMachines:true})">机器管理</button>
          <button :class="{active: data.clusterSection==='instances'}" type="button" @click="stopClusterTopologyAutoRefresh(); data.clusterSection='instances'; refreshClusterTopology({includeMachines:true})">实例管理</button>
          <button :class="{active: data.clusterSection==='architecture'}" type="button" @click="openArchitectureAdjustment">架构调整</button>
          <button :class="{active: data.clusterSection==='backup'}" type="button" @click="openClusterBackup">备份恢复</button>
          <div class="context-danger"><button type="button" @click="cleanupCluster(selectedClusterDetail)">一键清理</button><button type="button" @click="deleteCluster(selectedClusterDetail)">删除集群</button></div>
        </aside>
        <template v-if="selectedClusterDetail">
          <div class="cluster-detail-head"><button class="back-button" @click="closeClusterDetail">← 返回集群列表</button></div>
          <div class="cluster-hero cluster-detail-hero"><div class="cluster-detail-title"><p>CLUSTER DETAIL</p><h2>{{ selectedClusterDetail.Name || selectedClusterDetail.name }}</h2><span>{{ selectedClusterDetail.Description || selectedClusterDetail.description || '暂无集群描述' }}</span></div></div>
          <div class="cluster-summary cluster-detail-summary"><article><small>集群机器</small><b>{{ clusterMachineCount(selectedClusterDetail) }}</b></article><article><small>MySQL 实例</small><b>{{ data.mysqlInstances.filter(instance => (instance.Cluster || instance.cluster) === (selectedClusterDetail.Name || selectedClusterDetail.name)).length }}</b></article><article><small>异常实例</small><b>{{ data.mysqlInstances.filter(instance => (instance.Cluster || instance.cluster) === (selectedClusterDetail.Name || selectedClusterDetail.name) && ['fail','failed','error','offline'].includes(state(instance.HeartbeatStatus || instance.heartbeat_status || instance.Status || instance.status))).length }}</b></article><article><small>创建时间</small><strong>{{ date(selectedClusterDetail.CreatedAt || selectedClusterDetail.created_at) }}</strong></article></div>
          <InstanceManagement v-if="data.clusterSection==='instances'" :cluster="selectedClusterDetail" :machines="clusterMachineItems" :instances="data.mysqlInstances" :packages="data.mysqlPackages" :topology="clusterTopology" :account-presets="data.accountPresets" :initial-view="data.instanceView" @view-change="data.instanceView=$event" @close="data.clusterSection='overview'; data.instanceView='instances'" @refresh="refresh(); refreshClusterTopology({includeMachines:true})" @open-task="openTaskDetail" />
          <section v-else-if="data.clusterSection==='vip'" class="vip-management-workspace">
            <div class="vip-page-head"><div><p>CLUSTER ACCESS ENDPOINT</p><h3>VIP 管理</h3><span>通过在线 Agent 执行绑定、撤销、自动宣告与单持有者复检，不使用 SSH 或 MySQL 执行凭证。</span></div><button type="button" class="secondary" :disabled="vipBusy" @click="openVIPManagement(true)">{{ vipBusy ? '正在检测…' : '刷新网卡与当前状态' }}</button></div>
            <form class="vip-create-panel" @submit.prevent="saveVIPConfig"><header><div><b>添加业务 VIP</b><small>宣告方式由系统根据集群网络自动选择，不需要手工配置。</small></div><span>自动宣告</span></header><div class="vip-form-grid"><label>VIP 名称<input v-model.trim="vipForm.vip_name" required placeholder="业务 VIP"></label><label>VIP 地址<input v-model.trim="vipForm.vip_address" required placeholder="例如 192.168.31.100"></label><label>网络前缀<input v-model.number="vipForm.vip_prefix" type="number" min="1" max="32" required></label><label>当前持有机器<select v-model="vipForm.target_machine_id" required @change="vipForm.default_interface=vipInterfaceOptions[0]?.name||''"><option value="">请选择集群机器</option><option v-for="machine in clusterMachineItems" :key="machine.ID||machine.id" :value="machine.ID||machine.id">{{ machine.Name||machine.name }} · {{ machine.IP||machine.ip }}</option></select></label><label class="vip-interface-select">使用网卡<select v-model="vipForm.default_interface" required><option value="">请选择业务网卡</option><option v-for="item in vipInterfaceOptions" :key="item.name" :value="item.name">{{ item.name }} · {{ item.ip }}</option></select><small v-if="!vipInterfaceOptions.length">未读取到可用 IPv4 网卡，请点击“刷新网卡与当前状态”。</small><small v-else>系统将绑定到该网卡，并自动发送免费 ARP；三层集群自动复用 BGP 策略。</small></label></div><footer><ol><li><i>1</i>撤销同名旧 VIP</li><li><i>2</i>绑定目标网卡</li><li><i>3</i>自动网络宣告</li><li><i>4</i>全节点复检</li></ol><button class="primary" :disabled="vipBusy || !vipForm.vip_address || !vipForm.target_machine_id || !vipForm.default_interface">{{ vipBusy ? '正在执行…' : '添加并验证 VIP' }}</button></footer></form>
            <section class="vip-current-panel"><header><div><b>当前 VIP</b><small>展示 Manager 配置与 Agent 实机扫描的合并结果。</small></div><span>{{ vipConfigs.length }} 个</span></header><div v-if="vipConfigs.length" class="vip-card-grid"><article v-for="item in vipConfigs" :key="item.vip_address" :class="['vip-status-card',String(vipStateFor(item).vip_status||'UNKNOWN').toLowerCase()]"><header><span>VIP</span><em :class="['status',vipStateFor(item).vip_status==='BOUND'?'success':vipStateFor(item).vip_status==='CONFLICT'?'error':'offline']">{{ vipStatusLabel(vipStateFor(item).vip_status) }}</em></header><h4>{{ item.vip_address }}/{{ item.vip_prefix }}</h4><p>{{ item.vip_name || '业务 VIP' }}</p><dl><div><dt>当前持有者</dt><dd>{{ vipMachineName(vipStateFor(item).current_holder_machine_id) }}</dd></div><div><dt>使用网卡</dt><dd>{{ vipStateFor(item).current_interface || item.default_interface || '待检测' }}</dd></div><div><dt>宣告策略</dt><dd>系统自动 · {{ item.vip_route_mode==='BGP' ? 'BGP' : 'ARP' }}</dd></div><div><dt>最近检测</dt><dd>{{ vipStateFor(item).updated_at ? date(vipStateFor(item).updated_at) : '尚未检测' }}</dd></div></dl><p v-if="vipStateFor(item).last_error" class="vip-card-error">{{ errorSummary(vipStateFor(item).last_error,180) }}</p><footer><button type="button" class="danger-link" :disabled="vipBusy" @click="deleteVIPConfig(item)">撤销并删除</button></footer></article></div><div v-else class="vip-empty"><span>◇</span><b>尚未配置业务 VIP</b><small>在上方选择持有机器与业务网卡，系统将自动完成绑定和宣告。</small></div></section>
          </section>
          <section v-else-if="data.clusterSection==='architecture'" class="architecture-workspace">
            <div class="architecture-page-head"><div><p>TOPOLOGY ORCHESTRATION</p><h3>架构与 VIP 调整</h3><span>在同一工作区维护复制拓扑、VIP 持有者和安全漂移策略。</span></div><div class="architecture-head-actions"><button type="button" class="secondary" @click="openArchitectureAdjustment">重新读取</button><details class="architecture-add-menu"><summary>＋ 添加实例</summary><div><button v-for="item in architectureAvailableInstances()" :key="(item.MachineID||item.machine_id)+':'+(item.Port||item.port)" type="button" @click="addArchitectureInstance(item)"><b>{{ architectureNodeName(item.MachineID||item.machine_id) }}</b><small>{{ item.MachineIP||item.machine_ip }}:{{ item.Port||item.port }}</small></button><button v-if="!architectureAvailableInstances().length" type="button" @click="data.clusterSection='instances'"><b>创建新实例</b><small>当前没有可直接加入的已安装实例</small></button></div></details></div></div>
            <div v-if="architectureRoleChangeFeedback" class="topology-draft-feedback" role="status" aria-live="polite"><span>✓</span><div><b>目标架构草稿已更新</b><p>{{ architectureRoleChangeFeedback }}</p></div><div class="topology-draft-actions"><button v-if="architectureDraftHistory.length" type="button" @click="undoArchitectureDraft">↶ 撤销上一步</button><button v-if="architectureTopologyHasChanges() || architectureForm.move_vip" type="button" @click="resetArchitectureDraft">恢复线上架构</button><button type="button" @click="architectureRoleChangeFeedback=''" aria-label="关闭角色变更提示">×</button></div></div>
            <nav class="architecture-preset-bar" aria-label="目标架构快捷选择"><span><b>快速转换</b><small>选择目标，Manager 将生成可审计的安全流程</small></span><button type="button" :class="{active:architectureForm.architecture==='standalone'}" @click="applyArchitecturePreset('standalone')"><i>••</i><b>全部独立</b><small>解除复制，各自可写</small></button><button type="button" :class="{active:architectureForm.architecture==='master_slave'}" @click="applyArchitecturePreset('master_slave')"><i>→</i><b>一主多从</b><small>单写，多副本</small></button><button type="button" :class="{active:architectureForm.architecture==='dual_master'}" @click="applyArchitecturePreset('dual_master')"><i>⇄</i><b>双主架构</b><small>双向复制，双节点可写</small></button></nav>
            <section v-if="architectureHasChanges || architecturePlan || (architectureRun && !['success','failed'].includes(architectureRun.status))" :class="['architecture-quick-confirm',{planned:!!architecturePlan}]" aria-live="polite"><div><i>{{ architecturePlan ? '✓' : '→' }}</i><span><b>{{ architectureRun && !['success','failed'].includes(architectureRun.status) ? (architectureOperationVIPOnly ? 'VIP '+architectureVIPOperationAction+'正在执行' : '架构调整正在执行') : architecturePlan ? '安全执行计划已生成' : architectureAdjustmentTitle }}</b><small>{{ architectureRun && !['success','failed'].includes(architectureRun.status) ? 'Manager 正在通过 Agent 执行安全流程，可随时查看完整步骤与日志。' : architecturePlan ? architectureAdjustmentTitle+'。请打开计划核对步骤，并确认执行。' : architectureAdjustmentDetail }}</small></span></div><button v-if="architectureRun && !['success','failed'].includes(architectureRun.status)" type="button" class="primary" @click="architecturePlanDialog=true">{{ architectureOperationVIPOnly ? '查看 VIP 流程进度' : '查看架构调整进度' }}</button><button v-else-if="architecturePlan" type="button" class="primary" @click="architecturePlanDialog=true">{{ architectureOperationVIPOnly ? '查看计划并执行 VIP '+architectureVIPOperationAction : '查看计划并执行调整' }}</button><button v-else type="button" class="primary" :disabled="architectureSubmitting || architectureForm.nodes.length<2" @click="previewArchitectureAdjustment">{{ architectureSubmitting ? '正在生成安全计划…' : (architectureForm.move_vip && !architectureTopologyHasChanges() ? '开始 VIP '+architectureVIPActionLabel() : '开始调整架构') }}</button></section>
            <section v-if="false" class="architecture-vip-management vip-management-workspace">
              <div class="vip-page-head"><div><p>CLUSTER ACCESS ENDPOINT</p><h3>VIP 管理与安全漂移</h3><span>漂移按“互斥锁、隔离旧主、全节点零持有者、绑定新主、唯一持有者复检”执行，不使用额外执行凭证。</span></div><button type="button" class="secondary" :disabled="vipBusy" @click="refreshVIPManagement(true)">{{ vipBusy ? '正在检测…' : '刷新网卡与当前状态' }}</button></div>
              <form class="vip-create-panel" @submit.prevent="saveVIPConfig"><header><div><b>添加或重新绑定业务 VIP</b><small>宣告方式由系统自动选择，提交前后均检查全部集群机器，无法证明唯一持有者时立即停止。</small></div><span>防脑裂漂移</span></header><div class="vip-form-grid"><label>VIP 名称<input v-model.trim="vipForm.vip_name" required placeholder="业务 VIP"></label><label>VIP 地址<input v-model.trim="vipForm.vip_address" required placeholder="例如 192.168.31.100"></label><label>网络前缀<input v-model.number="vipForm.vip_prefix" type="number" min="1" max="32" required></label><label>目标持有机器<select v-model="vipForm.target_machine_id" required @change="vipForm.default_interface=vipInterfaceOptions[0]?.name||''"><option value="">请选择目标主节点</option><option v-for="node in architectureForm.nodes.filter(item=>item.role==='M')" :key="node.machine_id" :value="node.machine_id">{{ architectureNodeMeta(node.machine_id).name }} · {{ architectureNodeMeta(node.machine_id).ip }}</option></select></label><label class="vip-interface-select">使用网卡<select v-model="vipForm.default_interface" required><option value="">请选择业务网卡</option><option v-for="item in vipInterfaceOptions" :key="item.name" :value="item.name">{{ item.name }} · {{ item.ip }}</option></select><small v-if="!vipInterfaceOptions.length">未读取到可用 IPv4 网卡，请刷新网卡与当前状态。</small><small v-else>仅允许选择拓扑中的主节点；同集群高可用操作互斥，系统自动完成 ARP/BGP 宣告。</small></label></div><footer><ol><li><i>1</i>获取集群互斥锁</li><li><i>2</i>全节点撤销并确认归零</li><li><i>3</i>绑定目标并自动宣告</li><li><i>4</i>连续确认唯一持有者</li></ol><button class="primary" :disabled="vipBusy || !vipForm.vip_address || !vipForm.target_machine_id || !vipForm.default_interface">{{ vipBusy ? '正在执行…' : '添加并安全验证' }}</button></footer></form>
              <section class="vip-current-panel"><header><div><b>当前 VIP</b><small>Manager 配置与全部集群机器实时扫描的合并结果。</small></div><span>{{ vipConfigs.length }} 个</span></header><div v-if="vipConfigs.length" class="vip-card-grid"><article v-for="item in vipConfigs" :key="item.vip_address" :class="['vip-status-card',String(vipStateFor(item).vip_status||'UNKNOWN').toLowerCase()]"><header><span>VIP</span><em :class="['status',vipStateFor(item).vip_status==='BOUND'?'success':['CONFLICT','MISMATCH','FAILED'].includes(vipStateFor(item).vip_status)?'error':'offline']">{{ vipStatusLabel(vipStateFor(item).vip_status) }}</em></header><h4>{{ item.vip_address }}/{{ item.vip_prefix }}</h4><p>{{ item.vip_name || '业务 VIP' }}</p><dl><div><dt>期望持有者</dt><dd>{{ vipMachineName(vipStateFor(item).expected_holder_machine_id) }}</dd></div><div><dt>当前持有者</dt><dd>{{ vipMachineName(vipStateFor(item).current_holder_machine_id) }}</dd></div><div><dt>使用网卡</dt><dd>{{ vipStateFor(item).current_interface || item.default_interface || '待检测' }}</dd></div><div><dt>宣告策略</dt><dd>系统自动 · {{ item.vip_route_mode==='BGP' ? 'BGP' : 'ARP' }}</dd></div><div><dt>最近检测</dt><dd>{{ vipStateFor(item).updated_at ? date(vipStateFor(item).updated_at) : '尚未检测' }}</dd></div></dl><p v-if="vipStateFor(item).last_error" class="vip-card-error">{{ errorSummary(vipStateFor(item).last_error,180) }}</p><footer><button type="button" class="danger-link" :disabled="vipBusy" @click="deleteVIPConfig(item)">全节点撤销并删除</button></footer></article></div><div v-else class="vip-empty"><span>◇</span><b>尚未配置业务 VIP</b><small>选择目标持有机器和业务网卡后，系统会自动完成安全绑定。</small></div></section>
            </section>
            <form class="topology-architecture-layout" @submit.prevent="previewArchitectureAdjustment">
              <section class="topology-editor-card">
                <header class="topology-editor-head"><div><b>目标架构·{{ ({standalone:'独立实例',master_slave:'一主多从',dual_master:'双主架构',multi_master:'多主架构'})[architectureForm.architecture] }}</b><small>可使用上方快捷转换，也可直接修改单个节点角色或拖动连线</small></div><span v-if="architectureLinkSource" class="topology-link-hint">请点击目标实例 · <button type="button" @click="architectureLinkSource=''">取消</button></span><span v-else class="topology-safe-hint">草稿模式 · 确认执行前不会修改数据库</span></header>
                <div class="topology-draft-canvas">
                  <section class="topology-vip-palette">
                    <header><div><b>业务访问入口</b><small>{{ vipConfigs.length ? '拖动 VIP 卡片到主节点，或先点卡片再点目标主节点' : '请先在右侧添加业务 VIP' }}</small></div><span>拖拽绑定</span></header>
                    <div v-if="vipConfigs.some(item=>!vipCardTargetMachineID(item))" class="topology-unbound-vips">
                      <article v-for="item in vipConfigs.filter(item=>!vipCardTargetMachineID(item))" :key="'unbound-vip-'+item.vip_address" draggable="true" :class="['topology-vip-chip',{dragging:vipDraggingAddress===item.vip_address}]" @click="selectArchitectureVIPCard(item)" @dragstart="startArchitectureVIPDrag($event,item)" @dragend="finishArchitectureVIPDrag">
                        <i>VIP</i><span><b>{{ item.vip_address }}</b><small>{{ vipStatusLabel(vipStateFor(item).vip_status) }} · 拖到主节点</small></span><em>⋮⋮</em>
                      </article>
                    </div>
                    <div v-else-if="vipConfigs.length" class="topology-vip-placed-hint">VIP 已放置在主节点旁，可继续拖到另一主节点发起安全漂移。</div>
                  </section>
                  <div class="topology-draft-stem"></div>
                  <div v-if="architectureForm.nodes.some(node=>node.role==='M')" :class="['topology-level','topology-master-level',{'drag-active':architectureDraggingNode}]" @dragover.prevent @drop.prevent="dropArchitectureLayer('M')">
                    <span class="topology-level-label">根节点 · 主库</span>
                    <div class="topology-node-grid topology-root-grid">
                      <article v-for="node in architectureForm.nodes.filter(item=>item.role==='M')" :key="node.machine_id" draggable="true" :class="['topology-edit-node', node.role.toLowerCase(), 'root-node', {'vip-drop-target':vipDraggingAddress, 'vip-magnet-active':vipMagnetTargetID===node.machine_id, selected:architectureSelectedNode===node.machine_id, dragging:architectureDraggingNode===node.machine_id, source:architectureLinkSource===node.machine_id, target:architectureLinkSource && architectureLinkSource!==node.machine_id, changed:architectureNodeHasChanges(node)}]" @dragstart="startArchitectureNodeDrag($event,node)" @dragend="finishArchitectureNodeDrag" @dragenter.prevent="setVIPMagnetTarget(node)" @dragover.prevent="setVIPMagnetTarget(node)" @dragleave="clearVIPMagnetTarget($event,node)" @drop.stop.prevent="dropArchitectureCanvasItem(node)" @click="vipDraggingAddress ? dropArchitectureCanvasItem(node) : (architectureLinkSource && architectureLinkSource!==node.machine_id ? completeArchitectureLink(node) : architectureSelectedNode=node.machine_id)">
                        <span class="vip-magnet-halo" aria-hidden="true"></span>
                        <header><span class="topology-db-icon">◉</span><select :value="node.role" @click.stop @change="requestArchitectureRoleChange(node,$event.target.value)"><option value="M">主</option><option value="S">从</option><option value="I">独立</option></select><button type="button" title="移出集群" @click.stop="kickArchitectureNode(node)">×</button></header>
                        <div><b>{{ architectureNodeMeta(node.machine_id).name }}</b><small>{{ architectureNodeMeta(node.machine_id).ip }}:{{ node.port }}</small><span><i></i>{{ architectureNodeMeta(node.machine_id).status }}</span></div>
                        <footer><button type="button" @click.stop="startArchitectureLink(node)">连线</button><small v-if="node.role==='S'">复制源：{{ architectureNodeName(node.source_machine_id || architectureForm.primary_machine_id) }}</small><small v-else>{{ architectureForm.architecture==='master_slave' ? '根节点 · 可写主库' : '根节点 · 双主可写' }}</small></footer>
                        <aside v-if="vipMagnetTargetID===node.machine_id && vipDraggingAddress && !vipConfigs.some(vip=>vipCardTargetMachineID(vip)===node.machine_id)" class="topology-vip-magnet-preview" aria-hidden="true"><i>VIP</i><span><b>{{ vipDraggingAddress }}</b><small>释放后吸附到此节点</small></span></aside>
                        <aside v-for="item in vipConfigs.filter(vip=>vipCardTargetMachineID(vip)===node.machine_id)" :key="'node-vip-'+item.vip_address" draggable="true" :class="['topology-node-vip-card',{pending:architectureForm.move_vip,dragging:vipDraggingAddress===item.vip_address,snapping:vipSnapTargetID===node.machine_id}]" @click.stop="selectArchitectureVIPCard(item)" @dragstart.stop="startArchitectureVIPDrag($event,item)" @dragend.stop="finishArchitectureVIPDrag">
                          <i>VIP</i><span><b>{{ item.vip_address }}</b><small>{{ architectureForm.move_vip ? '待确认' : vipStatusLabel(vipStateFor(item).vip_status) }}</small></span><em>⋮⋮</em>
                        </aside>
                      </article>
                    </div>
                  </div>
                  <div v-if="architectureForm.nodes.some(node=>node.role==='S')" class="topology-branch-connector"><i></i><span></span></div>
                  <div v-if="architectureForm.nodes.some(node=>node.role==='S')" :class="['topology-level','topology-replica-level',{'drag-active':architectureDraggingNode}]" @dragover.prevent @drop.prevent="dropArchitectureLayer('S')">
                    <span class="topology-level-label">叶子节点 · 从库</span>
                    <div class="topology-node-grid topology-leaf-grid">
                      <article v-for="node in architectureForm.nodes.filter(item=>item.role==='S')" :key="node.machine_id" draggable="true" :class="['topology-edit-node', node.role.toLowerCase(), 'leaf-node', {selected:architectureSelectedNode===node.machine_id, dragging:architectureDraggingNode===node.machine_id, source:architectureLinkSource===node.machine_id, target:architectureLinkSource && architectureLinkSource!==node.machine_id, changed:architectureNodeHasChanges(node)}]" @dragstart="startArchitectureNodeDrag($event,node)" @dragend="finishArchitectureNodeDrag" @dragover.prevent @drop.stop.prevent="dropArchitectureCanvasItem(node)" @click="architectureLinkSource && architectureLinkSource!==node.machine_id ? completeArchitectureLink(node) : architectureSelectedNode=node.machine_id">
                        <header><span class="topology-db-icon">◉</span><select :value="node.role" @click.stop @change="requestArchitectureRoleChange(node,$event.target.value)"><option value="M">主</option><option value="S">从</option><option value="I">独立</option></select><button type="button" title="移出集群" @click.stop="kickArchitectureNode(node)">×</button></header>
                        <div><b>{{ architectureNodeMeta(node.machine_id).name }}</b><small>{{ architectureNodeMeta(node.machine_id).ip }}:{{ node.port }}</small><span><i></i>{{ architectureNodeMeta(node.machine_id).status }}</span></div>
                        <footer><button type="button" @click.stop="startArchitectureLink(node)">连线</button><small v-if="node.role==='S'">复制源：{{ architectureNodeName(node.source_machine_id || architectureForm.primary_machine_id) }}</small><small v-else>{{ architectureForm.architecture==='master_slave' ? '根节点 · 可写主库' : '根节点 · 双主可写' }}</small></footer>
                      </article>
                      <button type="button" class="topology-add-node-tile" @click="data.clusterSection='instances'"><i>＋</i><b>添加实例</b><small>新实例自动作为从节点</small></button>
                    </div>
                  </div>
                  <div v-if="architectureForm.nodes.some(node=>node.role==='I')" :class="['topology-level','topology-independent-level',{'drag-active':architectureDraggingNode}]" @dragover.prevent @drop.prevent="dropArchitectureLayer('I')">
                    <span class="topology-level-label">独立实例 · 无复制关系</span>
                    <div class="topology-node-grid topology-independent-grid">
                      <article v-for="node in architectureForm.nodes.filter(item=>item.role==='I')" :key="node.machine_id" draggable="true" :class="['topology-edit-node','independent-node',{selected:architectureSelectedNode===node.machine_id,dragging:architectureDraggingNode===node.machine_id,changed:architectureNodeHasChanges(node)}]" @dragstart="startArchitectureNodeDrag($event,node)" @dragend="finishArchitectureNodeDrag" @dragover.prevent @drop.stop.prevent="dropArchitectureCanvasItem(node)" @click="architectureSelectedNode=node.machine_id">
                        <header><span class="topology-db-icon">◉</span><select :value="node.role" @click.stop @change="requestArchitectureRoleChange(node,$event.target.value)"><option value="M">主</option><option value="S">从</option><option value="I">独立</option></select><button type="button" title="移出集群" @click.stop="kickArchitectureNode(node)">×</button></header>
                        <div><b>{{ architectureNodeMeta(node.machine_id).name }}</b><small>{{ architectureNodeMeta(node.machine_id).ip }}:{{ node.port }}</small><span><i></i>{{ architectureNodeMeta(node.machine_id).status }}</span></div>
                        <footer><button type="button" @click.stop="startArchitectureLink(node)">建立复制</button><small>独立可写 · 无复制源</small></footer>
                      </article>
                    </div>
                  </div>
                  <div class="topology-draft-edges"><b>目标复制关系</b><span v-for="edge in architectureDraftEdges()" :key="edge.source+'>'+edge.target"><em>{{ architectureNodeName(edge.source) }}</em><i>{{ edge.mutual ? '⇄' : '→' }}</i><em>{{ architectureNodeName(edge.target) }}</em></span><small v-if="!architectureDraftEdges().length">点击节点的“连线”，再点击目标节点建立复制关系。</small></div>
                </div>
              </section>
              <aside class="topology-inspector">
                <section class="topology-vip-editor">
                  <header><div><b>业务 VIP</b><small>{{ vipConfigs.length ? '已配置 '+vipConfigs.length+' 个' : '尚未配置，可直接添加' }}</small></div><span><button type="button" class="vip-header-action" @click="beginNewVIP">＋ 新增</button><button type="button" title="刷新网卡和 VIP 实机状态" :disabled="vipBusy" @click="refreshVIPManagement(true)">↻</button></span></header>
                  <div class="topology-vip-editor-body">
                    <label v-if="vipConfigs.length>1">选择 VIP<select :value="vipEditingAddress" @change="selectVIPForEdit($event.target.value)"><option v-for="item in vipConfigs" :key="item.vip_address" :value="item.vip_address">{{ item.vip_address }}/{{ item.vip_prefix }}</option></select></label>
                    <div v-if="!vipEditorIsNew" class="topology-vip-live"><span :class="['status',vipEditorState.vip_status==='BOUND'?'success':['CONFLICT','MISMATCH','FAILED'].includes(vipEditorState.vip_status)?'failed':'offline']">{{ vipStatusLabel(vipEditorState.vip_status) }}</span><b>{{ vipForm.vip_address }}/{{ vipForm.vip_prefix }}</b><small>当前持有：{{ vipMachineName(vipEditorState.current_holder_machine_id) }} · {{ vipEditorState.current_interface || '网卡待检测' }}</small></div>
                    <div v-else class="topology-vip-live new"><span>NEW</span><b>新增业务 VIP</b><small>保存后自动绑定与验证</small></div>
                    <label>名称<input v-model.trim="vipForm.vip_name" placeholder="业务 VIP"></label>
                    <div class="topology-vip-inline"><label>VIP 地址<input v-model.trim="vipForm.vip_address" :disabled="!vipEditorIsNew" placeholder="192.168.31.100"></label><label>前缀<input v-model.number="vipForm.vip_prefix" type="number" min="1" max="32"></label></div>
                    <label v-if="vipEditorIsNew">初始目标主节点<select v-model="vipForm.target_machine_id" @change="architectureVIPTargetChanged"><option value="">请选择</option><option v-for="node in architectureForm.nodes.filter(item=>item.role==='M')" :key="node.machine_id" :value="node.machine_id">{{ architectureNodeMeta(node.machine_id).name }}</option></select></label>
                    <label>业务网卡<select v-model="vipForm.default_interface"><option value="">请选择</option><option v-for="item in vipInterfaceOptions" :key="item.name" :value="item.name">{{ item.name }} · {{ item.ip }}</option></select></label>
                    <dl v-if="!vipEditorIsNew" class="topology-vip-facts"><div><dt>配置目标</dt><dd>{{ vipMachineName(vipEditorState.expected_holder_machine_id) }}</dd></div><div><dt>实机持有</dt><dd>{{ vipMachineName(vipEditorState.current_holder_machine_id) }}</dd></div><div><dt>当前网卡</dt><dd>{{ vipEditorState.current_interface || vipForm.default_interface || '—' }}</dd></div><div><dt>最近复检</dt><dd>{{ vipEditorState.updated_at ? date(vipEditorState.updated_at) : '—' }}</dd></div></dl>
                    <div v-if="!vipEditorIsNew" class="topology-vip-drag-help"><i>↗</i><span><b>拖拽卡片绑定或漂移</b><small>将画布中的 VIP 卡片拖到目标主节点旁，系统会弹出安全流程确认。</small></span></div>
                    <p v-if="vipEditorState.last_error" class="topology-vip-error">{{ errorSummary(vipEditorState.last_error,120) }}</p>
                    <div class="topology-vip-actions"><button v-if="!vipEditorIsNew" type="button" class="danger-link" :disabled="vipBusy" @click="deleteVIPConfig(vipEditingConfig)">撤销并删除此 VIP</button><button v-if="vipEditorIsNew" type="button" class="primary" :disabled="vipBusy || !vipForm.vip_address || !vipForm.target_machine_id || !vipForm.default_interface" @click="saveVIPConfig">{{ vipBusy ? '正在添加…' : '添加业务 VIP' }}</button><small v-else>VIP 实机操作统一由画布拖拽触发。</small></div>
                  </div>
                </section>
                <section v-if="architectureSelectedNode"><header><b>实例设置</b><small>{{ architectureNodeName(architectureSelectedNode) }}</small></header><div class="topology-inspector-form" v-for="node in architectureForm.nodes.filter(item=>item.machine_id===architectureSelectedNode)" :key="node.machine_id"><label>目标角色<select :value="node.role" @change="requestArchitectureRoleChange(node,$event.target.value)"><option value="M">主节点</option><option value="S">从节点</option><option value="I">独立实例</option></select></label><label v-if="node.role==='S'">复制源<select v-model="node.source_machine_id" @change="architecturePlan=null"><option v-for="source in architectureForm.nodes.filter(item=>item.role==='M'&&item.machine_id!==node.machine_id)" :key="source.machine_id" :value="source.machine_id">{{ architectureNodeName(source.machine_id) }}</option></select></label><label v-if="node.role!=='I'">选举优先级<input v-model.number="node.election_priority" type="number" min="0" max="1000" @input="architecturePlan=null"></label><label v-if="node.role==='S'">复制延时（秒）<input v-model.number="node.delay_seconds" type="number" min="0" @input="architecturePlan=null"></label><p v-if="node.role==='I'" class="topology-independent-note">执行后将停止并清理复制，恢复为独立可写实例。拆分前必须通过 PT 数据一致性验证。</p></div></section>
              </aside>
            </form>
            <div v-if="vipDriftDialog" class="modal-mask vip-drift-mask" @click.self="cancelVIPDrift">
              <section class="modal vip-drift-modal">
                <header><span>VIP</span><div><p>SAFE VIP {{ vipDriftDialog.action==='bind' ? 'BINDING' : 'MIGRATION' }}</p><h2>{{ vipDriftDialog.action==='bind' ? '确认绑定业务 VIP' : '确认漂移业务 VIP' }}</h2><small>拖拽目标已选择，确认后进入防脑裂 VIP 流程</small></div><button type="button" title="关闭" @click="cancelVIPDrift">×</button></header>
                <div class="vip-drift-route">
                  <article><small>当前持有</small><b>{{ vipDriftDialog.from_machine_id ? architectureNodeName(vipDriftDialog.from_machine_id) : '尚未绑定' }}</b><span>{{ vipDriftDialog.vip_address }}/{{ vipDriftDialog.vip_prefix }}</span></article>
                  <i>→</i>
                  <article class="target"><small>目标主节点</small><b>{{ architectureNodeName(vipDriftDialog.target_machine_id) }}</b><span>{{ architectureNodeMeta(vipDriftDialog.target_machine_id).ip }} · {{ vipForm.default_interface || '自动选择网卡' }}</span></article>
                </div>
                <ol class="vip-drift-safety"><li><i>1</i><span><b>获取集群互斥锁</b><small>阻止并发 VIP 与架构操作</small></span></li><li v-if="vipDriftDialog.action==='migrate'"><i>2</i><span><b>锁定业务入口</b><small>启用 offline_mode，拒绝新业务连接</small></span></li><li v-if="vipDriftDialog.action==='migrate'"><i>3</i><span><b>排空业务会话</b><small>保留管理连接并清理存量业务会话</small></span></li><li><i>{{ vipDriftDialog.action==='migrate' ? 4 : 2 }}</i><span><b>扫描当前持有者</b><small>发现多节点持有立即停止</small></span></li><li><i>{{ vipDriftDialog.action==='migrate' ? 5 : 3 }}</i><span><b>撤销旧 VIP</b><small>从全部集群机器移除网络入口</small></span></li><li><i>{{ vipDriftDialog.action==='migrate' ? 6 : 4 }}</i><span><b>零持有者屏障</b><small>确认全节点均不存在 VIP 后才继续</small></span></li><li><i>{{ vipDriftDialog.action==='migrate' ? 7 : 5 }}</i><span><b>{{ vipDriftDialog.action==='bind' ? '绑定并宣告' : '绑定新节点并宣告' }}</b><small>自动执行 ARP/BGP 网络宣告</small></span></li><li><i>{{ vipDriftDialog.action==='migrate' ? 8 : 6 }}</i><span><b>唯一持有者复检</b><small>失败时撤销新绑定；成功后恢复业务访问</small></span></li></ol>
                <div class="vip-drift-warning"><b>{{ vipDriftDialog.action==='migrate' ? '漂移期间业务访问会被主动暂停' : '首次绑定不会修改数据库角色' }}</b><p>{{ vipDriftDialog.action==='migrate' ? '系统会先阻止新连接并排空存量业务会话，完成唯一持有者验证后再恢复访问；双主角色和复制关系保持不变。' : '系统仅执行网络入口绑定、宣告和唯一持有者校验。' }}</p></div>
                <footer><button type="button" class="secondary" @click="cancelVIPDrift">取消</button><button type="button" class="primary" :disabled="architectureSubmitting || !vipForm.default_interface" @click="confirmVIPDrift">{{ architectureSubmitting ? '正在生成安全计划…' : (vipDriftDialog.action==='bind' ? '确认并进入 VIP 绑定流程' : '确认并进入 VIP 漂移流程') }}</button></footer>
              </section>
            </div>
            <div v-if="architectureRoleChangeDialog" class="modal-mask topology-risk-mask" @click.self="architectureRoleChangeDialog=null">
              <section class="modal topology-risk-modal">
                <header><span>!</span><div><p>ROLE CHANGE RISK</p><h2>确认切换主从角色</h2></div><button type="button" @click="architectureRoleChangeDialog=null">×</button></header>
                <div class="topology-risk-body">
                  <div class="topology-risk-summary">
                    <div class="topology-risk-target"><small>当前选择</small><b>{{ architectureNodeName(architectureRoleChangeDialog.machine_id) }}</b><span>{{ architectureRoleLabel(architectureRoleChangeDialog.from_role) }} → {{ architectureRoleLabel(architectureRoleChangeDialog.to_role) }}</span></div>
                    <label v-if="architectureRoleChangeDialog.needs_replacement" class="topology-risk-replacement"><span><b>选择接替的新主节点</b><small>Manager 会先等待候选节点复制追平，再执行角色互换。</small></span><select v-model="architectureRoleChangeDialog.replacement_machine_id" required><option value="">请选择候选从节点</option><option v-for="candidate in architectureForm.nodes.filter(item=>item.role==='S'&&item.machine_id!==architectureRoleChangeDialog.machine_id)" :key="candidate.machine_id" :value="candidate.machine_id">{{ architectureNodeName(candidate.machine_id) }} · 选举优先级 {{ candidate.election_priority }}</option></select></label>
                  </div>
                  <section v-if="architectureRoleChangePromotedMachineID() || architectureRoleChangeDemotedMachineID()" class="topology-role-impact">
                    <header><div><b>调整后的角色关系</b><small>应用到草稿后，将同时记录以下角色变化</small></div><em>{{ architectureRoleChangePromotedMachineID() && architectureRoleChangeDemotedMachineID() ? '2 个节点变化' : '1 个节点变化' }}</em></header>
                    <div class="topology-role-impact-grid">
                      <article v-if="architectureRoleChangePromotedMachineID()" class="promoted"><span>↑</span><div><small>提升为主节点</small><b>{{ architectureNodeName(architectureRoleChangePromotedMachineID()) }}</b></div><em>{{ architectureRoleChangePromotedMachineID()===architectureRoleChangeDialog.machine_id ? architectureRoleLabel(architectureRoleChangeDialog.from_role) : '从节点' }} → 主节点</em></article>
                      <article v-if="architectureRoleChangeDemotedMachineID()" class="demoted"><span>↓</span><div><small>降为从节点</small><b>{{ architectureNodeName(architectureRoleChangeDemotedMachineID()) }}</b></div><em>主节点 → 从节点</em></article>
                    </div>
                  </section>
                  <div class="topology-risk-warning"><b>{{ [architectureRoleChangeDialog.from_role,architectureRoleChangeDialog.to_role].includes('I') ? '复制关系调整前会执行一致性校验' : '切主期间业务连接会短暂断开' }}</b><p>Manager 会通过 Agent 暂停必要写入、等待复制追平，并使用 Percona Toolkit 验证数据一致性后再调整真实复制关系。应用需要具备断线重连能力。</p></div>
                  <ul class="topology-risk-effects"><li><i>1</i><span><b>短暂不可写</b><small>旧主会进入 read_only / super_read_only</small></span></li><li><i>2</i><span><b>存量连接被清理</b><small>除管理、复制、监控和备份账号外的会话将断开</small></span></li><li><i>3</i><span><b>复制未追平可能延长中断</b><small>超过 60 秒后必须再次人工确认，不会自动强制切换</small></span></li></ul>
                </div>
                <footer><small>此步骤只更新拓扑草稿，不会立即修改线上 MySQL。</small><button type="button" class="secondary" @click="architectureRoleChangeDialog=null">取消变更</button><button type="button" class="danger-button" :disabled="architectureRoleChangeDialog.needs_replacement&&!architectureRoleChangeDialog.replacement_machine_id" @click="confirmArchitectureRoleChange">应用角色变更到草稿</button></footer>
              </section>
            </div>
            <div v-if="architecturePlanDialog && (architecturePlan || architectureRun)" class="modal-mask architecture-execution-mask" @click.self="architecturePlanDialog=false"><section class="modal architecture-execution-modal"><header><div><p>{{ architectureRun ? (architectureOperationVIPOnly ? 'LIVE VIP CHANGE' : 'LIVE ARCHITECTURE CHANGE') : 'SAFE EXECUTION PLAN' }}</p><h2>{{ architectureRun ? (architectureRun.status==='success' ? (architectureOperationVIPOnly ? 'VIP '+architectureVIPOperationAction+'已完成' : '架构调整已完成') : architectureRun.status==='failed' ? (architectureOperationVIPOnly ? 'VIP '+architectureVIPOperationAction+'失败' : '架构调整失败') : (architectureOperationVIPOnly ? '正在安全'+architectureVIPOperationAction+' VIP' : '正在安全调整集群架构')) : (architectureOperationVIPOnly ? 'VIP '+architectureVIPOperationAction+'安全计划' : '架构调整安全计划') }}</h2><small>{{ architectureRun ? architectureRun.run_id : '计划 ID：'+architecturePlan.plan_id }}</small></div><button type="button" title="关闭" @click="architecturePlanDialog=false">×</button></header><div v-if="architectureRun" class="architecture-dialog-progress"><i :style="{width:architectureRunProgress()+'%'}"></i><span>{{ architectureRunProgress() }}%</span></div><div v-else class="architecture-dialog-verdict"><span :class="['status',architecturePlan.executable ? 'success' : 'failed']">{{ architecturePlan.executable ? '安全预检通过' : '存在安全阻断' }}</span><small>预检不会修改 MySQL、VIP 或网络配置</small></div><div v-if="(architectureRun?.error || architecturePlan?.blocking_reasons?.length)" class="architecture-blockers"><b>{{ architectureRun?.status==='waiting_force_confirmation' ? '需要人工决策' : '安全阻断' }}</b><p v-if="architectureRun?.error">{{ errorSummary(architectureRun.error) }}</p><p v-for="item in (architecturePlan?.blocking_reasons || [])" :key="item">{{ errorSummary(item,140) }}</p></div><ol class="architecture-dialog-steps"><li v-for="(step,index) in (architectureRun?.plan?.steps || architecturePlan?.steps || [])" :key="step.code" :class="architectureRun ? architectureRunStepStatus(step) : {ready:true,danger:step.destructive}" :style="{animationDelay:(index*70)+'ms'}"><i><span v-if="architectureRun && architectureRunStepStatus(step)==='running'" class="step-spinner"></span><template v-else>{{ architectureRun ? (architectureRunStepStatus(step)==='success' ? '✓' : architectureRunStepStatus(step)==='failed' ? '!' : step.order) : step.order }}</template></i><div><b>{{ step.name }}</b><small>{{ architectureRun ? errorSummary(architectureRunStepResult(step)?.message || (architectureRunStepResult(step)?.task_ids || []).join('、') || step.description,140) : step.description }}</small></div><em>{{ architectureRun ? ({pending:'等待',running:'执行中',success:'已完成',failed:'失败'})[architectureRunStepStatus(step)] : (step.requires_confirmation ? '需确认' : '已就绪') }}</em></li></ol><footer><button type="button" class="secondary" @click="architecturePlanDialog=false">{{ architectureRun && !['success','failed'].includes(architectureRun.status) ? '后台执行并收起' : '关闭' }}</button><button v-if="architectureRun" type="button" class="secondary" @click="openArchitectureRunTask">在任务中心查看</button><button v-if="architectureRun?.status==='waiting_force_confirmation'" type="button" class="danger-button" @click="confirmArchitectureForce">确认强制切主</button><button v-if="!architectureRun" type="button" class="primary" :disabled="architectureSubmitting || !architecturePlan.executable" @click="submitArchitectureAdjustment">{{ architectureOperationVIPOnly ? '按此计划'+architectureVIPOperationAction+' VIP' : '按此计划执行架构调整' }}</button></footer></section></div>
          </section>
          <section v-else-if="data.clusterSection==='backup'" class="backup-workspace">
            <div class="backup-page-head"><div><p>XTRABACKUP OPERATIONS</p><h3>备份与恢复</h3><span>Manager 负责策略调度，Agent 使用统一参数化 Shell 模板执行物理备份与恢复。</span></div><div><button class="secondary" @click="loadClusterBackups">刷新</button><button class="primary" @click="openBackupPolicyEditor()">＋ 新建策略</button></div></div>
            <form v-if="showBackupPolicyEditor" class="backup-policy-editor" @submit.prevent="saveBackupPolicy">
              <header><div><b>{{ backupPolicyForm.id ? '编辑备份策略' : '创建备份策略' }}</b><small>配置会保存在 Manager，正式执行由目标机器 Agent 完成。</small></div><button type="button" @click="showBackupPolicyEditor=false">×</button></header>
              <section class="backup-config-section"><div class="backup-section-title"><b>备份对象</b><small>默认选择拓扑中的备节点；只有机器存在多个 MySQL 实例时才需要选择实例。</small></div><div class="backup-form-grid"><label>策略名称<input v-model.trim="backupPolicyForm.name" required placeholder="生产集群物理备份"></label><label>备份机器<select v-model="backupPolicyForm.machine_id" required @change="backupMachineChanged"><option value="">自动选择备节点</option><option v-for="machine in backupMachines()" :key="machine.ID||machine.id" :value="machine.ID||machine.id">{{ machine.Name||machine.name }} · {{ machine.IP||machine.ip }} · {{ backupMachineRole(machine) }}</option></select></label><label v-if="backupInstancesForMachine(backupPolicyForm.machine_id).length>1">多实例选择<select v-model.number="backupPolicyForm.port"><option v-for="instance in backupInstancesForMachine(backupPolicyForm.machine_id)" :key="instance.Port||instance.port" :value="Number(instance.Port||instance.port)">端口 {{ instance.Port||instance.port }} · {{ instance.PackageName||instance.package_name||'版本待上报' }}</option></select></label><label v-else>当前实例<input :value="backupInstancesForMachine(backupPolicyForm.machine_id).length ? '端口 '+backupPolicyForm.port : '将自动选择备节点实例'" readonly></label><label>默认备份类型<select v-model="backupPolicyForm.backup_type"><option value="full">全量备份</option><option value="incremental">增量备份</option></select></label></div></section>
              <section class="backup-config-section"><div class="backup-section-title"><b>调度配置</b><small>按周模式可为每个执行日分别指定全量或增量；增量前必须存在成功的全量备份。</small></div><div class="backup-form-grid schedule-grid"><label>调度方式<select v-model="backupPolicyForm.schedule_type"><option value="weekly">按周备份</option><option value="custom">自定义间隔</option><option value="once">备份一次</option></select></label><label v-if="backupPolicyForm.schedule_type==='custom'">间隔时间（分钟）<input v-model.number="backupPolicyForm.interval_minutes" type="number" min="1" required></label><label>首次发起日期与时间<input v-model="backupPolicyForm.start_at" type="datetime-local" required></label></div><div v-if="backupPolicyForm.schedule_type==='weekly'" class="weekly-schedule"><div class="weekday-picker"><b>执行日期</b><label><input type="checkbox" :checked="backupPolicyForm.weekdays.length===7" @change="toggleAllBackupWeekdays">全选</label><label v-for="day in [{v:1,n:'周一'},{v:2,n:'周二'},{v:3,n:'周三'},{v:4,n:'周四'},{v:5,n:'周五'},{v:6,n:'周六'},{v:0,n:'周日'}]" :key="day.v"><input v-model="backupPolicyForm.weekdays" type="checkbox" :value="day.v">{{ day.n }}</label></div><div class="weekly-backup-types"><div v-for="day in backupPolicyForm.weekdays" :key="day"><b>{{ weekdayName(day) }}</b><label><input v-model="backupPolicyForm.weekday_backup_types[String(day)]" type="radio" value="full">全量</label><label><input v-model="backupPolicyForm.weekday_backup_types[String(day)]" type="radio" value="incremental">增量</label></div></div></div></section>
              <section class="backup-config-section"><div class="backup-section-title"><b>存储与安全预检</b><small>磁盘使用率达到阈值会直接失败；备节点延迟超过 30 秒仍未归零会进入重试。</small></div><div class="backup-form-grid"><label class="backup-path-field">备份位置（目标机器绝对路径）<input v-model.trim="backupPolicyForm.backup_location" required placeholder="/data/gmha/backups"></label><label>磁盘使用率失败阈值<input v-model.number="backupPolicyForm.disk_usage_threshold" type="number" min="1" max="99" required><small>默认 95%，达到阈值直接拒绝备份</small></label><label>失败后重试次数<input v-model.number="backupPolicyForm.retry_count" type="number" min="0" max="5"><small>最多 5 次，超过后判定当日失败</small></label><label>重试间隔（秒）<input v-model.number="backupPolicyForm.retry_interval_seconds" type="number" min="1"></label><label>备份账号<input v-model.trim="backupPolicyForm.mysql_user" required></label><label>备份账号密码<input v-model="backupPolicyForm.mysql_password" type="password" :placeholder="backupPolicyForm.id ? '留空保持原密码' : '请输入密码'"></label></div><div class="backup-precheck-flow"><span><i>1</i>磁盘使用率检查</span><span><i>2</i>主从延迟等待 ≤ 30 秒</span><span><i>3</i>XtraBackup 全量/增量</span><span><i>4</i>完整性标记与日志</span></div></section>
              <div class="backup-switches"><label><input v-model="backupPolicyForm.include_binlog" type="checkbox">同时备份 binlog</label><label><input v-model="backupPolicyForm.enabled" type="checkbox">启用自动调度</label></div><div class="backup-editor-actions"><button type="button" class="secondary" @click="showBackupPolicyEditor=false">取消</button><button class="primary">保存备份策略</button></div>
            </form>
            <section class="cluster-workspace backup-policy-list"><div class="panel-head"><div><h3>备份策略</h3><p>机器是备份执行目标；单实例自动选择，多实例才显示端口。</p></div><span class="count">{{ backupPolicies.length }} 条</span></div><div class="backup-table-wrap"><table><thead><tr><th>策略</th><th>备份机器</th><th>类型 / 调度</th><th>安全策略</th><th>下次执行</th><th>状态</th><th>操作</th></tr></thead><tbody><tr v-for="item in backupPolicies" :key="item.id"><td><b>{{ item.name }}</b><small>{{ item.backup_location }}</small></td><td><b>{{ clusterMachineItems.find(m=>(m.ID||m.id)===item.machine_id)?.Name || item.machine_id }}</b><small>实例端口 {{ item.port }}</small></td><td><b>{{ backupTypeLabel(item.backup_type) }}</b><small>{{ backupScheduleLabel(item) }}</small></td><td><span>磁盘 &lt; {{ item.disk_usage_threshold }}%</span><small>延迟等待 30 秒 · 重试 {{ item.retry_count }}/5 次</small></td><td>{{ item.next_run_at ? date(item.next_run_at) : '—' }}</td><td><span :class="['status', item.enabled ? 'success' : 'offline']">{{ item.enabled ? '已启用' : '已停用' }}</span></td><td class="cluster-row-actions"><button class="text-button" @click="runBackupPolicy(item)">立即备份</button><button class="text-button" @click="openBackupPolicyEditor(item)">编辑</button><button class="danger-link" @click="deleteBackupPolicy(item)">删除</button></td></tr><tr v-if="!backupPolicies.length"><td colspan="7" class="empty">尚未配置备份策略。点击“新建策略”开始。</td></tr></tbody></table></div></section>
            <section class="cluster-workspace backup-run-list"><div class="panel-head"><div><h3>备份记录与执行日志</h3><p>预检、延迟等待、重试和 XtraBackup 输出均随 Agent 任务记录。</p></div><span class="count">最近 {{ backupRuns.length }} 条</span></div><div class="backup-table-wrap"><table><thead><tr><th>备份时间</th><th>备份机器 / 实例</th><th>类型</th><th>任务状态</th><th>备份目录</th><th>操作</th></tr></thead><tbody><template v-for="run in backupRuns" :key="run.id"><tr><td>{{ date(run.created_at) }}</td><td><b>{{ run.machine_name || run.machine_id }}</b><small>{{ run.machine_ip }}:{{ run.port }}</small></td><td><span class="backup-type-badge">{{ backupTypeLabel(run.backup_type) }}</span><small v-if="run.base_run_id">基础：{{ run.base_run_id }}</small></td><td><span :class="['status', state(run.status)]">{{ taskStatusLabel(run.status) }}</span><small v-if="run.last_error" class="backup-last-error">{{ errorSummary(run.last_error) }}</small></td><td><code>{{ run.backup_path }}</code></td><td><button class="text-button" @click="openTaskDetail({ID:run.task_id,id:run.task_id})">任务详情</button><button class="danger-link" :disabled="state(run.status)!=='success'" @click="restoreBackup(run)">恢复</button></td></tr></template><tr v-if="!backupRuns.length"><td colspan="6" class="empty">暂无备份执行记录。</td></tr></tbody></table></div></section>
          </section>
	          <template v-else>
	<section class="cluster-overview-dashboard topology-workspace" aria-label="集群运行概览">
	  <header class="overview-dashboard-head">
	    <div><p>CLUSTER OBSERVABILITY</p><h3>集群运行概览</h3><span>吞吐、主机资源、数据容量与复制架构统一聚合；数据来自 Agent 心跳采集。</span></div>
	    <div class="overview-dashboard-controls"><label>时间范围<select v-model.number="clusterOverviewRange" @change="changeClusterOverviewRange"><option :value="15">近 15 分钟</option><option :value="60">近 1 小时</option><option :value="360">近 6 小时</option><option :value="1440">近 24 小时</option></select></label><span :class="['overview-live-state',{loading:clusterTopologyRefreshing,waiting:clusterOverview().data_source==='waiting'}]"><i></i>{{ clusterTopologyRefreshing ? '正在聚合' : clusterOverview().data_source==='waiting' ? '等待实例数据' : '实时更新' }}</span><button type="button" class="icon-button" :disabled="clusterTopologyRefreshing" @click="refreshClusterTopology()" title="刷新概览">↻</button></div>
	  </header>
	  <div v-if="clusterTopologyError" class="topology-error">{{ errorSummary(clusterTopologyError) }}</div>
	  <template v-else>
	    <div :class="['overview-data-note',clusterOverview().data_source]"><i>{{ clusterOverview().data_source==='history' ? '✓' : ['current_estimate','host_only'].includes(clusterOverview().data_source) ? 'i' : '!' }}</i><div><b>{{ clusterOverview().data_source==='history' ? '实时趋势数据已接入' : clusterOverview().data_source==='current_estimate' ? '正在积累趋势，当前值已经展示' : clusterOverview().data_source==='host_only' ? '机器资源已接入，等待 MySQL 实例数据' : '当前集群没有可聚合的数据' }}</b><small>{{ clusterOverview().data_source==='history' ? '指标来自 Manager 保存的 Agent 心跳快照。' : clusterOverview().data_source==='current_estimate' ? 'QPS/TPS 暂按当前累计计数与运行时长计算参考值；形成两个快照后自动切换为实时区间速率。' : clusterOverview().data_source==='host_only' ? 'CPU、IO、磁盘和网络指标会直接展示；完成实例登记后自动补充 QPS、TPS、容量和拓扑。' : '请确认集群已分配机器，并且 Agent 心跳在线。' }}</small></div></div>
	    <div class="overview-kpi-grid">
	      <article class="throughput"><header><span>QPS</span><i>查询吞吐</i></header><b>{{ overviewNumber(clusterOverview().summary.qps) }}</b><small>{{ clusterOverview().data_source==='history' ? overviewRangeLabel()+' · 全实例合计' : '当前参考值 · 全实例合计' }}</small><svg viewBox="0 0 220 50" preserveAspectRatio="none" aria-hidden="true"><polyline :points="overviewChartPoints('qps',220,50)" /></svg></article>
	      <article class="transaction"><header><span>TPS</span><i>事务吞吐</i></header><b>{{ overviewNumber(clusterOverview().summary.tps) }}</b><small>提交事务 / 秒</small><svg viewBox="0 0 220 50" preserveAspectRatio="none" aria-hidden="true"><polyline :points="overviewChartPoints('tps',220,50)" /></svg></article>
	      <article class="cpu"><header><span>CPU</span><i>机器均值</i></header><b>{{ overviewNumber(clusterOverview().summary.cpu_percent) }}<em>%</em></b><small>{{ clusterOverview().summary.machine_count || 0 }} 台机器</small><div class="overview-meter"><i :style="{width:Math.min(Number(clusterOverview().summary.cpu_percent||0),100)+'%'}"></i></div></article>
	      <article class="disk"><header><span>磁盘</span><i>最高使用率</i></header><b>{{ overviewNumber(clusterOverview().summary.disk_used_percent) }}<em>%</em></b><small>所有数据与系统挂载点</small><div class="overview-meter"><i :style="{width:Math.min(Number(clusterOverview().summary.disk_used_percent||0),100)+'%'}"></i></div></article>
	    </div>

	    <div class="overview-main-grid">
	      <section class="overview-chart-panel overview-traffic-panel">
	        <header><div><b>数据库吞吐趋势</b><small>{{ overviewRangeLabel() }} · 计数器差值按采集间隔换算</small></div><div class="overview-legend"><span class="qps-key">QPS</span><span class="tps-key">TPS</span></div></header>
	        <div v-if="clusterOverview().series.length" class="overview-line-chart"><svg viewBox="0 0 520 150" preserveAspectRatio="none"><defs><linearGradient id="qps-area" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#2f7df6" stop-opacity=".22"/><stop offset="1" stop-color="#2f7df6" stop-opacity="0"/></linearGradient></defs><g class="grid"><line v-for="y in [30,60,90,120]" :key="y" x1="0" :y1="y" x2="520" :y2="y"/></g><polygon :points="overviewChartArea('qps',520,150)" fill="url(#qps-area)"/><polyline class="qps" :points="overviewChartPoints('qps',520,150)"/><polyline class="tps" :points="overviewChartPoints('tps',520,150)"/></svg><footer><span>{{ clusterOverview().series[0]?.timestamp ? date(clusterOverview().series[0].timestamp) : '—' }}</span><span>当前 QPS {{ overviewNumber(clusterOverview().summary.qps) }}</span><span>{{ clusterOverview().series.at(-1)?.timestamp ? date(clusterOverview().series.at(-1).timestamp) : '—' }}</span></footer></div>
	        <div v-else class="overview-chart-empty"><i>⌁</i><b>{{ clusterOverview().data_source==='current_estimate' ? '当前参考 QPS '+overviewNumber(clusterOverview().summary.qps)+' · TPS '+overviewNumber(clusterOverview().summary.tps) : '正在等待实例指标' }}</b><small>{{ clusterOverview().data_source==='current_estimate' ? '当前值已经可用；趋势线将在两个心跳快照形成后自动出现，无需手工刷新。' : '完成 MySQL 实例登记并确认 Agent 在线后，指标会自动出现在这里。' }}</small></div>
	      </section>
	      <section class="overview-chart-panel overview-resource-panel">
	        <header><div><b>资源与网络</b><small>当前集群所有机器聚合</small></div><span>LIVE</span></header>
	        <div class="overview-resource-list"><div><span><i class="cpu"></i>CPU 使用率</span><b>{{ overviewNumber(clusterOverview().summary.cpu_percent) }}%</b><em><i :style="{width:Math.min(Number(clusterOverview().summary.cpu_percent||0),100)+'%'}"></i></em></div><div><span><i class="io"></i>磁盘 IO 忙碌</span><b>{{ overviewNumber(clusterOverview().summary.io_busy_percent) }}%</b><em><i :style="{width:Math.min(Number(clusterOverview().summary.io_busy_percent||0),100)+'%'}"></i></em></div><div><span><i class="disk"></i>磁盘使用率</span><b>{{ overviewNumber(clusterOverview().summary.disk_used_percent) }}%</b><em><i :style="{width:Math.min(Number(clusterOverview().summary.disk_used_percent||0),100)+'%'}"></i></em></div></div>
	        <dl class="overview-io-facts"><div><dt>IO 读取</dt><dd>{{ overviewBytes(clusterOverview().summary.io_read_bytes_sec,true) }}</dd></div><div><dt>IO 写入</dt><dd>{{ overviewBytes(clusterOverview().summary.io_write_bytes_sec,true) }}</dd></div><div><dt>网络接收</dt><dd>↓ {{ overviewBytes(clusterOverview().summary.network_receive_bytes_sec,true) }}</dd></div><div><dt>网络发送</dt><dd>↑ {{ overviewBytes(clusterOverview().summary.network_transmit_bytes_sec,true) }}</dd></div></dl>
	      </section>
	    </div>

	    <div class="overview-secondary-grid">
	      <section class="overview-capacity-panel"><header><div><b>数据库容量与碎片</b><small>业务 Schema 的 data_length、index_length 与 data_free</small></div><span>每 5 分钟采集</span></header><div class="capacity-hero"><div class="capacity-ring" :style="{'--fragment':Math.min(Number(clusterOverview().summary.fragment_percent||0),100)+'%'}"><b>{{ overviewNumber(clusterOverview().summary.fragment_percent) }}%</b><small>碎片率</small></div><div><small>数据库总量（含副本）</small><b>{{ overviewBytes(Number(clusterOverview().summary.data_bytes||0)+Number(clusterOverview().summary.index_bytes||0)) }}</b><span>物理容量按所有受管实例汇总</span></div></div><dl><div><dt>表数据</dt><dd>{{ overviewBytes(clusterOverview().summary.data_bytes) }}</dd></div><div><dt>索引数据</dt><dd>{{ overviewBytes(clusterOverview().summary.index_bytes) }}</dd></div><div><dt>可回收碎片</dt><dd>{{ overviewBytes(clusterOverview().summary.fragment_bytes) }}</dd></div></dl></section>
	      <section class="overview-topology-panel"><header><div><b>架构与简易拓扑</b><small>{{ clusterOverview().summary.architecture || '正在识别' }} · {{ clusterTopology.edges.length }} 条复制链路</small></div><button type="button" class="text-button" @click="openArchitectureAdjustment">调整架构 →</button></header><div v-if="clusterTopology.nodes.length" class="overview-mini-topology"><div class="mini-entry"><i>◇</i><span>业务入口 / VIP</span></div><div class="mini-stem"></div><div class="mini-node-row roots"><article v-for="node in overviewTopologyRoots()" :key="'mini-root-'+node.ip+node.port"><i :class="{bad:node.error}"></i><div><b>{{ node.name || node.ip }}</b><small>主库 · {{ node.ip }}:{{ node.port }}</small></div></article><article v-for="node in overviewTopologyStandalone()" :key="'mini-single-'+node.ip+node.port"><i :class="{bad:node.error}"></i><div><b>{{ node.name || node.ip }}</b><small>独立 · {{ node.ip }}:{{ node.port }}</small></div></article></div><div v-if="overviewTopologyReplicas().length" class="mini-branch"></div><div v-if="overviewTopologyReplicas().length" class="mini-node-row replicas"><article v-for="node in overviewTopologyReplicas()" :key="'mini-replica-'+node.ip+node.port"><i :class="{bad:node.error}"></i><div><b>{{ node.name || node.ip }}</b><small>从库 · 来源 {{ overviewTopologySourceName(node) }}</small></div></article></div></div><div v-else class="overview-mini-empty"><span>◇</span><b>尚未部署 MySQL 实例</b><small>完成实例安装并收到心跳后，将自动生成拓扑。</small></div></section>
	    </div>

	    <section class="overview-machine-panel"><header><div><b>机器资源明细</b><small>快速定位集群内资源热点</small></div><span>{{ clusterOverview().machines.length }} 台</span></header><div class="overview-machine-table"><table><thead><tr><th>机器</th><th>CPU</th><th>IO 忙碌</th><th>磁盘</th><th>网络吞吐</th><th>Agent / 心跳</th></tr></thead><tbody><tr v-for="machine in clusterOverview().machines" :key="machine.machine_id"><td><b>{{ machine.name || machine.ip }}</b><small>{{ machine.ip }}</small></td><td>{{ overviewNumber(machine.cpu_percent) }}%</td><td>{{ overviewNumber(machine.io_busy_percent) }}%</td><td><span :class="['resource-pill',{warning:Number(machine.disk_used_percent)>=80}]">{{ overviewNumber(machine.disk_used_percent) }}%</span></td><td>{{ overviewBytes(machine.network_bytes_sec,true) }}</td><td><span :class="['status',['ONLINE','HEALTHY','OK'].includes(String(machine.status).toUpperCase())?'success':'offline']">{{ machine.status || '等待心跳' }}</span></td></tr><tr v-if="!clusterOverview().machines.length"><td colspan="6" class="empty">尚无可聚合的机器指标，请确认集群机器 Agent 已升级并在线。</td></tr></tbody></table></div></section>
	  </template>
	</section>
	<section v-if="false" class="cluster-workspace topology-workspace live-topology-workspace">
  <div class="panel-head live-topology-head"><div><h3>MySQL 实例与主从拓扑</h3><p>主节点位于根层，从节点按实际复制源位于叶子层；运行数据每 5 秒自动刷新。</p></div><div class="live-topology-controls"><span :class="['live-dot',{refreshing:clusterTopologyRefreshing}]"><i></i>{{ clusterTopologyRefreshing ? '正在刷新' : '实时数据' }}</span><small>更新于 {{ clusterTopologyLastUpdated ? date(clusterTopologyLastUpdated) : '—' }}</small><button type="button" :class="['auto-refresh-toggle',{active:clusterTopologyAutoRefresh}]" @click="toggleClusterTopologyAutoRefresh">{{ clusterTopologyAutoRefresh ? '自动刷新已开启' : '自动刷新已暂停' }}</button><button type="button" class="icon-button" :disabled="clusterTopologyRefreshing" @click="refreshClusterTopology()" title="立即刷新">↻</button></div></div>
  <div v-if="clusterTopologyError" class="topology-error">{{ errorSummary(clusterTopologyError) }}</div>
  <div v-else-if="!clusterTopology.nodes.length" class="topology-empty topology-empty-rich"><div class="topology-empty-copy"><span class="topology-empty-icon">⌁</span><div><b>尚未发现 MySQL 实例</b><small>请前往“机器管理”将机器分配至集群，再为机器创建 MySQL 安装任务。</small></div></div><div class="topology-next-steps"><span><i>1</i>在机器管理中分配机器</span><span><i>2</i>创建 MySQL 安装任务</span><span><i>3</i>等待实例心跳上报</span></div><div class="topology-empty-actions"><button class="secondary" @click="active='machines'; closeClusterDetail()">前往机器管理</button><button class="primary" @click="active='mysql'; closeClusterDetail()">前往 MySQL 安装</button></div></div>
  <div v-else class="live-topology-canvas">
    <section class="live-entry-card"><span>◇</span><div><b>业务访问入口</b><small>应用连接 / VIP / 读写流量</small></div><em>LIVE</em></section>
    <div v-if="overviewTopologyRoots().length" class="live-topology-stem"></div>
    <section v-if="overviewTopologyRoots().length" class="live-topology-level live-root-level"><header><b>根节点 · 主库</b><small>{{ overviewTopologyRoots().length > 1 ? overviewTopologyRoots().length + ' 个可写根节点' : '当前写入主节点' }}</small></header><div class="live-node-row">
      <article v-for="node in overviewTopologyRoots()" :key="'root-'+node.ip+':'+node.port" :class="['live-node-card','root',{error:node.error}]"><header><span class="live-db-glyph">◉</span><em>{{ mysqlRoleLabel(node.role) }}</em><i :class="{bad:node.error}"></i></header><div class="live-node-identity"><b>{{ node.name || node.ip }}</b><small>{{ node.ip }}:{{ node.port }} · server_id {{ node.server_id || '—' }}</small></div><div class="live-node-metrics"><span><small>QPS</small><b>{{ topologyMetric(node.qps) }}</b></span><span><small>TPS</small><b>{{ topologyMetric(node.tps) }}</b></span><span><small>连接</small><b>{{ topologyMetric(node.connections) }}</b></span><span><small>心跳</small><b>{{ node.heartbeat || '—' }}</b></span></div><footer><span>{{ node.read_only==='true'||node.read_only==='ON' ? '只读' : '可写' }}</span><small>{{ node.last_updated || '等待动态数据' }}</small></footer></article>
    </div></section>
    <div v-if="overviewTopologyRoots().length && overviewTopologyReplicas().length" class="live-branch"><i></i><span></span></div>
    <section v-if="overviewTopologyReplicas().length" class="live-topology-level live-leaf-level"><header><b>叶子节点 · 从库</b><small>按复制源挂载 · {{ overviewTopologyReplicas().length }} 个副本</small></header><div class="live-node-row">
      <article v-for="node in overviewTopologyReplicas()" :key="'replica-'+node.ip+':'+node.port" :class="['live-node-card','leaf',{error:node.error,lagging:Number(overviewTopologyEdgeForReplica(node)?.lag||0)>30}]"><header><span class="live-db-glyph">◉</span><em>{{ mysqlRoleLabel(node.role) }}</em><i :class="{bad:node.error}"></i></header><div class="live-node-identity"><b>{{ node.name || node.ip }}</b><small>{{ node.ip }}:{{ node.port }} · 来源 {{ overviewTopologySourceName(node) }}</small></div><div class="live-node-metrics"><span><small>延迟</small><b>{{ topologyMetric(overviewTopologyEdgeForReplica(node)?.lag,'s') }}</b></span><span><small>IO 线程</small><b>{{ overviewTopologyEdgeForReplica(node)?.io_running || '—' }}</b></span><span><small>SQL 线程</small><b>{{ overviewTopologyEdgeForReplica(node)?.sql_running || '—' }}</b></span><span><small>QPS</small><b>{{ topologyMetric(node.qps) }}</b></span></div><footer><span>{{ Number(overviewTopologyEdgeForReplica(node)?.lag||0)>30 ? '复制延迟' : '复制正常' }}</span><small>{{ node.last_updated || '等待动态数据' }}</small></footer></article>
    </div></section>
    <section v-if="overviewTopologyStandalone().length" class="live-topology-level live-standalone-level"><header><b>独立实例</b><small>尚未建立复制关系</small></header><div class="live-node-row"><article v-for="node in overviewTopologyStandalone()" :key="'standalone-'+node.ip+':'+node.port" :class="['live-node-card','standalone',{error:node.error}]"><header><span class="live-db-glyph">◉</span><em>独立</em><i :class="{bad:node.error}"></i></header><div class="live-node-identity"><b>{{ node.name || node.ip }}</b><small>{{ node.ip }}:{{ node.port }} · server_id {{ node.server_id || '—' }}</small></div><div class="live-node-metrics"><span><small>QPS</small><b>{{ topologyMetric(node.qps) }}</b></span><span><small>TPS</small><b>{{ topologyMetric(node.tps) }}</b></span><span><small>连接</small><b>{{ topologyMetric(node.connections) }}</b></span><span><small>心跳</small><b>{{ node.heartbeat || '—' }}</b></span></div><footer><span>未配置复制</span><small>{{ node.last_updated || '等待动态数据' }}</small></footer></article></div></section>
    <div class="live-edge-list"><header><b>实时复制链路</b><span>{{ clusterTopology.edges.length }} 条</span></header><div><p v-for="edge in clusterTopology.edges" :key="edge.source_ip+':'+edge.source_port+'>'+edge.target_ip+':'+edge.target_port" :class="{bad:edge.io_running!=='Yes'||edge.sql_running!=='Yes'}"><span><b>{{ edge.source_name || edge.source_ip }}</b><small>{{ edge.source_ip }}:{{ edge.source_port }}</small></span><i>→</i><span><b>{{ edge.target_name || edge.target_ip }}</b><small>{{ edge.target_ip }}:{{ edge.target_port }}</small></span><em>{{ edge.io_running || '—' }}/{{ edge.sql_running || '—' }} · 延迟 {{ topologyMetric(edge.lag,'s') }}</em></p><small v-if="!clusterTopology.edges.length">当前没有复制链路，实例按独立节点展示。</small></div></div>
  </div>
</section>
          <section class="cluster-workspace member-overview cluster-machine-mysql"><div class="panel-head"><div><h3>集群机器管理</h3><p>仅管理当前集群的机器成员、连接信息和 Agent 状态；移出集群不会删除机器、Agent 或 MySQL 数据。</p></div><div class="cluster-machine-actions"><span>共 {{ clusterMachineTotal }} 台机器</span><span v-if="selectedClusterOperationMachineIDs.length">已选 {{ selectedClusterOperationMachineIDs.length }} 台</span><button v-if="clusterMachineItems.length" type="button" class="secondary" @click="toggleClusterMachinePageSelection">{{ clusterMachinePageSelected() ? '取消当前页全选' : '当前页全选' }}</button><button type="button" class="danger-button" :disabled="!selectedClusterOperationMachineIDs.length" @click="removeSelectedMachinesFromCluster">批量移出</button><button class="primary" @click="openClusterMembers(selectedClusterDetail)">＋ 添加机器</button></div></div><div class="cluster-machine-table-wrap"><table v-if="clusterMachineItems.length" class="cluster-machine-table"><thead><tr><th class="machine-check-column"><input type="checkbox" :checked="clusterMachinePageSelected()" aria-label="选择当前页全部机器" @change="toggleClusterMachinePageSelection"></th><th class="machine-column">机器</th><th class="network-column">网卡 / IP 地址</th><th class="architecture-column">架构</th><th class="ssh-column">SSH</th><th class="agent-column">Agent</th><th class="credential-column">凭证</th><th class="actions-column">操作</th></tr></thead><tbody><tr v-for="machine in clusterMachineItems" :key="machine.ID || machine.id" :class="{selected:selectedClusterOperationMachineIDs.includes(machine.ID || machine.id)}"><td class="machine-check-column"><input v-model="selectedClusterOperationMachineIDs" type="checkbox" :value="machine.ID || machine.id" :aria-label="'选择机器 '+(machine.Name || machine.name)"></td><td class="machine-column"><button class="machine-name-link" @click="showMachine(machine.ID || machine.id)" :title="machine.Name || machine.name">{{ machine.Name || machine.name }}</button><small :title="machine.ID || machine.id">{{ machine.ID || machine.id }}</small></td><td class="network-column"><div class="machine-network-list"><div v-for="item in clusterMachineInterfaces(machine)" :key="item.name + item.ip" class="machine-network-item"><span>{{ item.name }}</span><b>{{ item.ip }}</b></div></div></td><td class="architecture-column">{{ machine.Architecture || machine.architecture || machine.Arch || machine.arch || '待采集' }}</td><td class="ssh-column"><b>{{ machine.SSHUser || machine.ssh_user || 'root' }}</b><small>端口 {{ machine.SSHPort || machine.ssh_port || 22 }}</small></td><td class="agent-column"><span :class="['status', machineStatus(machine).code]">{{ machineStatus(machine).label }}</span></td><td class="credential-column">{{ data.credentials.find(item => (item.id || item.ID) === (machine.CredentialID || machine.credential_id))?.name || ((machine.CredentialID || machine.credential_id) ? '已关联' : '未分配') }}</td><td class="cluster-row-actions actions-column"><button class="text-button" @click="showMachine(machine.ID || machine.id)">详情 / 编辑</button><button class="danger-link" @click="removeMachineFromCluster(machine)">移出集群</button></td></tr></tbody></table><div v-else class="member-summary">当前集群暂无机器。点击“添加机器”从已纳管机器中选择。</div></div><div v-if="clusterMachineTotal" class="cluster-pager"><span>第 {{ clusterMachinePage }} 页 · 共 {{ clusterMachineTotal }} 台机器</span><div><button class="secondary" :disabled="clusterMachinePage===1" @click="changeClusterMachinePage(-1)">上一页</button><button class="secondary" :disabled="clusterMachinePage*50>=clusterMachineTotal" @click="changeClusterMachinePage(1)">下一页</button></div></div></section>
          </template>
        </template>
        <template v-else>
          <div class="cluster-hero"><div><p>CLUSTER OPERATIONS</p><h2>集群列表</h2><span>以集群为运维边界，集中处理机器、MySQL 实例、拓扑、备份与生命周期操作。</span></div><div class="cluster-hero-actions"><button class="primary" @click="openCreateCluster">＋ 创建集群</button><button class="secondary" @click="active='machines'">管理机器</button></div></div>
          <div class="cluster-summary"><article><small>集群总数</small><b>{{ clusterTotal || data.clusters.length }}</b></article><article><small>已分配机器</small><b>{{ data.clusters.reduce((total, item) => total + clusterMachineCount(item), 0) }}</b></article><article><small>在线 Agent</small><b>{{ data.agents.filter(item => agentStatus(item).code === 'agent_online' && machineCluster(item) !== '未分配集群').length }}</b></article></div>
          <section class="cluster-workspace"><div class="panel-head"><div><h3>集群列表</h3><p>按名称或描述检索；每行提供机器数、实例数和状态摘要。</p></div><form class="cluster-search" @submit.prevent="searchClusterPage"><input v-model.trim="clusterKeyword" placeholder="搜索集群名称或描述"><button class="secondary">查询</button><button type="button" class="icon-button" @click="refresh">↻</button></form></div><table v-if="clusterPageItems.length" class="cluster-table"><thead><tr><th>集群</th><th>描述</th><th>集群机器</th><th>MySQL 实例</th><th>在线 Agent</th><th>创建时间</th><th></th></tr></thead><tbody><tr v-for="item in clusterPageItems" :key="item.Name || item.name" @click="openClusterDetail(item)" class="cluster-row"><td><b>{{ item.Name || item.name }}</b></td><td><small>{{ item.Description || item.description || '—' }}</small></td><td>{{ clusterMachineCount(item) }}</td><td>{{ data.mysqlInstances.filter(instance => (instance.Cluster || instance.cluster) === (item.Name || item.name)).length }}</td><td>{{ clusterAgentCount(item) }}</td><td>{{ date(item.CreatedAt || item.created_at) }}</td><td><button class="text-button" @click.stop="openClusterDetail(item)">进入管理 →</button></td></tr></tbody></table><div v-else class="cluster-empty"><b>{{ clusterKeyword ? '未找到匹配集群' : '尚未创建集群' }}</b><small>{{ clusterKeyword ? '请修改搜索条件，或清空条件后重试。' : '先创建一个逻辑资源边界，再把已纳管机器分配进去。' }}</small><button v-if="!clusterKeyword" class="primary" @click="openCreateCluster">创建第一个集群</button><button v-else class="secondary" @click="clusterKeyword='';searchClusterPage()">清空搜索</button></div><div v-if="clusterTotal" class="cluster-pager"><span>第 {{ clusterPage }} 页 · 共 {{ clusterTotal }} 个集群</span><div><button class="secondary" :disabled="clusterPage===1" @click="changeClusterPage(-1)">上一页</button><button class="secondary" :disabled="clusterPage*12>=clusterTotal" @click="changeClusterPage(1)">下一页</button></div></div></section>
        </template>
      </section>
      <div v-if="data.restoreDialog" class="modal-mask recovery-modal-mask" @click.self="data.restoreDialog=null">
        <form class="modal recovery-modal" @submit.prevent="data.submitRestore()">
          <div class="modal-head"><div><p>MYSQL DATA RECOVERY</p><h2>恢复数据 · {{ data.restoreDialog.machine_name || data.restoreDialog.machine_id }}</h2></div><button type="button" @click="data.restoreDialog=null">×</button></div>
          <div class="recovery-target"><div><small>实例地址</small><b>{{ data.restoreDialog.machine_ip }}:{{ data.restoreDialog.port }}</b></div><div><small>关联备份</small><b>{{ backupTypeLabel(data.restoreDialog.backup_type) }} · {{ date(data.restoreDialog.created_at) }}</b></div></div>
          <div class="recovery-mode-grid">
            <label :class="{active:data.restoreForm.mode==='flashback'}"><input v-model="data.restoreForm.mode" type="radio" value="flashback"><b>数据闪回</b><small>默认使用 bin2sql 生成反向 SQL，不停库。</small></label>
            <label :class="{active:data.restoreForm.mode==='point_in_time'}"><input v-model="data.restoreForm.mode" type="radio" value="point_in_time"><b>按时间点恢复</b><small>XtraBackup 物理恢复后回放 binlog 到指定时间。</small></label>
            <label :class="{active:data.restoreForm.mode==='physical'}"><input v-model="data.restoreForm.mode" type="radio" value="physical"><b>全量物理恢复</b><small>使用当前全量/增量链覆盖目标实例数据。</small></label>
          </div>
          <div v-if="data.restoreForm.mode==='flashback'" class="recovery-risk amber"><b>在线闪回风险</b><p>bin2sql 依赖 ROW 格式且 binlog_row_image=FULL。默认只生成回滚 SQL；直接执行可能与当前数据冲突，请先审阅 SQL 并在低峰操作。</p></div>
          <div v-else class="recovery-risk red"><b>高风险：恢复期间数据库将暂停使用</b><p>系统会停止 MySQL、保留旧数据目录并执行物理恢复。按时间点恢复还会继续回放 binlog；请先确认业务已停止写入。</p></div>
          <div class="recovery-form-grid">
            <label v-if="data.restoreForm.mode!=='flashback'" class="wide">恢复文件位置<input v-model.trim="data.restoreForm.backup_path" required placeholder="/data/gmha/backups/..."><small>默认使用本条记录及其增量链；手动改路径时按独立全量备份处理。</small></label>
            <label v-if="data.restoreForm.mode!=='physical'">恢复时间<input v-model="data.restoreForm.restore_time" type="datetime-local" required><small>{{ data.restoreForm.mode==='flashback' ? '回滚此时间之后的变更' : '恢复至该时间点' }}</small></label>
            <label v-if="data.restoreForm.mode==='flashback'">闪回 SQL 保存位置<input v-model.trim="data.restoreForm.output_dir" required placeholder="/data/gmha/recovery"></label>
            <label v-if="data.restoreForm.mode==='flashback'">限定数据库（可选）<input v-model.trim="data.restoreForm.database" placeholder="留空表示全部数据库"></label>
            <label v-if="data.restoreForm.mode==='flashback'">限定数据表（可选）<input v-model.trim="data.restoreForm.tables" placeholder="orders,users"></label>
            <label>MySQL 管理账号<input v-model.trim="data.restoreForm.mysql_user" required></label>
            <label>MySQL 管理密码<input v-model="data.restoreForm.mysql_password" type="password" autocomplete="new-password" placeholder="用于 binlog 回放或主从修复"></label>
          </div>
          <div class="recovery-options">
            <label v-if="data.restoreForm.mode!=='flashback'"><input v-model="data.restoreForm.repair_replication" type="checkbox">恢复后启动复制，并使用 Percona Toolkit（pt-table-sync）校验与修复主从数据</label>
            <label v-else><input v-model="data.restoreForm.apply_flashback" type="checkbox">生成后立即执行回滚 SQL（高风险，建议保持关闭并先审阅文件）</label>
          </div>
          <label class="recovery-confirm">二次确认<input v-model.trim="data.restoreForm.confirmation" required :placeholder="data.restoreExpected()"><small>请输入：<code>{{ data.restoreExpected() }}</code></small></label>
          <div class="modal-actions"><button type="button" class="secondary" @click="data.restoreDialog=null">取消</button><button :class="data.restoreForm.mode==='flashback' && !data.restoreForm.apply_flashback ? 'primary' : 'danger-button'">{{ data.restoreForm.mode==='flashback' && !data.restoreForm.apply_flashback ? '生成闪回 SQL' : '创建恢复任务' }}</button></div>
        </form>
      </div>
      <div v-if="showClusterMembers && selectedClusterForMembers" class="modal-mask cluster-members-mask" @click.self="showClusterMembers=false"><section class="modal cluster-members-modal"><div class="modal-head"><div><p>集群机器</p><h2>添加机器到 {{ selectedClusterForMembers.Name || selectedClusterForMembers.name }}</h2></div><button type="button" @click="showClusterMembers=false">×</button></div><p class="form-note">选择机器后会调用与 CLI 完全相同的集群分配流程。机器从其他集群迁入时，其归属将被更新。</p><div class="member-list"><div v-if="clusterCandidatesLoading" class="cluster-candidates-loading"><i></i><span><b>正在加载可选机器</b><small>读取已纳管机器及当前集群归属…</small></span></div><div v-else-if="clusterCandidatesError" class="cluster-candidates-error"><b>候选机器加载失败</b><small>{{ errorSummary(clusterCandidatesError, 180) }}</small><button type="button" class="secondary" @click="changeClusterCandidatePage(0)">重新加载</button></div><label v-for="machine in clusterCandidates" :key="machine.ID || machine.id" class="member-choice"><input type="checkbox" :value="machine.ID || machine.id" v-model="selectedClusterMachineIDs" :disabled="machineCluster(machine) === (selectedClusterForMembers.Name || selectedClusterForMembers.name)"><span><b>{{ machine.Name || machine.name }}</b><small>{{ machine.IP || machine.ip }} · {{ machineStatus(machine).label }}</small></span><em :class="['cluster-label', {unassigned: machineCluster(machine) === '未分配集群'}]">{{ machineCluster(machine) }}</em></label><p v-if="!clusterCandidatesLoading && !clusterCandidatesError && !clusterCandidates.length" class="cluster-candidates-empty"><b>暂无可选机器</b><small>请先在“机器与凭证”中纳管机器，或检查筛选页码。</small></p></div><div class="member-pager"><button class="secondary" :disabled="clusterCandidatesLoading || clusterCandidatePage===1" @click="changeClusterCandidatePage(-1)">上一页</button><span>第 {{ clusterCandidatePage }} 页 · 共 {{ clusterCandidateTotal }} 台</span><button class="secondary" :disabled="clusterCandidatesLoading || clusterCandidatePage*pageSize>=clusterCandidateTotal" @click="changeClusterCandidatePage(1)">下一页</button></div><section v-if="clusterMemberAssignResult" class="member-result"><b>分配结果</b><p v-for="item in (clusterMemberAssignResult.results || [])" :key="item.machine_id"><span :class="['status', item.success ? 'success' : 'error']">{{ item.success ? '成功' : '失败' }}</span>{{ item.machine_id }}<small v-if="item.error">{{ item.error }}</small></p></section><div class="modal-actions"><button type="button" class="secondary" @click="showClusterMembers=false">取消</button><button class="primary" :disabled="clusterCandidatesLoading || !selectedClusterMachineIDs.length" @click="assignClusterMembers">添加 {{ selectedClusterMachineIDs.length ? selectedClusterMachineIDs.length + ' 台机器' : '' }}</button></div></section></div>
<div v-if="showClusterEditor" class="modal-mask" @click.self="showClusterEditor=false"><form class="modal cluster-editor" @submit.prevent="saveCluster"><div class="modal-head"><div><p>集群管理</p><h2>{{ clusterForm.old_name ? '修改集群' : '创建集群' }}</h2></div><button type="button" @click="showClusterEditor=false">×</button></div><label>集群名称<input v-model.trim="clusterForm.name" required placeholder="mysql-prod-a"></label><label>集群描述<textarea v-model.trim="clusterForm.description" rows="4" placeholder="例如：生产订单库主从集群"></textarea></label><p class="form-note">创建后可在机器详情中分配机器；重命名会同步更新已有成员的集群归属。</p><div class="modal-actions"><button type="button" class="secondary" @click="showClusterEditor=false">取消</button><button class="primary">{{ clusterForm.old_name ? '保存修改' : '创建集群' }}</button></div></form></div>
      <div v-if="showClusterCleanup && clusterCleanupResult" class="modal-mask"><section class="modal cluster-cleanup-modal"><div class="modal-head"><div><p>集群一键清理</p><h2>{{ clusterCleanupResult.Cluster || clusterCleanupResult.cluster }}</h2></div><button type="button" @click="showClusterCleanup=false">×</button></div><div class="cleanup-summary"><b>已处理 {{ (clusterCleanupResult.Items || clusterCleanupResult.items || []).length }} 台机器</b><span :class="['status', (clusterCleanupResult.Failed || clusterCleanupResult.failed) ? 'error' : 'success']">{{ (clusterCleanupResult.Failed || clusterCleanupResult.failed) ? '失败 ' + (clusterCleanupResult.Failed || clusterCleanupResult.failed) + ' 台' : '全部完成' }}</span></div><div class="cleanup-result"><article v-for="item in (clusterCleanupResult.Items || clusterCleanupResult.items || [])" :key="item.MachineID || item.machine_id"><div><b>{{ item.Name || item.name }}</b><small>{{ item.IP || item.ip }}</small></div><span>{{ (item.MySQLPorts || item.mysql_ports || []).length ? 'MySQL：' + (item.MySQLPorts || item.mysql_ports || []).join('、') : '无 MySQL 实例' }}</span><span>{{ (item.AgentUninstalled ?? item.agent_uninstalled) ? 'Agent 已卸载' : 'Agent 未卸载' }}</span><span>{{ (item.LocalCleaned ?? item.local_cleaned) ? '本地记录已清理' : '本地记录未清理' }}</span><small v-if="item.Error || item.error" class="cleanup-error">{{ errorSummary(item.Error || item.error, 140) }}</small></article></div><div class="modal-actions"><button class="primary" @click="showClusterCleanup=false">完成</button></div></section></div>
      <div v-if="agentActionDialog" class="modal-mask agent-action-mask" @click.self="!agentActionSubmitting && closeAgentAction(true)"><form class="modal agent-action-modal" @submit.prevent="submitAgentAction"><div class="modal-head"><div><p>Agent 运维操作</p><h2>{{ agentActionDialog.title }}</h2></div><button type="button" :disabled="agentActionSubmitting" @click="closeAgentAction(true)">×</button></div><div class="agent-action-target"><span>目标机器</span><b>{{ agentActionDialog.name }}</b><small>{{ agentActionDialog.ip }}</small></div><p :class="['agent-action-description', { danger: agentActionDialog.danger }]">{{ agentActionDialog.description }}</p><section v-if="agentActionSubmitting" class="agent-action-running"><div><i></i><span><b>{{ agentActionDialog.type === 'upgrade' ? '正在升级 Agent' : '正在执行操作' }}</b><small>{{ agentActionDialog.type === 'upgrade' ? '正在通过 SSH 替换程序、重启 systemd 服务并等待新心跳，请勿重复点击。' : '请求已经提交，请等待执行结果。' }}</small></span><time>{{ agentActionElapsed }} 秒</time></div><progress></progress></section><p v-if="agentActionError" class="agent-action-inline-error"><b>操作失败</b><span>{{ agentActionError }}</span><small>完整执行日志请在任务中心查看；可修正问题后再次提交。</small></p><label v-if="agentActionDialog.inputLabel" class="agent-action-input">{{ agentActionDialog.inputLabel }}<input v-model="agentActionInput" :placeholder="agentActionDialog.expected || ''" type="text" :disabled="agentActionSubmitting" autofocus><small v-if="agentActionDialog.expected">请输入：<code>{{ agentActionDialog.expected }}</code></small></label><div class="modal-actions"><button type="button" class="secondary" :disabled="agentActionSubmitting" @click="closeAgentAction(true)">取消</button><button :class="agentActionDialog.danger ? 'danger-button' : 'primary'" :disabled="agentActionSubmitting">{{ agentActionSubmitting ? (agentActionDialog.type === 'upgrade' ? '升级中，请稍候…' : '处理中…') : agentActionDialog.confirm }}</button></div></form></div>
      <section v-if="active === 'manager'" class="panel manager-upgrade-center"><div class="manager-upgrade-head"><div><span>MANAGER VERSION CONTROL</span><h3>Manager 版本升级</h3><p>运行控制与自身版本升级统一在 Manager 控制台管理。</p></div><button type="button" class="secondary" @click="active='packages';packageForm.category='gmha-manager'">管理 Manager 制品</button></div><div v-if="upgradeOverview.manager_packages.length" class="manager-upgrade-body"><div class="upgrade-package-list"><label v-for="pkg in upgradeOverview.manager_packages" :key="pkg.name" :class="['upgrade-package-option',{selected:upgradeForm.manager_package===pkg.name,blocked:pkg.relation!=='upgrade'}]"><input v-model="upgradeForm.manager_package" type="radio" :value="pkg.name"><span><b>{{ pkg.version || '版本未知' }}</b><small>{{ pkg.name }}</small><em>{{ pkg.arch }} · {{ packageSize(pkg.size) }} · SHA {{ packageChecksum(pkg.sha256) }}</em></span><strong :class="pkg.relation">{{ pkg.relation==='upgrade' ? '可升级' : pkg.relation==='current' ? '当前版本' : pkg.relation==='downgrade' ? '低于当前版本' : '无法比较' }}</strong></label></div><section class="upgrade-version-decision"><div><small>当前运行版本</small><b>{{ upgradeOverview.manager_version || data.manager.version || '未知' }}</b></div><i>→</i><div><small>目标制品版本</small><b>{{ selectedManagerUpgradePackage()?.version || '尚未选择' }}</b></div><span :class="selectedManagerUpgradePackage()?.relation || 'unselected'">{{ !selectedManagerUpgradePackage() ? '等待选择制品' : selectedManagerUpgradePackage().relation==='upgrade' ? '版本检查通过' : selectedManagerUpgradePackage().relation==='current' ? '无需重复升级' : selectedManagerUpgradePackage().relation==='downgrade' ? '禁止降级' : '版本格式无法识别' }}</span></section><div class="upgrade-steps"><span><i>1</i>运行环境预检</span><b>→</b><span><i>2</i>候选程序验版</span><b>→</b><span><i>3</i>备份当前程序</span><b>→</b><span><i>4</i>原子替换</span><b>→</b><span><i>5</i>重启健康检查</span></div><footer class="upgrade-submit-bar"><div><b>升级会短暂中断 Manager 服务</b><small>当前程序：{{ upgradeOverview.storage?.manager_executable || '运行中的 gmha 文件' }}；失败时自动恢复同目录备份。</small></div><button type="button" class="primary" :disabled="upgradeSubmitting || selectedManagerUpgradePackage()?.relation!=='upgrade' || !data.manager.running" @click="startManagerUpgrade">{{ upgradeSubmitting ? '正在提交…' : '确认升级 Manager' }}</button></footer></div><div v-else class="agent-upgrade-empty"><span>▣</span><div><b>尚未上传 Manager 升级制品</b><small>上传带版本号的 Manager Linux 可执行文件后，这里会显示当前版本、目标版本和升级关系。</small><code>{{ upgradeOverview.storage?.manager_package_dir || 'software/gmha-manager' }}</code></div><button type="button" class="primary" @click="active='packages';packageForm.category='gmha-manager'">上传 Manager 制品</button></div><details class="component-upgrade-history"><summary>最近 Manager 升级记录 <span>{{ upgradeJobs.filter(item=>item.component==='manager').length }} 条</span></summary><div><p v-for="job in upgradeJobs.filter(item=>item.component==='manager').slice(0,5)" :key="job.id"><b>{{ job.current_version }} → {{ job.target_version }}</b><small>{{ date(job.created_at) }} · {{ job.package_name }}</small><span :class="['status',job.status]">{{ upgradeStatusLabel(job.status) }}</span></p><p v-if="!upgradeJobs.some(item=>item.component==='manager')" class="empty">暂无 Manager 升级记录。</p></div></details></section>
      <div v-if="showBatchOnboard" class="modal-mask machine-bulk-mask" @click.self="!batchOnboardRunning && (showBatchOnboard=false)">
        <form class="modal batch-onboard-modal" @submit.prevent="submitBatchOnboard">
          <div class="modal-head"><div><p>批量机器纳管</p><h2>一次添加多台机器</h2></div><button type="button" :disabled="batchOnboardRunning" @click="showBatchOnboard=false">×</button></div>
		  <section class="batch-onboard-shared"><header><b>共用连接配置</b><small>每台机器使用相同的 SSH 用户与凭证</small></header><div><label>SSH 端口<input v-model.number="batchOnboardShared.ssh_port" type="number" min="1" required></label><label>SSH 用户<input v-model.trim="batchOnboardShared.ssh_user" required></label><label>已有凭证<select v-model="batchOnboardShared.credential_id" @change="batchOnboardCredentialChanged"><option value="">直接输入密码</option><option v-for="item in data.credentials" :key="item.id || item.ID" :value="item.id || item.ID">{{ item.name || item.Name }}</option></select></label><label v-if="!batchOnboardShared.credential_id">SSH 密码<input v-model="batchOnboardShared.ssh_password" type="password" autocomplete="new-password" required></label></div><div class="batch-preserve-options"><label><input v-model="batchOnboardShared.preserve_agent" type="checkbox"><span><b>保留已有 Agent</b><small>优先接管；旧 Agent 无法启动时自动用当前版本修复，不影响 MySQL。</small></span></label><label><input v-model="batchOnboardShared.preserve_mysql" type="checkbox"><span><b>保留已有 MySQL</b><small>不停止服务或删除数据。</small></span></label></div></section>
          <section class="batch-concurrency-control"><label><input v-model="batchOnboardShared.concurrent" type="checkbox" :disabled="batchOnboardRunning"><span><b>启用并发纳管</b><small>多台机器同时执行，单台失败不会阻断其他机器。</small></span></label><label>最大并发数<select v-model.number="batchOnboardShared.concurrency" :disabled="batchOnboardRunning || !batchOnboardShared.concurrent"><option :value="2">2 台</option><option :value="3">3 台（推荐）</option><option :value="5">5 台</option><option :value="10">10 台</option></select></label><em>{{ batchOnboardShared.concurrent ? '并发模式' : '串行模式' }}</em></section>
          <section class="batch-onboard-machines"><header><div><b>机器清单</b><small>填写机器名称和管理 IP</small></div><button type="button" class="text-button" :disabled="batchOnboardRunning" @click="addBatchOnboardRow">＋ 添加一行</button></header><div class="batch-onboard-row" v-for="(row,index) in batchOnboardRows" :key="index"><span>{{ index + 1 }}</span><input v-model.trim="row.name" placeholder="机器名称，例如 mysql-prod-01" :disabled="batchOnboardRunning"><input v-model.trim="row.ip" placeholder="管理 IP，例如 10.0.0.11" :disabled="batchOnboardRunning"><button type="button" class="danger-link" :disabled="batchOnboardRunning || batchOnboardRows.length===1" @click="removeBatchOnboardRow(index)">移除</button></div></section>
		  <section v-if="batchOnboardResults.length" class="machine-bulk-progress"><article v-for="item in batchOnboardResults" :key="item.name+item.ip"><header><div><b>{{ item.name }}</b><small>{{ item.ip }}</small></div><span :class="['status',item.status]">{{ item.status==='running' ? '执行中' : item.status==='success' ? '成功' : item.status==='failed' ? '失败' : '等待' }}</span></header><ol><li v-for="step in item.steps" :key="step.key" :class="step.state"><i>{{ step.state==='success' ? '✓' : step.state==='failed' ? '!' : step.state==='running' ? '…' : step.state==='skipped' ? '—' : '○' }}</i><span><b>{{ step.title }}</b><small>{{ step.detail }}</small></span></li></ol><p :class="{failed:item.status==='failed'}">{{ errorSummary(item.message, 360) }}</p><small v-if="item.status==='failed'" class="bulk-log-hint">机器登记记录已保留。请根据失败步骤修复 SSH、systemd 或 Agent 配置后重试纳管。</small></article></section>
          <div class="modal-actions"><button type="button" class="secondary" :disabled="batchOnboardRunning" @click="showBatchOnboard=false">关闭</button><button class="primary" :disabled="batchOnboardRunning">{{ batchOnboardRunning ? (batchOnboardShared.concurrent ? '并发纳管中…' : '串行纳管中…') : '开始批量纳管' }}</button></div>
        </form>
      </div>
      <div v-if="showBulkDelete" class="modal-mask machine-bulk-mask" @click.self="!bulkDeleteRunning && (showBulkDelete=false)">
        <form class="modal bulk-delete-modal" @submit.prevent="submitBulkDelete">
          <div class="modal-head"><div><p>批量危险操作</p><h2>删除 {{ selectedMachineIDs.length }} 台机器</h2></div><button type="button" :disabled="bulkDeleteRunning" @click="showBulkDelete=false">×</button></div>
          <div class="bulk-delete-summary"><b>已选择 {{ selectedMachineIDs.length }} 台机器</b><small>每台机器独立执行完整清理流程；单台失败不会中断其他机器，失败项会保持选中。</small></div>
          <section v-if="bulkDeleteClusterMembers().length" class="machine-cluster-reminder"><div><b>请先将机器移出集群</b><p>以下 {{ bulkDeleteClusterMembers().length }} 台机器仍有集群归属，删除前必须先在集群管理中执行“移出集群”。</p><small>{{ bulkDeleteClusterSummary() }}</small></div><button type="button" class="secondary" @click="leaveBulkDeleteForClusters">前往集群管理</button></section>
          <section class="batch-concurrency-control"><label><input v-model="bulkDeleteForm.concurrent" type="checkbox" :disabled="bulkDeleteRunning"><span><b>启用并发删除</b><small>同时清理多台机器，每台机器内部仍严格按 MySQL → Agent → 平台记录执行。</small></span></label><label>最大并发数<select v-model.number="bulkDeleteForm.concurrency" :disabled="bulkDeleteRunning || !bulkDeleteForm.concurrent"><option :value="2">2 台</option><option :value="3">3 台（推荐）</option><option :value="5">5 台</option><option :value="10">10 台</option></select></label><em>{{ bulkDeleteForm.concurrent ? '并发模式' : '串行模式' }}</em></section>
		  <div class="delete-mode-picker"><label><input v-model="bulkDeleteForm.mode" type="radio" value="detach"><span><b>仅从平台剔除</b><small>不连接目标机器，不处理 Agent、MySQL、systemd 或数据目录。</small></span></label><label><input v-model="bulkDeleteForm.mode" type="radio" value="cleanup"><span><b>远端清理后删除</b><small>MySQL 优先使用在线 Agent，必要时回退 SSH；Agent 自身卸载必须使用已保存的 SSH 凭证。</small></span></label></div>
          <div v-if="bulkDeleteForm.mode==='cleanup'" class="machine-delete-options"><label><input v-model="bulkDeleteForm.delete_mysql" type="checkbox" :disabled="bulkDeleteRunning"><span><b>探测并删除每台机器上的 MySQL 与数据</b><small>逐台通过 Agent/SSH 检查真实的 systemd 服务、mysqld 进程、配置与数据路径，不以平台是否登记作为判断依据。</small></span></label><label><input v-model="bulkDeleteForm.delete_agent" type="checkbox" :disabled="bulkDeleteRunning"><span><b>卸载每台机器上的 Agent</b><small>停止并删除 gmha-agent systemd 服务与安装目录。</small></span></label></div>
          <section v-if="bulkDeleteResults.length" class="machine-bulk-progress"><article v-for="item in bulkDeleteResults" :key="item.id"><header><div><b>{{ item.name }}</b><small>{{ item.ip || item.id }}</small></div><span :class="['status',item.status]">{{ item.status==='running' ? '执行中' : item.status==='success' ? '成功' : item.status==='failed' ? '失败' : '等待' }}</span></header><ol><li v-for="step in item.steps" :key="step.key" :class="step.state"><i>{{ step.state==='success' ? '✓' : step.state==='failed' ? '!' : step.state==='running' ? '…' : step.state==='skipped' ? '—' : '○' }}</i><span><b>{{ step.title }}</b><small>{{ step.detail }}</small></span></li></ol><p :class="{failed:item.status==='failed'}">{{ errorSummary(item.message, 360) }}</p><small v-if="item.status==='failed'" class="bulk-log-hint">远端资源尚未全部清理，平台记录和失败项选择均已保留；请排除上述问题后重试。</small></article></section>
          <label class="machine-delete-confirm">输入 <code>{{ bulkDeleteExpected() }}</code> 确认批量删除<input v-model="bulkDeleteForm.confirmation" :disabled="bulkDeleteRunning || bulkDeleteClusterMembers().length" autocomplete="off" :placeholder="bulkDeleteExpected()"></label>
          <div class="modal-actions"><button type="button" class="secondary" :disabled="bulkDeleteRunning" @click="showBulkDelete=false">关闭</button><button class="danger-button" :disabled="bulkDeleteRunning || bulkDeleteClusterMembers().length || bulkDeleteForm.confirmation !== bulkDeleteExpected()">{{ bulkDeleteRunning ? (bulkDeleteForm.concurrent ? '并发处理中…' : '串行处理中…') : (bulkDeleteForm.mode==='detach' ? '确认批量剔除' : '确认批量删除') }}</button></div>
        </form>
      </div>
      <div v-if="showMachineDelete && selectedMachine" class="modal-mask machine-delete-mask" @click.self="!machineDeleteSubmitting && (showMachineDelete=false)">
        <form class="modal machine-delete-modal" @submit.prevent="deleteMachine">
          <div class="modal-head"><div><p>危险操作</p><h2>删除机器</h2></div><button type="button" :disabled="machineDeleteSubmitting" @click="showMachineDelete=false">×</button></div>
          <div class="machine-delete-target"><span>目标机器</span><b>{{ selectedMachine.Name || selectedMachine.name }}</b><small>{{ selectedMachine.IP || selectedMachine.ip }}</small></div>
          <section v-if="machineDeleteClusterName()" class="machine-cluster-reminder"><div><b>请先将机器移出集群</b><p>当前机器仍属于集群 <strong>{{ machineDeleteClusterName() }}</strong>。为避免破坏集群拓扑和自动化任务，删除前必须先解除集群归属。</p></div><button type="button" class="secondary" @click="leaveMachineDeleteForCluster">前往集群机器管理</button></section>
          <div class="machine-delete-warning"><b>机器记录删除后不可恢复</b><p v-if="machineDeleteForm.mode==='detach'">仅删除管理平台内的机器、心跳、任务与实例关联，不连接目标机器，也不改变远端 Agent、MySQL、systemd 或数据目录。</p><p v-else>按下方选择清理远端资源。MySQL 清理会停止服务、取消开机自启、删除 systemd unit，并永久删除实例数据目录。</p></div>
          <div class="delete-mode-picker">
            <label><input v-model="machineDeleteForm.mode" type="radio" value="detach" :disabled="machineDeleteSubmitting"><span><b>仅从平台剔除</b><small>适合已离线或无法管理的机器；不尝试 SSH，不清理远端组件。</small></span></label>
			<label><input v-model="machineDeleteForm.mode" type="radio" value="cleanup" :disabled="machineDeleteSubmitting"><span><b>远端清理后删除</b><small>MySQL 优先使用在线 Agent，必要时回退 SSH；Agent 自身卸载必须通过已保存的 SSH 凭证执行。</small></span></label>
          </div>
          <section v-if="machineDeleteForm.mode==='cleanup'" :class="['machine-delete-discovery', {loading:machineDeletePrechecking, detected:machineDeleteRemoteMySQLDetected(), warning:machineDeletePrecheck.warning}]">
            <header><div><b>{{ machineDeletePrechecking ? '正在实机探测 MySQL…' : machineDeleteRemoteMySQLDetected() ? '实机检测到 MySQL' : machineDeletePrecheck.remote_checked ? '实机未检测到 MySQL' : '实机状态未确认' }}</b><small>平台登记 {{ machineDeleteRegisteredPorts().length }} 个实例{{ machineDeleteRegisteredPorts().length ? '：' + machineDeleteRegisteredPorts().join('、') : '；登记数量不能代表目标机真实状态' }}</small></div><span>{{ machineDeletePrechecking ? '检查中' : machineDeletePrecheck.remote_checked ? (machineDeletePrecheck.probe_channel === 'agent' ? 'Agent 已检查' : 'SSH 已检查') : '未完成检查' }}</span></header>
            <div v-if="machineDeletePrechecking" class="machine-delete-discovery-progress"><i></i><p>正在检查 systemd 服务、mysqld 进程、启动参数、配置文件和数据路径。</p></div>
            <div v-else-if="machineDeleteMySQLResidues().length" class="machine-delete-discovery-items"><span v-for="item in machineDeleteMySQLResidues()" :key="item">{{ machineDeleteResidueLabel(item) }}</span></div>
			<p v-if="machineDeletePrecheck.warning" class="machine-delete-discovery-warning">{{ errorSummary(machineDeletePrecheck.warning, 360) }}</p><p v-else-if="machineDeletePrecheck.ssh_reachable" class="machine-delete-discovery-ready">SSH 卸载通道已验证，可安全执行 Agent 停止与删除。</p>
          </section>
          <div v-if="machineDeleteForm.mode==='cleanup'" class="machine-delete-options">
            <label><input v-model="machineDeleteForm.delete_mysql" type="checkbox" :disabled="machineDeleteSubmitting || machineDeletePrechecking"><span><b>删除 MySQL 与数据</b><small v-if="machineDeleteRemoteMySQLDetected()">将清理实机检测到的 MySQL 服务、systemd unit、程序、配置及数据目录，包括未在平台登记的实例。</small><small v-else-if="machineDeleteRegisteredPorts().length">将清理 {{ machineDeleteRegisteredPorts().length }} 个已登记实例，并再次检查远端残留。</small><small v-else-if="machineDeletePrecheck.remote_checked">实机暂未检测到 MySQL；仍可选中，以便执行删除时再次检查并清理残留。</small><small v-else>无法确认远端状态；未登记不代表没有 MySQL，仍可明确选择远程清理。</small></span></label>
            <label><input v-model="machineDeleteForm.delete_agent" type="checkbox" :disabled="machineDeleteSubmitting"><span><b>卸载 Agent</b><small>停止并禁用 gmha-agent，删除 systemd unit、安装目录和 Manager 心跳记录。</small></span></label>
          </div>
          <section class="machine-delete-flow">
            <header><b>{{ machineDeleteForm.mode==='detach' ? '平台剔除流程' : '整体执行流程' }}</b><small>{{ machineDeleteForm.mode==='detach' ? '不连接目标机器，直接清理平台记录' : '严格按以下顺序执行，任一步失败都会中止后续删除' }}</small></header>
            <ol>
              <li v-for="(step,index) in machineDeleteSteps()" :key="step.key" :class="{skipped:!step.enabled,running:machineDeleteSubmitting && step.enabled}">
                <i>{{ step.enabled ? index + 1 : '—' }}</i><span><b>{{ step.title }}</b><small>{{ step.detail }}</small></span><em>{{ !step.enabled ? '跳过' : (machineDeleteSubmitting ? '执行队列中' : '待执行') }}</em>
              </li>
            </ol>
          </section>
          <div v-if="machineDeleteError" class="machine-delete-error"><b>流程已中止</b><p>{{ errorSummary(machineDeleteError) }}</p><small>机器记录仍保留；完整日志请到任务中心对应任务详情查看。</small></div>
          <label class="machine-delete-confirm">输入 <code>{{ machineDeleteExpected() }}</code> 确认操作<input v-model="machineDeleteForm.confirmation" :disabled="machineDeleteSubmitting || !!machineDeleteClusterName()" autocomplete="off" :placeholder="machineDeleteExpected()"></label>
		  <div class="modal-actions"><button type="button" class="secondary" :disabled="machineDeleteSubmitting" @click="showMachineDelete=false">取消</button><button class="danger-button" :disabled="machineDeleteSubmitting || !!machineDeleteClusterName() || machineDeleteSSHBlocked() || machineDeleteForm.confirmation !== machineDeleteExpected()">{{ machineDeleteSubmitting ? '正在处理，请勿关闭…' : machineDeleteSSHBlocked() ? '请先恢复 SSH 通道' : (machineDeleteForm.mode==='detach' ? '确认从平台剔除' : '确认删除机器') }}</button></div>
        </form>
      </div>
    </main>
    <section v-if="active === 'agents' && data.manualRecovery" class="recovery-flow-toast"><div><b>手动拉起流程</b><small>{{ data.manualRecovery.machine_ip || data.manualRecovery.MachineIP }} · {{ data.manualRecovery.status || data.manualRecovery.Status }}</small></div><ol><li :class="{active:['pending','confirming','executing','waiting_heartbeat','succeeded'].includes(state(data.manualRecovery.status || data.manualRecovery.Status))}">确认离线状态</li><li :class="{active:['executing','waiting_heartbeat','succeeded'].includes(state(data.manualRecovery.status || data.manualRecovery.Status))}">SSH 拉起服务</li><li :class="{active:['waiting_heartbeat','succeeded'].includes(state(data.manualRecovery.status || data.manualRecovery.Status))}">等待心跳恢复</li><li :class="{active:state(data.manualRecovery.status || data.manualRecovery.Status)==='succeeded',failed:['failed','suppressed'].includes(state(data.manualRecovery.status || data.manualRecovery.Status))}">{{ state(data.manualRecovery.status || data.manualRecovery.Status)==='succeeded' ? '恢复完成' : (errorSummary(data.manualRecovery.last_error || data.manualRecovery.LastError, 100) || '等待恢复结果') }}</li></ol></section>
  `
}).component('TaskTable', {
  props: ['items', 'machines', 'state', 'date', 'page', 'total', 'pageSize'],
  emits: ['select', 'page'],
  data() { return { collapsedTasks: {} } },
  methods: {
    typeLabel(value) { return ({ exec: '远程命令', collect_machine_info: '机器信息采集', collect_static_info: '静态资产采集', mysql_install: 'MySQL 安装', mysql_uninstall: 'MySQL 卸载', mysql_topology: 'MySQL 拓扑采集', mysql_upgrade: 'MySQL 升级', mysql_cluster_bootstrap: 'MySQL 集群初始化', batch_operation: '批量业务操作', architecture_adjustment: 'MySQL 架构调整', agent_recovery: 'Agent 恢复', platform_operation: '平台操作' })[String(value || '').toLowerCase()] || value || '未知任务' },
    categoryLabel(item) {
      const rawSpec = item.SpecJSON || item.spec_json
      let spec = {}; try { spec = typeof rawSpec === 'string' ? JSON.parse(rawSpec) : (rawSpec || {}) } catch (_) {}
      return String(spec.operation || '').startsWith('mysql_') ? '数据库操作' : this.typeLabel(item.Type || item.type)
    },
    title(item) {
      const type = String(item.Type || item.type || '').toLowerCase()
      const rawSpec = item.SpecJSON || item.spec_json
      let spec = {}
      try { spec = typeof rawSpec === 'string' ? JSON.parse(rawSpec) : (rawSpec || {}) } catch (_) { spec = {} }
      const port = spec.port ? ` · ${spec.port} 端口` : ''
      return ({ exec: spec.display_name || '执行远程命令', collect_machine_info: '采集机器运行信息', collect_static_info: '采集机器静态资产', mysql_install: `部署 MySQL${port}`, mysql_uninstall: `卸载 MySQL${port}`, mysql_topology: '采集 MySQL 拓扑', mysql_upgrade: `升级 MySQL${port}`, mysql_cluster_bootstrap: spec.display_name || '批量安装并初始化架构', batch_operation: spec.display_name || '批量业务操作', architecture_adjustment: '执行 MySQL 架构调整', platform_operation: spec.display_name || '平台操作' })[type] || this.typeLabel(type)
    },
    statusLabel(value) { return ({ pending: '等待执行', sent: '已下发', running: '执行中', success: '执行成功', completed: '执行成功', failed: '执行失败', error: '执行失败' })[this.state(value)] || value || '未知' },
    progress(item) { return Number(item.ProgressPercent ?? item.progress_percent ?? 0) },
    targetMachine(item) {
      const id = item.MachineID || item.machine_id
      const ip = item.MachineIP || item.machine_ip
      return (this.machines || []).find(machine => (id && (machine.ID || machine.id) === id) || (ip && (machine.IP || machine.ip) === ip)) || {}
    },
    targetName(item) { const machine = this.targetMachine(item); return item.MachineName || item.machine_name || machine.Name || machine.name || item.MachineID || item.machine_id || '—' },
    targetCode(item) { const machine = this.targetMachine(item); return item.MachineID || item.machine_id || machine.ID || machine.id || '—' },
    targetIP(item) { const machine = this.targetMachine(item); return item.MachineIP || item.machine_ip || machine.IP || machine.ip || '—' },
    targetCluster(item) { const machine = this.targetMachine(item); return item.Cluster || item.cluster || machine.Cluster || machine.cluster || '未分配集群' },
    children(item) { return Array.isArray(item.Children) ? item.Children : (Array.isArray(item.children) ? item.children : []) },
    isExpanded(item) { return !this.collapsedTasks[item.ID || item.id] },
    toggleChildren(item) { const id = item.ID || item.id; this.collapsedTasks = { ...this.collapsedTasks, [id]: this.isExpanded(item) } },
    canDelete(item) { return this.page != null && ['success', 'completed', 'succeeded', 'failed', 'error'].includes(this.state(item.Status || item.status)) },
    isSelected(item) { return (this.$root.selectedTaskIDs || []).includes(item.ID || item.id) },
    selectableItems() { return (this.items || []).filter(this.canDelete) },
    allPageSelected() { const items = this.selectableItems(); return items.length > 0 && items.every(item => this.isSelected(item)) },
    somePageSelected() { const items = this.selectableItems(); return !this.allPageSelected() && items.some(item => this.isSelected(item)) },
    toggleSelection(item) { this.$root.toggleTaskSelection(item) },
    togglePageSelection() { this.$root.selectCurrentTaskPage() },
    pageCount() { return Math.max(1, Math.ceil(Number(this.total || 0) / Number(this.pageSize || 20))) },
    pageNumbers() {
      const total = this.pageCount(), current = Number(this.page || 1)
      const start = Math.max(1, Math.min(current - 2, total - 4))
      return Array.from({ length: Math.min(5, total) }, (_, index) => start + index)
    },
    deleteRecord(item) { this.$root.deleteTaskRecord(item) }
  },
  template: `<table class="task-table task-tree-table"><thead><tr><th class="task-select-column"><input type="checkbox" aria-label="选择当前页已完成任务" :checked="allPageSelected()" :indeterminate="somePageSelected()" @change="togglePageSelection"></th><th>任务</th><th>目标机器</th><th>归属集群</th><th>状态</th><th>执行进度</th><th>创建时间</th><th>操作</th></tr></thead><tbody><template v-for="item in items" :key="item.ID || item.id"><tr :class="['task-tree-parent',{selected:isSelected(item)}]"><td class="task-select-cell"><input type="checkbox" :aria-label="'选择任务 '+(item.ID || item.id)" :checked="isSelected(item)" :disabled="!canDelete(item)" @change="toggleSelection(item)"></td><td class="task-name-cell"><div class="task-tree-name"><button v-if="children(item).length" class="task-tree-toggle" :aria-label="isExpanded(item) ? '收起子任务' : '展开子任务'" @click.stop="toggleChildren(item)">{{ isExpanded(item) ? '−' : '+' }}</button><span v-else class="task-tree-spacer"></span><button class="task-name-link" @click="$emit('select',item)"><span class="task-type-glyph">⌁</span><span><b>{{ title(item) }}</b><small>{{ categoryLabel(item) }} · #{{ item.ID || item.id }}<em v-if="children(item).length"> · {{ children(item).length }} 个子任务</em></small></span></button></div></td><td class="task-machine-cell"><b>{{ targetName(item) }}</b><small>机器码：{{ targetCode(item) }}</small><small>IP：{{ targetIP(item) }}</small></td><td><span :class="['cluster-label', {unassigned:targetCluster(item)==='未分配集群'}]">{{ targetCluster(item) }}</span></td><td><span :class="['status', state(item.Status || item.status)]">{{ statusLabel(item.Status || item.status) }}</span></td><td class="task-progress-cell"><div class="progress"><i :style="{ width: progress(item) + '%' }"></i></div><span>{{ progress(item) }}%</span><small v-if="item.CurrentStep || item.current_step">{{ item.CurrentStep || item.current_step }}</small></td><td>{{ date(item.CreatedAt || item.created_at) }}</td><td class="task-row-actions"><button class="text-button" @click="$emit('select',item)">查看详情</button><button v-if="canDelete(item)" class="danger-link" @click.stop="deleteRecord(item)">删除记录</button></td></tr><tr v-for="(child, childIndex) in children(item)" v-show="isExpanded(item)" :key="child.ID || child.id" class="task-tree-child"><td class="task-select-cell child"></td><td class="task-name-cell"><div class="task-tree-branch" :class="{'last-child':childIndex === children(item).length - 1}"><button class="task-name-link" @click="$emit('select',child)"><span class="task-type-glyph child">↳</span><span><b>{{ title(child) }}</b><small>执行子任务 · #{{ child.ID || child.id }}</small></span></button></div></td><td class="task-machine-cell"><b>{{ targetName(child) }}</b><small>机器码：{{ targetCode(child) }}</small><small>IP：{{ targetIP(child) }}</small></td><td><span :class="['cluster-label', {unassigned:targetCluster(child)==='未分配集群'}]">{{ targetCluster(child) }}</span></td><td><span :class="['status', state(child.Status || child.status)]">{{ statusLabel(child.Status || child.status) }}</span></td><td class="task-progress-cell"><div class="progress"><i :style="{ width: progress(child) + '%' }"></i></div><span>{{ progress(child) }}%</span><small v-if="child.CurrentStep || child.current_step">{{ child.CurrentStep || child.current_step }}</small></td><td>{{ date(child.CreatedAt || child.created_at) }}</td><td class="task-row-actions"><button class="text-button" @click="$emit('select',child)">查看详情</button></td></tr></template><tr v-if="!items.length"><td colspan="8" class="empty">暂无任务记录。</td></tr></tbody></table><div v-if="$root.selectedTaskIDs.length" class="task-selection-summary"><b>已选任务记录</b><span v-for="id in $root.selectedTaskIDs" :key="id">#{{ id }}</span></div><div class="pager task-pager"><div class="task-page-size"><span>每页</span><select :value="pageSize" aria-label="每页任务数量" @change="$root.changeTaskPageSize($event.target.value)"><option :value="10">10 条</option><option :value="20">20 条</option><option :value="50">50 条</option><option :value="100">100 条</option></select></div><span class="task-page-total">共 {{ total }} 条</span><div class="task-page-buttons"><button :disabled="page <= 1" aria-label="首页" @click="$emit('page',1)">«</button><button :disabled="page <= 1" aria-label="上一页" @click="$emit('page',page-1)">‹</button><button v-for="number in pageNumbers()" :key="number" :class="{active:number===page}" :aria-current="number===page ? 'page' : undefined" @click="$emit('page',number)">{{ number }}</button><button :disabled="page >= pageCount()" aria-label="下一页" @click="$emit('page',page+1)">›</button><button :disabled="page >= pageCount()" aria-label="末页" @click="$emit('page',pageCount())">»</button></div><span class="task-page-current">第 {{ page }} / {{ pageCount() }} 页</span></div>`
}).mount('#app')
