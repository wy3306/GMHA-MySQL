const dynamicParameterNames = [
  'autocommit', 'binlog_expire_logs_seconds', 'expire_logs_days', 'binlog_format', 'connect_timeout', 'event_scheduler', 'general_log', 'general_log_file',
  'group_concat_max_len', 'innodb_buffer_pool_size', 'innodb_flush_log_at_trx_commit', 'innodb_io_capacity', 'innodb_io_capacity_max',
  'innodb_lock_wait_timeout', 'innodb_max_dirty_pages_pct', 'innodb_old_blocks_time', 'innodb_online_alter_log_max_size',
  'innodb_print_all_deadlocks', 'innodb_purge_threads', 'innodb_read_io_threads', 'innodb_stats_on_metadata', 'innodb_write_io_threads',
  'interactive_timeout', 'join_buffer_size', 'lock_wait_timeout', 'log_output', 'long_query_time', 'max_allowed_packet',
  'max_connect_errors', 'max_connections', 'max_execution_time', 'max_heap_table_size', 'max_prepared_stmt_count', 'net_read_timeout',
  'net_write_timeout', 'optimizer_switch', 'read_buffer_size', 'read_only', 'read_rnd_buffer_size', 'slow_query_log', 'sort_buffer_size',
  'sql_mode', 'super_read_only', 'sync_binlog', 'table_definition_cache', 'table_open_cache', 'thread_cache_size', 'tmp_table_size',
  'transaction_isolation', 'tx_isolation', 'wait_timeout'
]
const stableRestartParameterNames = ['server_id']

export const dynamicMySQLParameters = new Set(dynamicParameterNames)

export const mysqlParameterIsDynamic = name => dynamicMySQLParameters.has(String(name || '').trim().toLowerCase())

export const mysqlParameterCategory = name => {
  const normalized = String(name || '').trim().toLowerCase()
  if (/^innodb_/.test(normalized)) return 'InnoDB'
  if (/^(server_id$|binlog_|log_|gtid_|relay_)/.test(normalized)) return '日志与复制'
  if (/^(max_|thread_|table_|open_files|back_log|connect_|wait_|interactive_)/.test(normalized)) return '连接与缓存'
  if (/^(character_|collation_|sql_mode|time_zone|lc_)/.test(normalized)) return '字符集与 SQL'
  if (/^(performance_schema|optimizer_|join_|sort_|read_|tmp_)/.test(normalized)) return '性能与优化器'
  return '其他'
}

const normalizeField = (field, groupName = '') => {
  const name = String(field?.key || field?.name || '').trim().toLowerCase()
  if (!name || /^(limit_|sysctl_)/.test(name)) return null
  return {
    name,
    label: field.label || name,
    category: mysqlParameterCategory(name),
    sourceGroup: groupName,
    dynamic: mysqlParameterIsDynamic(name),
    description: field.description || '',
    defaultValue: field.default ?? '',
    placeholder: field.placeholder || '',
    options: Array.isArray(field.options) ? field.options.map(String) : []
  }
}

// Both the single-cluster instance page and multi-cluster automation page use
// this factory. Package-specific parameter groups are merged into the stable
// built-in directory so apply-mode labels cannot drift between the two pages.
export const createMySQLParameterCatalog = ({ groups = [], packages = [] } = {}) => {
  const entries = new Map()
  const addGroups = input => {
    for (const group of input || []) {
      for (const field of group?.fields || []) {
        const item = normalizeField(field, group.name || '')
        if (!item) continue
        entries.set(item.name, { ...(entries.get(item.name) || {}), ...item })
      }
    }
  }
  addGroups(groups)
  for (const pkg of packages || []) addGroups(pkg.runtime_parameter_groups || pkg.RuntimeParameterGroups || [])
  for (const name of dynamicParameterNames) {
    if (!entries.has(name)) entries.set(name, normalizeField({ key: name }))
  }
  for (const name of stableRestartParameterNames) {
    if (!entries.has(name)) entries.set(name, normalizeField({ key: name, description: '每个实例必须唯一，修改后需要重启' }))
  }
  return [...entries.values()].sort((left, right) => left.name.localeCompare(right.name))
}
