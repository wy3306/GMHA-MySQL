import { computed, onMounted, onUnmounted, ref, watch } from 'vue/dist/vue.esm-bundler.js'

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
const mysqlPrivilegeOptions = [
  'SELECT', 'INSERT', 'UPDATE', 'DELETE', 'CREATE', 'ALTER', 'DROP', 'SHOW VIEW', 'TRIGGER', 'EVENT',
  'PROCESS', 'RELOAD', 'LOCK TABLES', 'REPLICATION CLIENT', 'REPLICATION SLAVE', 'CONNECTION_ADMIN', 'BACKUP_ADMIN', 'CLONE_ADMIN'
]
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
const summary = (input, limit = 180) => {
  const text = String(input || '').replace(/((?:root_)?password["']?\s*[:=]\s*)[^\s,;}]+/gi, '$1******').replace(/(-p)([^\s]+)/g, '$1******').replace(/\s+/g, ' ').trim()
  if (!text) return ''
  const first = text.split(/\s+\|\s+/).find(part => part.trim()) || '操作未完成'
  return first.length > limit ? `${first.slice(0, Math.max(1, limit - 1))}…` : first
}

export default {
  name: 'InstanceManagement',
  props: {
    cluster: { type: Object, required: true }, machines: { type: Array, default: () => [] },
    instances: { type: Array, default: () => [] }, packages: { type: Array, default: () => [] },
    tasks: { type: Array, default: () => [] }, topology: { type: Object, default: () => ({ nodes: [], edges: [] }) },
    accountPresets: { type: Array, default: () => [] },
    initialView: { type: String, default: 'overview' }
  },
  emits: ['close', 'refresh', 'open-task', 'view-change'],
  setup(props, { emit }) {
    const view = ref(props.initialView || 'overview'), busy = ref(false), error = ref(''), notice = ref('')
    const selectedMachines = ref([]), targetConfigs = ref({}), accountsInitialized = ref(false)
    const install = ref({ mysql_user: 'mysql', root_password: '', profile: 'default', install_pt_tools: false, base_dir: '/usr/local/mysql', accounts: [] })
    const collectConfig = ref({ enabled: true, version: '', tasks: [] })
    const paramKeyword = ref(''), paramCategory = ref('all')
    const liveParameters = ref([]), parameterTask = ref(null)
    const parameterAuth = ref({ instance: '', username: 'root', password: '' })
    const parameterEditor = ref({ open: false, action: 'update', name: '', value: '', dynamic: false, restart: false })
    const upgrade = ref({ instance: '', version: '', architecture: '', username: 'root', password: '' })
    const clusterName = computed(() => value(props.cluster, 'Name', 'name'))
    const machineIDs = computed(() => new Set(props.machines.map(item => value(item, 'ID', 'id'))))
    const machineIPs = computed(() => new Set(props.machines.map(item => value(item, 'IP', 'ip'))))
    const clusterInstances = computed(() => props.instances.filter(item => {
      const explicit = value(item, 'Cluster', 'cluster')
      return explicit ? explicit === clusterName.value : machineIDs.value.has(value(item, 'MachineID', 'machine_id')) || machineIPs.value.has(value(item, 'MachineIP', 'machine_ip'))
    }))
    const mysqlTasks = computed(() => props.tasks.filter(item => {
      const spec = value(item, 'SpecJSON', 'spec_json') || {}
      const mysqlOperation = /mysql/i.test(value(item, 'Type', 'type')) || /mysql/i.test(spec.operation || spec.Operation || '')
      return mysqlOperation && (machineIDs.value.has(value(item, 'MachineID', 'machine_id')) || machineIPs.value.has(value(item, 'MachineIP', 'machine_ip')))
    }))
    const failures = computed(() => clusterInstances.value.filter(item => ['fail','failed','error','offline'].includes(lower(value(item, 'HeartbeatStatus', 'heartbeat_status') || value(item, 'Status', 'status')))))
    const paramCategories = computed(() => [...new Set(liveParameters.value.map(item => item.category))])
    const filteredParams = computed(() => liveParameters.value.filter(item => {
      const categoryOK = paramCategory.value === 'all' || item.category === paramCategory.value
      const text = [item.name, item.category, item.value].filter(Boolean).join(' ').toLowerCase()
      return categoryOK && (!paramKeyword.value || text.includes(paramKeyword.value.toLowerCase()))
    }))
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
      if (!targetConfigs.value[id]) targetConfigs.value[id] = { version: '', architecture: '', port: 3306, server_id: index + 1, runtime_parameters: {} }
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
    const selectedInstallTargets = computed(() => props.machines.filter(machine => selectedMachines.value.includes(value(machine, 'ID', 'id'))).map((machine, index) => {
      const config = ensureTarget(machine, index)
      return { machine, config, pkg: installPackageFor(machine, config) }
    }))
    Object.defineProperty(targetConfigs.value, '_selected_install_targets', { enumerable: false, get: () => selectedInstallTargets.value })
    watch(() => props.machines, items => items.forEach(ensureTarget), { immediate: true, deep: true })
    watch(() => props.accountPresets, presets => {
      if (accountsInitialized.value) return
      if ((presets || []).length) {
        install.value.accounts = presets.map(item => ({ ...item, privileges: [...(item.privileges || [])] }))
        accountsInitialized.value = true
        return
      }
      install.value.accounts = ['monitor','mha','backup'].map(role => ({ role, username: '', password: '', host: '', enabled: true, privileges: [] }))
    }, { immediate: true, deep: true })
    const parameterGroupsFor = target => [...mysqlRuntimeParameterGroups, ...(target.pkg?.runtime_parameter_groups || [])]
    const addCustomAccount = () => install.value.accounts.push({ role: `custom-${Date.now()}`, username: '', password: '', host: '%', enabled: true, privileges: [] })
    const removeCustomAccount = index => install.value.accounts.splice(index, 1)
    const isCustomAccount = account => String(account.role || '').startsWith('custom-')
    const selectedTargetIssues = computed(() => {
      const issues = []
      if (!selectedMachines.value.length) issues.push('请至少选择一台目标机器')
      if (!String(install.value.root_password || '').trim()) issues.push('请填写 root 密码')
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
    const createInstallTasks = () => run(async () => {
      if (selectedTargetIssues.value.length) throw new Error(selectedTargetIssues.value[0])
      const selected = props.machines.filter(item => selectedMachines.value.includes(value(item, 'ID', 'id')))
      const results = await Promise.allSettled(selected.map((machine, index) => {
        const cfg = ensureTarget(machine, index)
        const pkg = installPackageFor(machine, cfg)
        const allowed = new Set([...mysqlRuntimeParameterGroups, ...(pkg?.runtime_parameter_groups || [])].flatMap(group => group.fields || []).map(field => field.key))
        const runtimeParameters = Object.fromEntries(Object.entries(cfg.runtime_parameters || {}).filter(([key, parameterValue]) => allowed.has(key) && String(parameterValue || '').trim() !== ''))
        const paths = Object.fromEntries(directoryFields.map(([key]) => [key, install.value[key] || '']))
        return request('/tasks/mysql-install', { method: 'POST', body: JSON.stringify({ machine: value(machine, 'IP', 'ip'), version: cfg.version, architecture: cfg.architecture, port: Number(cfg.port), server_id: Number(cfg.server_id), mysql_user: install.value.mysql_user, root_password: install.value.root_password, profile: install.value.profile, ...paths, install_pt_tools: (!pkg || pkg.pt_tools_supported) && install.value.install_pt_tools, runtime_parameters: runtimeParameters, accounts: install.value.accounts }) })
      }))
      const failed = results.filter(item => item.status === 'rejected')
      const succeeded = results.filter(item => item.status === 'fulfilled')
      install.value.root_password = ''; emit('refresh')
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
    const saveParams = () => run(async () => { collectConfig.value = await request('/mysql-dynamic-collect/config', { method: 'PUT', body: JSON.stringify(collectConfig.value) }); notice.value = '动态采集策略已保存并将通过心跳下发' })
    const selectedParameterInstance = computed(() => clusterInstances.value.find(item => `${value(item,'MachineIP','machine_ip')}:${value(item,'Port','port')}` === parameterAuth.value.instance))
    const waitTask = async taskID => {
      for (let i = 0; i < 120; i++) {
        const detail = await request(`/tasks?id=${encodeURIComponent(taskID)}`)
        const status = lower(value(detail.task, 'Status', 'status'))
        if (['success','failed'].includes(status)) return detail
        await new Promise(resolve => setTimeout(resolve, 1000))
      }
      throw new Error('等待参数采集任务超时，请到任务中心查看进度')
    }
    const collectParameters = () => run(async () => {
      if (!parameterAuth.value.instance || !parameterAuth.value.password) throw new Error('请选择实例并填写数据库管理员密码')
      const [machine, port] = parameterAuth.value.instance.split(':')
      const created = await request('/tasks/mysql-parameters', { method: 'POST', body: JSON.stringify({ machine, port: Number(port), username: parameterAuth.value.username, password: parameterAuth.value.password, action: 'collect' }) })
      parameterTask.value = created
      const detail = await waitTask(value(created.task, 'ID', 'id'))
      if (lower(value(detail.task, 'Status', 'status')) !== 'success') throw new Error(value(detail.task, 'CurrentStep', 'current_step') || '参数采集失败')
      const rows = []
      for (const event of detail.events || []) {
        for (const line of String(value(event,'Content','content') || '').split('\n')) {
          if (!line.startsWith('GMHA_MYSQL_PARAMETER\t')) continue
          const [, name, ...rest] = line.split('\t')
          rows.push({ name: String(name || '').toLowerCase(), value: rest.join('\t'), dynamic: dynamicMySQLParameters.has(String(name || '').toLowerCase()), category: parameterCategory(String(name || '').toLowerCase()) })
        }
      }
      liveParameters.value = rows
      notice.value = `已动态采集 ${rows.length} 个 MySQL 运行参数`
    })
    const editParameter = (item, action = 'update') => { parameterEditor.value = { open: true, action, name: item.name, value: item.value, dynamic: item.dynamic, restart: false } }
    const closeParameterEditor = () => { parameterEditor.value.open = false }
    const submitParameter = () => run(async () => {
      const instance = selectedParameterInstance.value
      if (!instance || !parameterAuth.value.password) throw new Error('实例或管理员密码不可用，请重新采集参数')
      const action = parameterEditor.value.action
      const restart = confirm(`${action === 'delete' ? '删除' : '修改'}参数后是否立即重启 MySQL？\n确定：写入配置并重启；取消：${parameterEditor.value.dynamic ? '动态参数直接下发数据库，不重启' : '仅写入配置，等待维护窗口重启'}`)
      const applyMode = parameterEditor.value.dynamic ? (restart ? 'both' : 'dynamic') : 'config'
      const result = await request('/tasks/mysql-parameters', { method: 'POST', body: JSON.stringify({ machine: value(instance,'MachineIP','machine_ip'), port: Number(value(instance,'Port','port')), username: parameterAuth.value.username, password: parameterAuth.value.password, action, name: parameterEditor.value.name, value: parameterEditor.value.value, apply_mode: applyMode, config_path: value(instance,'MyCnfPath','my_cnf_path'), systemd_unit: value(instance,'SystemdUnit','systemd_unit'), restart }) })
      closeParameterEditor(); notice.value = `${action === 'delete' ? '删除' : '修改'}参数任务已创建`; emit('open-task', result)
    })
    const roleFor = instance => {
      const ip = value(instance, 'MachineIP', 'machine_ip'), port = Number(value(instance, 'Port', 'port'))
      return props.topology.nodes?.find(node => node.ip === ip && Number(node.port) === port)?.role || value(instance, 'Role', 'role') || '独立实例'
    }
    const machineName = task => props.machines.find(item => value(item, 'ID', 'id') === value(task, 'MachineID', 'machine_id'))?.Name || value(task, 'MachineID', 'machine_id') || value(task, 'MachineIP', 'machine_ip') || '—'
    const selectedUpgradeInstance = computed(() => {
      const [ip, port] = upgrade.value.instance.split(':'); const instance = clusterInstances.value.find(item => value(item, 'MachineIP', 'machine_ip') === ip && String(value(item, 'Port', 'port')) === port)
      return instance
    })
    const upgradeArchitectures = computed(() => [...new Set(props.packages.map(pkg => normalizeArchitecture(pkg.arch || pkg.Arch)).filter(Boolean))])
    const upgradeVersions = computed(() => [...new Map(props.packages.filter(pkg => !upgrade.value.architecture || normalizeArchitecture(pkg.arch || pkg.Arch) === normalizeArchitecture(upgrade.value.architecture)).map(pkg => [pkg.version || pkg.Version, pkg])).values()])
    const upgradePreview = computed(() => {
      const instance = selectedUpgradeInstance.value
      const pkg = props.packages.find(item => (item.version || item.Version) === upgrade.value.version && normalizeArchitecture(item.arch || item.Arch) === normalizeArchitecture(upgrade.value.architecture))
      return { instance, pkg, ready: !!instance && !!pkg }
    })
    const upgradeInstanceChanged = () => {
      const instance = selectedUpgradeInstance.value
      upgrade.value.architecture = normalizeArchitecture(value(instance, 'Architecture', 'architecture')) || upgrade.value.architecture || upgradeArchitectures.value[0] || ''
      if (!upgradeVersions.value.some(pkg => (pkg.version || pkg.Version) === upgrade.value.version)) upgrade.value.version = upgradeVersions.value[0]?.version || ''
    }
    const upgradeArchitectureChanged = () => {
      if (!upgradeVersions.value.some(pkg => (pkg.version || pkg.Version) === upgrade.value.version)) upgrade.value.version = upgradeVersions.value[0]?.version || ''
    }
    const upgradeStages = ['升级兼容性检查','数据库与复制预检','暂停数据库写入','下载目标安装包','解压并检查新版本','停止数据库服务','原子切换软连接','校验升级后配置','启动与数据字典升级','数据库完整性检查','主从复制检查与修复','恢复业务访问']
    const startUpgrade = () => run(async () => {
      if (!upgradePreview.value.ready || !upgrade.value.password) throw new Error('请选择实例、目标版本并填写数据库管理员密码')
      if (!confirm(`确认升级 ${upgrade.value.instance}？\n升级期间数据库将暂停写入并短暂停止服务；任何步骤失败都会自动恢复原软连接。`)) return
      const [machine, port] = upgrade.value.instance.split(':')
      const plan = await request('/tasks/mysql-upgrade', { method: 'POST', body: JSON.stringify({ machine, port: Number(port), package_name: upgradePreview.value.pkg.file_name, username: upgrade.value.username, password: upgrade.value.password }) })
      upgrade.value.password = ''; notice.value = `升级任务已创建：MySQL ${plan.current_version} → ${plan.target_version}`; emit('open-task', plan.task)
    })
    let refreshTimer
    onMounted(() => { refreshTimer = setInterval(() => { if (['overview','instances','tasks','topology'].includes(view.value) && !busy.value) emit('refresh') }, 8000) })
    onUnmounted(() => { clearInterval(refreshTimer); parameterAuth.value.password = ''; upgrade.value.password = '' })
    return { view, busy, error, notice, clusterName, clusterInstances, mysqlTasks, failures, selectedMachines, targetConfigs, install, directoryFields, installStages, mysqlPrivilegeOptions, selectedTargetIssues, collectConfig, paramKeyword, paramCategory, paramCategories, filteredParams, liveParameters, parameterAuth, parameterEditor, parameterTask, selectedParameterInstance, collectParameters, editParameter, closeParameterEditor, submitParameter, upgrade, upgradePreview, upgradeArchitectures, upgradeVersions, upgradeInstanceChanged, upgradeArchitectureChanged, upgradeStages, startUpgrade, compatiblePackages, architecturesFor, versionsFor, targetArchitectureChanged, ensureTarget, parameterGroupsFor, addCustomAccount, removeCustomAccount, isCustomAccount, createInstallTasks, uninstall, forget, loadParams, saveParams, roleFor, machineName, emit, value, summary }
  },
  template: `
    <section class="instance-management-page">
      <header class="instance-page-head"><div><p>INSTANCE MANAGEMENT</p><h2>{{ clusterName }} · 实例管理</h2><span>统一管理 MySQL 实例生命周期、任务、拓扑、运行参数与版本升级。</span></div><button class="secondary" @click="emit('close')">返回概览</button></header>
      <nav class="instance-tabs"><button v-for="item in [['overview','概览'],['instances','实例'],['install','创建安装'],['tasks','安装任务'],['topology','拓扑'],['params','参数管理'],['upgrade','版本升级']]" :key="item[0]" :class="{active:view===item[0]}" @click="view=item[0]; item[0]==='params'&&loadParams()">{{ item[1] }}</button></nav>
      <div v-if="error" class="alert error"><b>操作未完成</b><span>{{ summary(error) }}</span><small>完整执行日志请在任务中心对应任务详情中查看。</small></div><div v-if="notice" class="alert success"><span>{{ summary(notice,240) }}</span></div>
      <main v-if="view==='overview'" class="instance-overview"><div class="instance-kpis"><article><small>实例总数</small><b>{{ clusterInstances.length }}</b></article><article><small>异常实例</small><b>{{ failures.length }}</b></article><article><small>复制链路</small><b>{{ topology.edges?.length || 0 }}</b></article><article><small>运行中任务</small><b>{{ mysqlTasks.filter(t=>['pending','sent','running'].includes(String(value(t,'Status','status')).toLowerCase())).length }}</b></article></div><section class="instance-panel"><h3>健康摘要</h3><p v-if="!failures.length">当前没有异常实例。</p><p v-for="item in failures" :key="value(item,'MachineIP','machine_ip')+value(item,'Port','port')">{{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }} · {{ summary(value(item,'HeartbeatDetail','heartbeat_detail') || '状态异常', 140) }}</p></section></main>
      <main v-else-if="view==='instances'" class="instance-panel"><div class="panel-head"><div><h3>实例列表</h3><p>查询、卸载或遗忘当前集群 MySQL 实例；状态每 8 秒自动更新。</p></div><button class="primary" @click="view='install'">创建实例</button></div><table><thead><tr><th>机器 / 实例</th><th>版本</th><th>架构</th><th>心跳</th><th>角色</th><th>操作</th></tr></thead><tbody><tr v-for="item in clusterInstances" :key="value(item,'MachineIP','machine_ip')+value(item,'Port','port')"><td><b>{{ value(item,'MachineName','machine_name') || value(item,'MachineIP','machine_ip') }}</b><small>{{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }}</small></td><td>{{ value(item,'Version','version') || value(item,'PackageName','package_name') || '待上报' }}</td><td>{{ value(item,'Architecture','architecture') || '待上报' }}</td><td><span :class="['status', String(value(item,'HeartbeatStatus','heartbeat_status') || value(item,'Status','status')).toLowerCase()]">{{ value(item,'HeartbeatStatus','heartbeat_status') || value(item,'Status','status') }}</span></td><td>{{ roleFor(item) }}</td><td><button class="danger-link" @click="uninstall(item)">卸载</button><button class="text-button" @click="forget(item)">遗忘记录</button></td></tr><tr v-if="!clusterInstances.length"><td colspan="6" class="empty">当前集群暂无 MySQL 实例。</td></tr></tbody></table></main>
      <main v-else-if="view==='install'" class="instance-install-page">
        <section class="instance-panel install-target-panel"><div class="panel-head"><div><h3>1. 目标机器、版本与架构</h3><p>版本和 CPU 架构独立选择；后端再按目标机器 glibc 自动确定具体安装包。</p></div><span class="count">已选 {{ selectedMachines.length }} 台</span></div><div class="install-table-wrap"><table><thead><tr><th>选择</th><th>机器</th><th>目标架构</th><th>MySQL 版本</th><th>端口</th><th>server_id</th></tr></thead><tbody><tr v-for="(machine,index) in machines" :key="value(machine,'ID','id')"><td><input v-model="selectedMachines" type="checkbox" :value="value(machine,'ID','id')"></td><td><b>{{ value(machine,'Name','name') }}</b><small>{{ value(machine,'IP','ip') }}</small></td><td><select v-model="ensureTarget(machine,index).architecture" @change="targetArchitectureChanged(machine,index)"><option v-for="arch in architecturesFor(machine)" :key="arch" :value="arch">{{ arch }}</option></select></td><td><select v-model="ensureTarget(machine,index).version"><option v-for="pkg in versionsFor(machine,ensureTarget(machine,index))" :key="pkg.version" :value="pkg.version">{{ pkg.version }} · {{ pkg.release_track }}</option></select><small>具体制品按 glibc 自动匹配</small></td><td><input v-model.number="ensureTarget(machine,index).port" type="number" min="1" max="65535"></td><td><input v-model.number="ensureTarget(machine,index).server_id" type="number" min="1"></td></tr><tr v-if="!machines.length"><td colspan="6" class="empty">当前集群没有可用机器，请先在机器管理中添加并部署 Agent。</td></tr></tbody></table></div></section>
        <section class="instance-panel install-common-panel"><div class="panel-head"><div><h3>2. 基础配置与目录</h3><p>目录留空时由安装 Profile 按端口自动生成；批量安装会在各目标机器使用相同目录规则。</p></div></div><div class="instance-install-fields"><label>MySQL 运行用户<input v-model.trim="install.mysql_user" required></label><label>参数 Profile<input v-model.trim="install.profile" required></label><label>root 密码<input v-model="install.root_password" type="password" autocomplete="new-password" required></label><label v-for="field in directoryFields" :key="field[0]">{{ field[1] }}<input v-model.trim="install[field[0]]" :placeholder="field[2]"></label></div></section>
        <section class="instance-panel install-runtime-panel"><div class="panel-head"><div><h3>3. MySQL 运行参数</h3><p>留空表示沿用 Profile 自动计算结果；选定安装包后同时展示版本专属参数。</p></div></div><p v-if="!targetConfigs._selected_install_targets.length" class="install-empty-hint">选择机器后可配置各目标实例参数。</p><div class="target-version-parameters"><article v-for="target in targetConfigs._selected_install_targets" :key="value(target.machine,'ID','id')"><header><b>{{ value(target.machine,'Name','name') }} · MySQL {{ target.pkg?.version || '未选择版本' }}</b><small>{{ target.pkg?.release_track || value(target.machine,'IP','ip') }}</small></header><section v-for="group in parameterGroupsFor(target)" :key="group.name"><h4>{{ group.name }}</h4><div><label v-for="field in group.fields" :key="field.key">{{ field.label }}<select v-if="field.options" v-model="target.config.runtime_parameters[field.key]"><option value="">自动</option><option v-for="option in field.options" :key="option" :value="option">{{ option }}</option></select><input v-else v-model.trim="target.config.runtime_parameters[field.key]" :placeholder="field.placeholder || (field.default ? '默认 '+field.default : '自动计算')"><small v-if="field.description || field.default">{{ field.description || ('建议值 '+field.default) }}</small></label></div></section></article></div></section>
        <section class="instance-panel install-account-panel"><div class="panel-head"><div><h3>4. 初始化账号与权限</h3><p>沿用数据库管理中的账号预设，也可为本次批量安装添加自定义用户。</p></div><button type="button" class="secondary" @click="addCustomAccount">＋ 自定义用户</button></div><div class="cluster-install-accounts"><article v-for="(account,index) in install.accounts" :key="account.role"><header><div><b>{{ isCustomAccount(account) ? '自定义数据库用户' : ({monitor:'监控账号',mha:'MHA 管理账号',backup:'备份账号'})[account.role] || account.role }}</b><small>{{ account.role }}</small></div><button v-if="isCustomAccount(account)" type="button" class="danger-link" @click="removeCustomAccount(index)">删除</button><label v-else><input v-model="account.enabled" type="checkbox"> 启用</label></header><div class="account-inputs"><label>用户名<input v-model.trim="account.username" :placeholder="account.role"></label><label>密码<input v-model="account.password" type="password" autocomplete="new-password" placeholder="使用预设或输入密码"></label><label>访问白名单<input v-model.trim="account.host" placeholder="默认 %"></label></div><div class="cluster-privileges"><label v-for="privilege in mysqlPrivilegeOptions" :key="privilege"><input v-model="account.privileges" type="checkbox" :value="privilege"> {{ privilege }}</label></div></article></div></section>
        <aside class="instance-panel install-submit-panel"><div><h3>5. 确认并安装</h3><p>任务将并发创建，每台机器独立执行，单台失败不会中断其他目标。</p></div><ol class="install-flow-preview"><li v-for="(stage,index) in installStages" :key="stage"><i>{{ index+1 }}</i><span>{{ stage }}</span></li></ol><label class="install-option"><input v-model="install.install_pt_tools" type="checkbox" :disabled="!targetConfigs._selected_install_targets.length || targetConfigs._selected_install_targets.some(target=>target.pkg && !target.pkg.pt_tools_supported)"><span><b>安装 Percona Toolkit</b><small>自动选包时由任务根据每台机器架构和最终 MySQL 版本校验并匹配</small></span></label><div v-if="selectedTargetIssues.length" class="install-validation"><b>请先处理以下问题</b><span v-for="issue in selectedTargetIssues" :key="issue">{{ issue }}</span></div><div class="install-submit-summary"><span>目标机器 <b>{{ selectedMachines.length }}</b></span><span>将创建 <b>{{ selectedMachines.length }}</b> 个任务</span></div><button type="button" class="primary" :disabled="busy" @click="createInstallTasks">{{ busy ? '正在创建…' : '并发创建安装任务' }}</button><small>提交后自动进入“安装任务”；校验未通过时会在此处明确提示原因。</small></aside>
      </main>
      <main v-else-if="view==='tasks'" class="instance-panel"><div class="panel-head"><div><h3>安装与卸载任务</h3><p>展示当前集群的 MySQL 生命周期任务；点击任务可查看完整流程与日志。</p></div><button class="secondary" @click="emit('refresh')">刷新</button></div><table><thead><tr><th>任务</th><th>机器</th><th>状态</th><th>进度</th><th>时间</th></tr></thead><tbody><tr v-for="task in mysqlTasks" :key="value(task,'ID','id')" class="clickable-row" @click="emit('open-task',task)"><td><b>{{ value(task,'Type','type') }}</b><small>{{ value(task,'ID','id') }}</small></td><td>{{ machineName(task) }}</td><td><span :class="['status',String(value(task,'Status','status')).toLowerCase()]">{{ value(task,'Status','status') }}</span></td><td><div class="instance-task-progress"><i :style="{width:(value(task,'ProgressPercent','progress_percent') || 0)+'%'}"></i></div><small>{{ value(task,'ProgressPercent','progress_percent') || 0 }}%</small></td><td>{{ value(task,'CreatedAt','created_at') }}</td></tr><tr v-if="!mysqlTasks.length"><td colspan="5" class="empty">暂无 MySQL 任务。</td></tr></tbody></table></main>
      <main v-else-if="view==='topology'" class="instance-panel"><div class="panel-head"><div><h3>MySQL 实例与主从拓扑</h3><p>基于 Agent 实时复制状态绘制。</p></div><button class="secondary" @click="emit('refresh')">刷新</button></div><div class="instance-topology"><article v-for="node in topology.nodes" :key="node.ip+':'+node.port"><b>{{ node.name || node.ip }}</b><span>{{ node.ip }}:{{ node.port }}</span><small>{{ node.role || '独立实例' }} · {{ node.heartbeat || '—' }}</small></article></div><div class="instance-edges"><p v-for="edge in topology.edges" :key="edge.source_ip+edge.target_ip"><span>{{ edge.source_name || edge.source_ip }}</span><i>→</i><span>{{ edge.target_name || edge.target_ip }}</span><em>延迟 {{ edge.lag || '—' }}</em></p><p v-if="!topology.edges?.length">暂无复制链路。</p></div></main>
      <main v-else-if="view==='params'" class="mysql-parameter-page">
        <section class="instance-panel parameter-runtime-panel"><div class="panel-head"><div><h3>MySQL 全量运行参数</h3><p>从 performance_schema 动态采集当前实例的全部 GLOBAL VARIABLES；动态参数直接下发数据库，静态参数写入 my.cnf。</p></div><button class="primary" :disabled="busy" @click="collectParameters">{{ busy ? '采集中…' : '动态采集' }}</button></div><div class="parameter-connection-bar"><label>目标实例<select v-model="parameterAuth.instance"><option value="">选择实例</option><option v-for="item in clusterInstances" :value="value(item,'MachineIP','machine_ip')+':'+value(item,'Port','port')">{{ value(item,'MachineName','machine_name') || value(item,'MachineIP','machine_ip') }} · {{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }}</option></select></label><label>管理员用户<input v-model.trim="parameterAuth.username"></label><label>管理员密码<input v-model="parameterAuth.password" type="password" autocomplete="new-password"></label></div><div class="param-toolbar"><input v-model.trim="paramKeyword" placeholder="搜索参数名称、值或分类"><select v-model="paramCategory"><option value="all">全部分类</option><option v-for="category in paramCategories" :key="category" :value="category">{{ category }}</option></select><span>共 {{ filteredParams.length }} / {{ liveParameters.length }} 项</span></div><div class="param-table-wrap"><table><thead><tr><th>参数名称</th><th>当前值</th><th>分类</th><th>生效方式</th><th>操作</th></tr></thead><tbody><tr v-for="item in filteredParams" :key="item.name"><td><b>{{ item.name }}</b></td><td class="parameter-value" :title="item.value">{{ item.value }}</td><td>{{ item.category }}</td><td><span :class="['parameter-mode', item.dynamic ? 'dynamic' : 'restart']">{{ item.dynamic ? '动态下发' : '配置 / 重启' }}</span></td><td><button class="text-button" @click="editParameter(item)">修改</button><button class="danger-link" @click="editParameter(item,'delete')">删除配置</button></td></tr><tr v-if="!liveParameters.length"><td colspan="5" class="empty">请选择实例并填写管理员密码，然后点击“动态采集”。</td></tr><tr v-else-if="!filteredParams.length"><td colspan="5" class="empty">没有匹配的运行参数。</td></tr></tbody></table></div></section>
        <details class="instance-panel collect-policy-panel"><summary>动态指标采集策略</summary><div class="panel-head"><div><h3>Agent 动态采集</h3><p>配置性能、连接、复制与存储指标的采集周期；当前版本 {{ collectConfig.version || '未生成' }}。</p></div><button class="secondary" :disabled="busy" @click="saveParams">保存采集策略</button></div><div class="collect-policy-table"><table><thead><tr><th>指标</th><th>分类</th><th>启用</th><th>间隔(秒)</th><th>超时(秒)</th></tr></thead><tbody><tr v-for="task in collectConfig.tasks" :key="task.name"><td>{{ task.labels?.display_name || task.name }}</td><td>{{ task.category }}</td><td><input v-model="task.enabled" type="checkbox"></td><td><input v-model.number="task.interval_seconds" type="number" min="1"></td><td><input v-model.number="task.timeout_seconds" type="number" min="1"></td></tr></tbody></table></div></details>
      </main>
      <main v-else class="instance-panel mysql-upgrade-page"><div class="panel-head"><div><h3>MySQL 版本升级</h3><p>升级采用版本目录解压与软连接原子替换；提交前校验架构、glibc、版本路径及数据库状态，失败自动回滚原链接。</p></div><button class="primary" :disabled="busy || !upgradePreview.ready" @click="startUpgrade">{{ busy ? '正在创建…' : '创建升级任务' }}</button></div><div class="upgrade-form-grid"><label>目标实例<select v-model="upgrade.instance" @change="upgradeInstanceChanged"><option value="">选择实例</option><option v-for="item in clusterInstances" :value="value(item,'MachineIP','machine_ip')+':'+value(item,'Port','port')">{{ value(item,'MachineIP','machine_ip') }}:{{ value(item,'Port','port') }} · {{ value(item,'Version','version') || value(item,'PackageName','package_name') || '未知版本' }}</option></select></label><label>目标架构<select v-model="upgrade.architecture" @change="upgradeArchitectureChanged"><option v-for="arch in upgradeArchitectures" :key="arch" :value="arch">{{ arch }}</option></select></label><label>目标版本<select v-model="upgrade.version"><option value="">选择版本</option><option v-for="pkg in upgradeVersions" :key="pkg.version" :value="pkg.version">{{ pkg.version }} · {{ pkg.release_track }}</option></select></label><label>管理员用户<input v-model.trim="upgrade.username"></label><label>管理员密码<input v-model="upgrade.password" type="password" autocomplete="new-password"></label></div><div v-if="upgradePreview.ready" class="upgrade-preview"><b>升级目标</b><p>当前：{{ value(upgradePreview.instance,'Version','version') || value(upgradePreview.instance,'PackageName','package_name') }}</p><p>目标：{{ upgradePreview.pkg.version }} · {{ upgradePreview.pkg.arch }} · {{ upgradePreview.pkg.release_track }}</p><p>安装路径保持不变，任务会将软连接切换到新版本目录。</p></div><section class="upgrade-flow"><h4>升级执行流程与日志节点</h4><ol><li v-for="(stage,index) in upgradeStages" :key="stage"><i>{{ index+1 }}</i><span>{{ stage }}</span></li></ol><p>每一步的开始时间、结束时间、命令输出和错误都会写入任务中心；失败时同一任务日志会记录自动回滚结果。</p></section></main>
      <div v-if="parameterEditor.open" class="modal-mask" @click.self="closeParameterEditor"><form class="modal parameter-editor" @submit.prevent="submitParameter"><div class="modal-head"><div><p>MYSQL PARAMETER</p><h2>{{ parameterEditor.action==='delete' ? '删除参数配置' : '修改运行参数' }}</h2></div><button type="button" @click="closeParameterEditor">×</button></div><label>参数名称<input :value="parameterEditor.name" readonly></label><label v-if="parameterEditor.action==='update'">参数值<input v-model.trim="parameterEditor.value" required></label><div class="parameter-apply-note"><b>{{ parameterEditor.dynamic ? '支持动态生效' : '需要配置生效' }}</b><p>{{ parameterEditor.dynamic ? '不重启时将立即 SET GLOBAL 下发数据库；如选择重启则同时写入 my.cnf。' : '修改会写入 my.cnf；提交时可选择是否立即重启。' }}</p></div><div class="modal-actions"><button type="button" class="secondary" @click="closeParameterEditor">取消</button><button :class="parameterEditor.action==='delete' ? 'danger-button' : 'primary'">确认{{ parameterEditor.action==='delete' ? '删除' : '修改' }}</button></div></form></div>
    </section>`
}
