import { computed, ref } from 'vue'
import { manualDeepDive } from './manual-deep-dive.js'

const endpoint = (category, method, path, title, options = {}) => ({
  category,
  method,
  path,
  title,
  status: options.status || (method === 'POST' ? 200 : method === 'DELETE' ? 200 : 200),
  query: options.query || [],
  body: options.body,
  response: options.response ?? { ok: true },
  note: options.note || '',
  contentType: options.contentType || 'application/json'
})

const taskResponse = { id: 'task-01HX...', type: 'mysql_install', status: 'pending', progress_percent: 0 }
const pageResponse = { items: [{ id: '示例 ID', name: '示例对象' }], total: 1, page: 1, page_size: 20 }
const okResponse = { ok: true }
const deletedResponse = { deleted: true }

export const apiEndpoints = [
  endpoint('系统', 'GET', '/healthz', '服务健康检查', { response: { status: 'ok' }, note: '负载均衡器与监控系统可使用此端点探活。' }),

  endpoint('机器与凭证', 'GET', '/machines?page=1&page_size=20&keyword=&cluster=all', '分页查询机器', { query: ['page', 'page_size', 'keyword', 'cluster'], response: pageResponse }),
  endpoint('机器与凭证', 'POST', '/machines', '纳管机器', { body: { name: 'db-01', ip: '10.0.0.11', ssh_port: 22, ssh_user: 'root', credential_id: 'cred-01', preserve_agent: false, preserve_mysql: true }, response: { machine: { id: 'machine-01', name: 'db-01', ip: '10.0.0.11' }, task_id: 'task-01HX...' }, note: '执行 SSH 预检并安装/接管 Agent；敏感凭证不会在返回中回显。' }),
  endpoint('机器与凭证', 'POST', '/machines/precheck', '机器纳管预检', { body: { ip: '10.0.0.11', ssh_port: 22, ssh_user: 'root', credential_id: 'cred-01' }, response: { reachable: true, checks: [{ name: 'ssh', passed: true, message: '连接成功' }] } }),
  endpoint('机器与凭证', 'POST', '/machines/cleanup', '清理未完成纳管', { body: { ip: '10.0.0.11', ssh_port: 22, ssh_user: 'root' }, response: { status: 'cleaned' } }),
  endpoint('机器与凭证', 'POST', '/machines/batch-delete', '批量删除机器', { body: { machine_ids: ['machine-01'], delete_mysql: false, delete_agent: true, detach_only: false, concurrency: 3 }, response: { requested: 1, deleted: 1, failed: 0, items: [] } }),
  endpoint('机器与凭证', 'GET', '/machines/{machine_id}', '查询机器详情', { response: { id: 'machine-01', name: 'db-01', ip: '10.0.0.11', cluster: 'prod' } }),
  endpoint('机器与凭证', 'PUT', '/machines/{machine_id}', '更新机器信息', { body: { name: 'db-primary', ip: '10.0.0.11', ssh_port: 22, ssh_user: 'root' }, response: { machine_id: 'machine-01' } }),
  endpoint('机器与凭证', 'DELETE', '/machines/{machine_id}', '删除或仅解除纳管', { body: { delete_mysql: false, delete_agent: true, detach_only: false }, response: { machine_id: 'machine-01', deleted: true, task_ids: ['task-01HX...'] } }),
  endpoint('机器与凭证', 'GET', '/machines/{machine_id}/delete-precheck', '删除机器前检查', { response: { registered_mysql_ports: [3306], remote_checked: true, mysql_detected: true, warnings: [] } }),
  endpoint('机器与凭证', 'GET', '/machines/{machine_id}/static-info', '读取机器静态信息', { response: { hostname: 'db-01', os: 'Rocky Linux 9', architecture: 'x86_64', cpu_cores: 16, memory_gb: 64 } }),
  endpoint('机器与凭证', 'POST', '/machines/{machine_id}/static-info', '重新采集静态信息', { response: taskResponse }),
  endpoint('机器与凭证', 'GET', '/machines/{machine_id}/dynamic-metrics', '读取机器实时指标', { response: { machine_id: 'machine-01', collected_at: '2026-07-23T10:00:00Z', metrics: { cpu_usage_percent: 23.1, mem_usage_percent: 48.2 } } }),
  endpoint('机器与凭证', 'POST', '/machines/{machine_id}/assign-cluster', '把机器加入集群', { body: { cluster: 'prod' }, response: { machine_id: 'machine-01', cluster: 'prod' } }),
  endpoint('机器与凭证', 'DELETE', '/machines/{machine_id}/assign-cluster', '把机器移出集群', { response: { machine_id: 'machine-01', cluster: '' }, note: '只解除平台集群归属，不删除机器、Agent 或 MySQL 数据。' }),
  endpoint('机器与凭证', 'GET', '/ssh-credentials', '查询 SSH 凭证', { response: [{ id: 'cred-01', name: '生产环境 root', ssh_user: 'root', type: 'private_key' }], note: '密码、私钥与口令不会返回。' }),
  endpoint('机器与凭证', 'POST', '/ssh-credentials', '创建 SSH 凭证', { body: { name: '生产环境 root', ssh_user: 'root', type: 'private_key', private_key: '-----BEGIN OPENSSH PRIVATE KEY-----...' }, response: { id: 'cred-01', name: '生产环境 root', ssh_user: 'root', type: 'private_key' } }),
  endpoint('机器与凭证', 'DELETE', '/ssh-credentials/{credential_id}', '删除 SSH 凭证', { response: { credential: 'cred-01' } }),
  endpoint('机器与凭证', 'POST', '/ssh-credentials/{credential_id}/assign', '批量分配凭证', { body: { machine_ids: ['machine-01', 'machine-02'] }, response: { credential_id: 'cred-01', machine_ids: ['machine-01', 'machine-02'] } }),

  endpoint('集群', 'GET', '/clusters?page=1&page_size=20&keyword=', '分页查询集群', { query: ['page', 'page_size', 'keyword'], response: pageResponse }),
  endpoint('集群', 'POST', '/clusters', '创建集群', { body: { name: 'prod', description: '生产主集群' }, response: { name: 'prod', description: '生产主集群' } }),
  endpoint('集群', 'GET', '/clusters/{cluster_name}', '查询集群详情', { response: { name: 'prod', description: '生产主集群', machine_count: 3 } }),
  endpoint('集群', 'PUT', '/clusters/{cluster_name}', '更新集群', { body: { new_name: 'prod-ha', description: '生产高可用集群' }, response: { old_name: 'prod', new_name: 'prod-ha', description: '生产高可用集群' }, note: '只改说明时 new_name 与当前名称相同。重命名会同步成员机器，但存在 VIP、备份策略或活动任务时会被阻止，避免遗留旧集群引用。' }),
  endpoint('集群', 'DELETE', '/clusters/{cluster_name}', '删除集群登记', { response: { name: 'prod' }, note: '会把普通成员变为未分配；存在 VIP、备份策略或活动任务时拒绝。内置 AI 使用更严格的空集群删除规则。' }),
  endpoint('集群', 'GET', '/clusters/{cluster_name}/machines?page=1&page_size=20', '查询集群机器', { response: pageResponse }),
  endpoint('集群', 'POST', '/clusters/{cluster_name}/members', '设置集群成员', { body: { machine_ids: ['machine-01', 'machine-02'] }, response: { cluster: 'prod', assigned: 2, failed: 0, items: [] } }),
  endpoint('集群', 'POST', '/clusters/{cluster_name}/cleanup', '一键清理并删除集群', { response: { cluster: 'prod', removed_vips: ['10.0.0.100'], deleted_backup_policies: ['policy-01'], items: [{ machine_id: 'machine-01', mysql_uninstall_tasks: ['task-01'], agent_uninstalled: true, local_cleaned: true }], failed: 0 }, note: '极高风险：先阻止并发任务并安全撤销 VIP、删除备份策略，再逐机卸载 MySQL、清理残留、卸载 Agent、清理本地记录并删除集群。页面要求输入 CLEAN CLUSTER {cluster_name}；API 调用方也必须实施同等级确认。' }),
  endpoint('集群', 'GET', '/clusters/{cluster_name}/topology?range_minutes=60&instance=', '查询拓扑与概览指标', { query: ['range_minutes', 'instance'], response: { cluster: 'prod', nodes: [], edges: [], overview: { summary: {}, series: [], machines: [], storage: [] } } }),

  endpoint('Agent', 'GET', '/agents?page=1&page_size=50&keyword=&status=all&version=all', '分页查询 Agent', { query: ['page', 'page_size', 'keyword', 'status', 'version', 'candidate'], response: pageResponse }),
  endpoint('Agent', 'POST', '/agents/register', 'Agent 注册', { body: { ip: '10.0.0.11', hostname: 'db-01', version: 'v1.2.0' }, response: { ip: '10.0.0.11', state: 'online' }, note: '供 Agent 使用，生产环境应在反向代理层限制来源。' }),
  endpoint('Agent', 'POST', '/agents/heartbeat', 'Agent 心跳上报', { body: { ip: '10.0.0.11', status: 'online', metrics: {} }, response: { ip: '10.0.0.11', state: 'online' }, note: '心跳携带主机与 MySQL 动态采集结果，并驱动告警计算。' }),
  endpoint('Agent', 'POST', '/agents/retry-install', '重试安装 Agent', { body: { ip: '10.0.0.11' }, response: { ip: '10.0.0.11', task_id: 'task-01HX...' } }),
  endpoint('Agent', 'POST', '/agents/upgrade', '升级单个 Agent', { body: { ip: '10.0.0.11', package_name: 'gmha-agent-v1.3.0-linux-amd64' }, response: { ip: '10.0.0.11', task_id: 'task-01HX...' } }),
  endpoint('Agent', 'POST', '/agents/detect-version', '识别 Agent 版本', { body: { ip: '10.0.0.11' }, response: { ip: '10.0.0.11', version: 'v1.2.0', state: 'online' } }),
  endpoint('Agent', 'POST', '/agents/repair-mysql-config', '修复 Agent MySQL 采集配置', { body: { ip: '10.0.0.11' }, response: { ip: '10.0.0.11', task_id: 'task-01HX...' } }),
  endpoint('Agent', 'POST', '/agents/uninstall', '卸载 Agent', { body: { ip: '10.0.0.11' }, response: { ip: '10.0.0.11', task_id: 'task-01HX...' } }),
  endpoint('Agent', 'GET', '/agents/recovery-tasks', '查询恢复任务', { response: [{ id: 'recovery-01', machine_ip: '10.0.0.11', status: 'waiting_heartbeat' }] }),
  endpoint('Agent', 'POST', '/agents/recover', '手动恢复 Agent', { body: { machine_id: 'machine-01' }, response: { id: 'recovery-01', machine_ip: '10.0.0.11', status: 'pending' } }),

  endpoint('MySQL 实例', 'GET', '/mysql/instances', '查询 MySQL 实例', { response: [{ machine_id: 'machine-01', machine_name: 'db-01', machine_ip: '10.0.0.11', port: 3306, version: '8.0.46', status: 'running', cluster: 'prod', heartbeat_status: 'ok' }] }),
  endpoint('MySQL 实例', 'DELETE', '/mysql/instances', '移除实例登记', { body: { machine: 'machine-01', port: 3306 }, response: { machine: 'machine-01', port: 3306 }, note: '只移除 GMHA 登记，不卸载远端 MySQL。' }),
  endpoint('MySQL 实例', 'GET', '/mysql/histograms?machine_id=machine-01&port=3306&schema=app&table=orders', '查看直方图', { query: ['machine_id', 'port', 'schema', 'table'], response: { server_version: '8.0.46', schemas: ['app'], tables: [{ name: 'orders', estimated_rows: 120000 }], columns: [{ name: 'status', eligible: true, has_histogram: true }], histograms: [{ schema: 'app', table: 'orders', column: 'status', buckets: 8 }] } }),
  endpoint('MySQL 实例', 'POST', '/mysql/histograms', '创建或更新直方图', { body: { machine_id: 'machine-01', port: 3306, schema: 'app', table: 'orders', columns: ['status'], buckets: 16 }, response: { action: 'update', schema: 'app', table: 'orders', columns: ['status'], buckets: 16 }, note: '仅支持 MySQL 8.0+；columns 为数组，桶数范围 1–1024。' }),
  endpoint('MySQL 实例', 'DELETE', '/mysql/histograms', '删除直方图', { body: { machine_id: 'machine-01', port: 3306, schema: 'app', table: 'orders', columns: ['status'] }, response: { action: 'drop', schema: 'app', table: 'orders', columns: ['status'] }, note: '使用 NO_WRITE_TO_BINLOG 删除实例本地优化器统计，不删除数据或索引。' }),
  endpoint('MySQL 实例', 'GET', '/mysql/binlog-analysis', '查询 Binlog 分析任务', { response: { items: [{ id: 'binlog-1784800000-ab12cd34', status: 'completed', request: { machine_id: 'machine-01', port: 3306 }, summary: { total_rows: 12680, ddl_count: 2, big_txn_count: 1 } }] }, note: '列表不会返回数据库凭据或完整分析明细。' }),
  endpoint('MySQL 实例', 'POST', '/mysql/binlog-analysis', '创建 Binlog 分析任务', { status: 202, body: { machine_id: 'machine-01', port: 3306, start_time: '2026-07-23T09:00', end_time: '2026-07-23T10:00', start_file: '', big_txn_mode: 'rows', big_txn_rows_threshold: 1000, big_txn_bytes_threshold: 0 }, response: { id: 'binlog-1784800000-ab12cd34', status: 'queued', progress: { phase: 'queued', message: '任务已进入分析队列' } }, note: '凭据从已启用的 MHA 账号预设中解析；单次范围最长 7 天。' }),
  endpoint('MySQL 实例', 'GET', '/mysql/binlog-analysis/{task_id}', '查询 Binlog 分析进度与结果', { response: { id: 'binlog-1784800000-ab12cd34', status: 'completed', progress: { phase: 'completed', files_total: 3, files_completed: 3 }, result: { summary: { total_rows: 12680, ddl_count: 2, big_txn_count: 1 }, buckets: [], tables: [], big_transactions: [{ gtid: 'uuid:120', row_count: 3200, replication_delay_micros: 12500 }] } } }),
  endpoint('MySQL 实例', 'DELETE', '/mysql/binlog-analysis/{task_id}', '取消运行中的 Binlog 分析', { response: { id: 'binlog-1784800000-ab12cd34', status: 'canceled' } }),
  endpoint('MySQL 实例', 'GET', '/mysql/account-presets', '读取初始化账号预设', { response: [{ role: 'monitor', username: 'gmha_monitor', host: '%', privileges: ['PROCESS', 'REPLICATION CLIENT'] }] }),
  endpoint('MySQL 实例', 'PUT', '/mysql/account-presets', '保存初始化账号预设', { body: [{ role: 'monitor', username: 'gmha_monitor', host: '%', privileges: ['PROCESS'] }], response: [{ role: 'monitor', username: 'gmha_monitor', host: '%', privileges: ['PROCESS'] }] }),

  endpoint('SQL 诊断', 'GET', '/sql-diagnostics/config', '读取 SQL 诊断配置', { response: { max_rows: 500, slow_query_seconds: 1, kill_audit_retention_days: 90 } }),
  endpoint('SQL 诊断', 'PUT', '/sql-diagnostics/config', '更新 SQL 诊断配置', { body: { max_rows: 500, slow_query_seconds: 1, kill_audit_retention_days: 90 }, response: { max_rows: 500, slow_query_seconds: 1, kill_audit_retention_days: 90 } }),
  endpoint('SQL 诊断', 'POST', '/sql-diagnostics/explain', '分析 SQL 执行计划', { body: { machine_id: 'machine-01', port: 3306, database: 'app', sql: 'SELECT * FROM orders WHERE user_id = 42' }, response: { instance: { machine_id: 'machine-01', port: 3306 }, database: 'app', sql: 'SELECT * FROM orders WHERE user_id = 42', columns: ['id', 'select_type', 'table', 'type', 'key', 'rows', 'Extra'], rows: [{ id: 1, select_type: 'SIMPLE', table: 'orders', type: 'ref', key: 'idx_user_id', rows: 8, Extra: null }], generated_at: '2026-07-23T10:00:00Z' }, note: '只接受单条可 EXPLAIN 的 SQL；不接受 EXPLAIN/EXPLAIN ANALYZE、写语句、多语句或注释。' }),
  endpoint('SQL 诊断', 'GET', '/sql-diagnostics/current?machine=machine-01&port=3306', '查询当前会话', { query: ['machine', 'port', 'database', 'user', 'keyword'], response: { collected_at: '2026-07-23T10:00:00Z', items: [], total: 0 } }),
  endpoint('SQL 诊断', 'GET', '/sql-diagnostics/history?machine=machine-01&port=3306', '查询语句历史', { query: ['machine', 'port', 'start', 'end', 'limit'], response: { items: [], total: 0 } }),
  endpoint('SQL 诊断', 'GET', '/sql-diagnostics/top?machine=machine-01&port=3306&sort=latency', '查询 Top SQL', { query: ['machine', 'port', 'sort', 'limit'], response: { items: [], total: 0, sort: 'latency' } }),
  endpoint('SQL 诊断', 'GET', '/sql-diagnostics/slow?machine=machine-01&port=3306', '查询慢 SQL', { query: ['machine', 'port', 'start', 'end', 'limit'], response: { items: [], total: 0 } }),
  endpoint('SQL 诊断', 'POST', '/sql-diagnostics/kill', '终止会话或查询', { body: { machine: 'machine-01', port: 3306, connection_id: 12345, mode: 'query', reason: '阻塞在线事务', actor: 'dba' }, response: { killed: true, connection_id: 12345, mode: 'query', audit_id: 'audit-01' } }),
  endpoint('SQL 诊断', 'GET', '/sql-diagnostics/kill-audits?start=2026-07-01T00:00:00Z&end=2026-08-01T00:00:00Z', '查询 Kill 审计', { query: ['start', 'end', 'machine', 'actor'], response: { start: '2026-07-01T00:00:00Z', end: '2026-08-01T00:00:00Z', items: [] } }),

  endpoint('性能监控', 'GET', '/performance/catalog', '查询指标目录', { response: { items: [{ name: 'mysql_qps', display_name: 'QPS', scope: 'mysql', unit: '次/s', interval_seconds: 5 }], scopes: ['mysql', 'machine'] } }),
  endpoint('性能监控', 'GET', '/performance/metrics?cluster=prod&metric=mysql_qps&range_minutes=60&step_seconds=5', '查询指标时序', { query: ['cluster', 'metric', 'range_minutes 或 start/end', 'step_seconds', 'instance'], response: { cluster: 'prod', metric: 'mysql_qps', unit: '次/s', aggregation: 'sum', start: '2026-07-23T09:00:00Z', end: '2026-07-23T10:00:00Z', series: [{ timestamp: '2026-07-23T10:00:00Z', value: 128.4 }] } }),
  endpoint('性能监控', 'GET', '/performance/flamegraphs?cluster=prod&limit=50', '查询火焰图记录', { response: { items: [], total: 0 } }),
  endpoint('性能监控', 'POST', '/performance/flamegraphs', '立即采集火焰图', { status: 201, body: { machine_id: 'machine-01', target_type: 'process', target: 'mysqld', duration_seconds: 30, frequency_hz: 99, backend: 'auto' }, response: { id: 'profile-01', status: 'pending', task_id: 'task-01HX...' } }),
  endpoint('性能监控', 'GET', '/performance/flamegraphs/{profile_id}', '查询火焰图详情', { response: { id: 'profile-01', status: 'success', svg_url: '/api/v1/performance/flamegraphs/profile-01/content' } }),
  endpoint('性能监控', 'DELETE', '/performance/flamegraphs/{profile_id}', '删除火焰图记录', { response: { id: 'profile-01' } }),
  endpoint('性能监控', 'GET', '/performance/flamegraphs/schedules?cluster=prod', '查询火焰图计划', { response: { items: [], total: 0 } }),
  endpoint('性能监控', 'POST', '/performance/flamegraphs/schedules', '创建火焰图计划', { status: 201, body: { name: '每日 mysqld CPU', machine_id: 'machine-01', target_type: 'process', target: 'mysqld', duration_seconds: 30, frequency_hz: 99, backend: 'auto', schedule_type: 'interval', interval_minutes: 1440, enabled: true }, response: { id: 'schedule-01', enabled: true } }),
  endpoint('性能监控', 'POST', '/performance/flamegraphs/schedules/{schedule_id}/run', '立即运行火焰图计划', { status: 201, response: { id: 'profile-01', status: 'pending', task_id: 'task-01HX...' } }),
  endpoint('性能监控', 'DELETE', '/performance/flamegraphs/schedules/{schedule_id}', '删除火焰图计划', { response: { id: 'schedule-01' } }),

  endpoint('任务与自动化', 'GET', '/tasks?page=1&page_size=20&status=all&type=all&keyword=', '分页查询任务', { query: ['page', 'page_size', 'status', 'type', 'keyword'], response: pageResponse }),
  endpoint('任务与自动化', 'GET', '/tasks?id={task_id}', '查询任务详情', { response: { task: taskResponse, steps: [], events: [], children: [] } }),
  endpoint('任务与自动化', 'GET', '/tasks?stats=true', '查询任务统计', { response: { all: 42, running: 2, success: 38, failed: 2 } }),
  endpoint('任务与自动化', 'DELETE', '/tasks?id={task_id}', '删除一条已完成任务记录', { status: 204, response: null, note: '成功时无响应体；运行中任务不能删除。' }),
  endpoint('任务与自动化', 'DELETE', '/tasks', '批量删除任务记录', { body: { task_ids: ['task-01'], all_filtered: false, keyword: '', status: 'success', type: 'all' }, response: { requested: 1, deleted: 1, skipped: 0, items: [] } }),
  endpoint('任务与自动化', 'POST', '/tasks/exec', '在 Agent 执行命令', { body: { machine: 'machine-01', command: 'uptime' }, response: taskResponse }),
  endpoint('任务与自动化', 'POST', '/tasks/collect-machine-info', '创建机器信息采集任务', { body: { machine: 'machine-01' }, response: taskResponse }),
  endpoint('任务与自动化', 'POST', '/tasks/cluster-automation', '创建集群批量自动化或数据库巡检', { body: { clusters: ['prod'], target_machine_id: 'machine-01', operation: 'database_inspection', port: 3306 }, response: { operation: 'database_inspection', parent_task_id: 'task-parent', created: 1, failed: 0, items: [{ machine_id: 'machine-01', task_id: 'task-01' }] }, note: 'operation 支持 collect_machine、collect_mysql、database_inspection、database_deep_inspection 等受控动作；target_machine_id 可把任务限定到集群中的一台机器。' }),
  endpoint('任务与自动化', 'GET', '/tasks/cluster-automation/results?task_ids=task-01,task-02&operation=collect_machine', '汇总自动化结果', { query: ['task_ids', 'operation'], response: { operation: 'collect_machine', ready: true, pending: 0, failed: 0, rows: [] } }),
  endpoint('任务与自动化', 'GET', '/tasks/cluster-automation/report?task_ids=task-01,task-02&format=html', '下载自动化报告', { query: ['task_ids', 'format'], contentType: 'text/html 或 text/csv', response: '<html>...</html>' }),
  endpoint('任务与自动化', 'GET', '/tasks/cluster-automation/artifacts/{task_id}/{file_name}', '下载任务产物', { contentType: 'application/octet-stream', response: '二进制文件' }),
  endpoint('任务与自动化', 'GET', '/tasks/database-inspection/results?task_ids=task-01,task-02', '汇总数据库巡检结果', { query: ['task_ids'], response: { ready: true, pending: 0, failed: 0, targets: [{ task_id: 'task-01', machine_id: 'machine-01', port: 3306, level: 'standard', status: 'success', score: 92, passed: 11, warnings: 1, critical: 0 }], checks: [], exported_at: '2026-07-23T10:00:00Z' } }),
  endpoint('任务与自动化', 'GET', '/tasks/database-inspection/report?task_ids=task-01,task-02', '下载数据库巡检 Word 报告', { contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', response: 'DOCX 二进制文件' }),
  endpoint('任务与自动化', 'GET', '/tasks/database-inspection/data?task_ids=task-01,task-02', '导出数据库巡检 Excel 数据', { contentType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', response: 'XLSX 二进制文件' }),

  endpoint('MySQL 任务', 'POST', '/tasks/mysql-install', '安装 MySQL 实例', { body: { machine: 'machine-01', port: 3306, server_id: 101, mysql_user: 'mysql', root_password: '请替换', profile: 'prod', package_name: 'mysql-8.0.46-linux-glibc2.17-x86_64.tar.xz', version: '8.0.46', architecture: 'x86_64', install_pt_tools: true, install_xtrabackup: true }, response: { task: taskResponse, steps: [], events: [] } }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-lifecycle', '安全重启或关闭 MySQL', { body: { machine: 'machine-01', port: 3306, action: 'restart', confirmation: 'RESTART 10.0.0.11:3306', risk_acknowledged: true, primary_acknowledged: false, deep_data_check: true }, response: { task: taskResponse, steps: [], events: [] }, note: '先留存拓扑并检查事务、复制和可写状态；重启后再次校验拓扑与数据。confirmation 必须精确匹配 ACTION IP:PORT。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-uninstall', '卸载 MySQL 实例', { body: { machine: 'machine-01', port: 3306 }, response: { task: taskResponse, steps: [], events: [] } }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-users', '管理 MySQL 用户', { body: { machine: 'machine-01', port: 3306, action: 'create', target_username: 'app_user', target_password: '请替换', target_host: '10.%', privileges: ['SELECT', 'INSERT'] }, response: { task: taskResponse, steps: [], events: [] }, note: 'action 支持 list、query、create、update、delete、grant、revoke、lock、unlock；list 的结果通过 GET /tasks?id={task_id} 的 events 返回。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-indexes', '管理索引', { body: { machine: 'machine-01', port: 3306, action: 'create', schema: 'app', table: 'orders', name: 'idx_user_id', kind: 'btree', columns: [{ name: 'user_id', direction: 'ASC' }], purpose: '加速用户订单查询', impact: '降低扫描行数', lock_mode: 'none', lock_acknowledged: true }, response: { task: taskResponse, steps: [], events: [] }, note: 'action 支持 list、create、rename、delete。远端变更是异步任务；继续查询任务详情读取索引清单、步骤和最终状态。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-online-ddl', '执行在线 DDL', { body: { machine: 'machine-01', port: 3306, action: 'execute', schema: 'app', table: 'orders', alter: 'ADD COLUMN source varchar(32) NULL', purpose: '记录订单来源', impact: '新增可空字段，无数据回填', max_load_threads_running: 25, critical_threads_running: 50, max_lag_seconds: 10, chunk_time_seconds: 0.5, check_interval_seconds: 1, alter_foreign_keys_method: 'auto', risk_acknowledged: true, confirmation: 'app.orders' }, response: { task: taskResponse, steps: [], events: [] }, note: 'action= dry_run 或 execute；只提交 ALTER TABLE 后面的子句。execute 必须确认风险并精确匹配 schema.table。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-archive', '执行数据归档', { body: { machine: 'machine-01', port: 3306, action: 'execute', source_schema: 'app', source_table: 'orders', destination_schema: 'archive', destination_table: 'orders_2025', where: "created_at < '2026-01-01'", batch_size: 1000, sleep_seconds: 1, run_time_seconds: 3600, delete_source: false, risk_acknowledged: true, confirmation: 'app.orders->archive.orders_2025' }, response: { task: taskResponse, steps: [], events: [] }, note: 'action= dry_run 或 execute；execute 的 confirmation 必须精确匹配 source_schema.source_table->destination_schema.destination_table。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-parameters', '采集或修改 MySQL 参数', { body: { targets: [{ machine: 'machine-01', port: 3306, config_path: '/etc/my.cnf', systemd_unit: 'mysqld' }], restart_targets: [], restart_confirmed: false, changes: [{ action: 'update', name: 'max_connections', value: '1000' }] }, response: { parent: taskResponse, tasks: [{ task: taskResponse, steps: [], events: [] }], requires_restart: false, dynamic_count: 1, restart_count: 0 }, note: '采集使用 {machine, port, action:\"collect\"}。变更 action 只能为 update 或 delete；需要重启的参数必须显式给出 restart_targets 并设置 restart_confirmed=true。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-upgrade/precheck', 'MySQL 升级预检', { status: 202, body: { machine: 'machine-01', port: 3306, package_name: 'mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz' }, response: { current_version: '8.0.46', target_version: '8.4.6', checker: 'mysqlsh', task: { task: taskResponse, steps: [], events: [] } }, note: '先等待预检任务成功，再把该任务 ID 作为 precheck_task_id 提交升级。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-upgrade', '升级 MySQL 实例', { body: { machine: 'machine-01', port: 3306, package_name: 'mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz', precheck_task_id: 'task-01HX...', force: false, risk_acknowledged: true }, response: { current_version: '8.0.46', target_version: '8.4.6', forced: false, task: { task: taskResponse, steps: [], events: [] } } }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-topology', '配置复制拓扑', { body: { topology: 'primary-replica', port: 3306, replication_user: 'repl', replication_password: '请替换', primary_machine: 'machine-01', nodes: [{ machine: 'machine-01', port: 3306, role: 'primary' }, { machine: 'machine-02', port: 3306, role: 'replica', source_machine: 'machine-01' }] }, response: { parent: taskResponse, tasks: [] } }),
  endpoint('MySQL 任务', 'POST', '/tasks/cluster-mysql-install', '集群批量安装 MySQL', { body: { cluster: 'prod', port: 3306, server_id_start: 101, mysql_user: 'mysql', root_password: '请替换', profile: 'prod', version: '8.0.46', architecture: 'x86_64' }, response: { cluster: 'prod', parent: { task: { ID: 'task-parent', Status: 'pending' } }, created: 3, failed: 0, items: [] }, note: 'root_password 与 accounts[].password 只能通过安全表单提交，不得写入 AI 对话。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/cluster-mysql-uninstall', '集群批量卸载 MySQL', { body: { cluster: 'prod', port: 3306 }, response: { cluster: 'prod', parent: { task: { ID: 'task-parent', Status: 'pending' } }, created: 2, failed: 0, items: [] }, note: '极高风险：永久删除目标端口的全部集群实例。业务 VIP、备份策略或活动任务仍存在时，服务端不创建卸载任务。' }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-cluster-upgrade/plan', '生成滚动升级计划', { body: { cluster: 'prod', target_version: '8.4.6', port: 3306 }, response: { cluster: 'prod', target_version: '8.4.6', executable: true, blocking_reasons: [], nodes: [], stages: [] } }),
  endpoint('MySQL 任务', 'POST', '/tasks/mysql-cluster-upgrade/start', '启动滚动升级', { status: 202, body: { cluster: 'prod', target_version: '8.4.6', port: 3306, risk_acknowledged: true }, response: { run_id: 'run-01', status: 'running', current_stage: 'precheck', nodes: [], stages: [] } }),
  endpoint('MySQL 任务', 'GET', '/tasks/mysql-cluster-upgrade?run_id={run_id}', '查询滚动升级状态', { response: { run_id: 'run-01', status: 'running', current_stage: 'replicas', nodes: [], stages: [] } }),
  endpoint('MySQL 任务', 'GET', '/mysql/packages', '查询可用于 MySQL 任务的软件包', { response: [{ name: 'mysql-8.0.46-linux-glibc2.17-x86_64.tar.xz', version: '8.0.46', arch: 'x86_64' }] }),
  endpoint('MySQL 任务', 'GET', '/software/mysql/{file_name}', '下载 MySQL 软件包', { contentType: 'application/octet-stream', response: '二进制文件' }),

  endpoint('高可用', 'POST', '/clusters/{cluster_name}/bootstrap', '批量安装并初始化集群架构', { body: { architecture: 'master_slave', primary_machine_id: 'machine-01', enable_vip: true, vip: { vip_address: '10.0.0.100', vip_prefix: 24, default_interface: 'eth0' }, installs: [{ machine: '10.0.0.11', machine_id: 'machine-01', port: 3306, server_id: 101, mysql_user: 'mysql', root_password: '请替换', accounts: [] }, { machine: '10.0.0.12', machine_id: 'machine-02', port: 3306, server_id: 102, mysql_user: 'mysql', root_password: '请替换', accounts: [] }] }, response: { task: { id: 'cluster-bootstrap-01', type: 'mysql_cluster_bootstrap', status: 'pending' }, steps: [] }, note: '组合高风险工作流；至少两个安装目标，并复用 MySQL 安装、架构执行与 VIP 安全流程。' }),
  endpoint('高可用', 'GET', '/clusters/{cluster_name}/vip/config', '查询业务 VIP 配置', { response: [{ id: 1, cluster_id: 'prod', vip_name: '业务 VIP', vip_address: '10.0.0.100', vip_prefix: 24, vip_route_mode: 'L2_ARP', default_interface: 'eth0', enabled: true }] }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/vip/config', '保存并绑定业务 VIP', { body: { vip_name: '业务 VIP', vip_address: '10.0.0.100', vip_prefix: 24, default_interface: 'eth0', target_machine_id: 'machine-01', arping_count: 3 }, response: { task_id: 'task-01HX...', vip_address: '10.0.0.100', expected_holder_machine_id: 'machine-01', current_holder_machine_id: 'machine-01', current_interface: 'eth0', vip_status: 'BOUND' }, note: '高风险：按“全节点撤销旧地址 → 零持有者屏障 → 新目标绑定 → 两轮全节点唯一持有者复检”执行。地址必须由网络规划确认，不能让 AI 猜测。' }),
  endpoint('高可用', 'DELETE', '/clusters/{cluster_name}/vip/config?vip={vip_address}', '撤销并删除业务 VIP', { query: ['vip'], response: { deleted: true }, note: '极高风险：先从所有节点撤销地址，确认零持有者后才删除配置。' }),
  endpoint('高可用', 'GET', '/clusters/{cluster_name}/vip/status', '查询 VIP 实机绑定状态', { response: [{ vip_address: '10.0.0.100', vip_status: 'BOUND', current_holder_machine_id: 'machine-01', detected_holders: 'machine-01' }] }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/vip/scan', '通过 Agent 扫描 VIP 实机状态', { response: [{ task_id: 'task-parent', vip_address: '10.0.0.100', vip_status: 'BOUND', detected_holders: 'machine-01' }] }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/vip/adopt', '采纳策略允许的手工 VIP', { body: { vip: '10.0.0.100' }, response: { vip_address: '10.0.0.100', vip_status: 'BOUND' } }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/vip/validate', '验证全部 VIP 状态', { response: [{ vip_address: '10.0.0.100', vip_status: 'BOUND' }] }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/failover/plan', '生成故障切换计划', { response: { failover_id: 'fo-01', cluster_id: 'prod', status: 'INIT', reason: 'plan only; no operations executed' }, note: '只读计划，不执行切换。' }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/failover/start', '启动受保护的故障切换', { response: { failover_id: 'fo-01', cluster_id: 'prod', status: 'FAILED', reason: '...' }, note: '缺少旧主隔离或实时集成时安全停止，不会不安全地提升新主。' }),
  endpoint('高可用', 'GET', '/clusters/{cluster_name}/failover/{failover_id}', '查询故障切换状态', { response: { failover_id: 'fo-01', cluster_id: 'prod', status: 'FAILED', reason: '...' } }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/architecture/plan', '生成复制架构或 VIP 漂移预检计划', { body: { architecture: 'master_slave', current_architecture: 'dual_master', current_master_machine_id: 'machine-01', preferred_new_master_machine_id: 'machine-02', move_vip: true, nodes: [{ machine_id: 'machine-01', port: 3306, role: 'S', source_machine_id: 'machine-02' }, { machine_id: 'machine-02', port: 3306, role: 'M' }] }, response: { plan_id: 'plan-architecture-01', cluster_id: 'prod', architecture: 'master_slave', executable: true, blocking_reasons: [], steps: [] }, note: '只读预检，不执行变更。' }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/architecture/start', '启动复制架构调整或 VIP 漂移', { body: { architecture: 'master_slave', current_architecture: 'dual_master', current_master_machine_id: 'machine-01', preferred_new_master_machine_id: 'machine-02', move_vip: true, nodes: [{ machine_id: 'machine-01', port: 3306, role: 'S', source_machine_id: 'machine-02' }, { machine_id: 'machine-02', port: 3306, role: 'M' }] }, response: { run_id: 'architecture-01', cluster_id: 'prod', status: 'pending', plan: {}, request: {} }, note: '高风险：先调用 plan；服务端会冻结业务连接、等待复制追平、校验一致性、调整角色、漂移 VIP 并复检。' }),
  endpoint('高可用', 'GET', '/clusters/{cluster_name}/architecture/{run_id}', '查询架构调整状态', { response: { run_id: 'architecture-01', status: 'running', current_step: 'wait_replication', step_results: [], task_ids: [] } }),
  endpoint('高可用', 'POST', '/clusters/{cluster_name}/architecture/{run_id}/force', '确认复制未追平时强制继续', { response: { run_id: 'architecture-01', status: 'running', force_confirmed: true }, note: '极高风险，可能造成数据丢失；仅用于 waiting_force_confirmation。' }),

  endpoint('备份恢复', 'GET', '/backup/targets?cluster=prod', '查询可选备份与恢复目标', { query: ['cluster（可选；留空返回全部集群）'], response: [{ cluster: 'prod', machine_id: 'machine-02', machine_name: 'db-replica-01', machine_ip: '10.0.0.12', agent_status: 'agent_online', port: 3306, instance_status: 'running', mysql_version: '8.4.10', architecture: 'x86_64', package_name: 'mysql-8.4.10-linux-glibc2.28-x86_64', backup_ready: true, restore_ready: true }], note: '聚合机器与 MySQL 实例登记，不返回 SSH/MySQL 凭据或实例目录；实际执行前 Agent 仍会检查 XtraBackup 版本、磁盘和复制延迟。' }),
  endpoint('备份恢复', 'GET', '/backup/policies?cluster=prod', '查询备份策略', { query: ['cluster（可选）'], response: [] }),
  endpoint('备份恢复', 'GET', '/backup/policies/{policy_id}', '查询单个备份策略', { response: { id: 'policy-01', name: 'prod-daily', cluster: 'prod', machine_id: 'machine-02', port: 3306, backup_type: 'full', schedule_type: 'weekly', weekdays: [0, 1, 2, 3, 4, 5, 6], weekday_backup_types: { 0: 'full', 1: 'incremental' }, backup_location: '/backup/mysql', include_binlog: true, enabled: true }, note: 'mysql_password 永不返回。' }),
  endpoint('备份恢复', 'POST', '/backup/policies', '创建备份策略', { status: 201, body: { name: 'prod-daily', cluster: 'prod', machine_id: 'machine-02', port: 3306, backup_type: 'full', disk_usage_threshold: 95, schedule_type: 'weekly', weekdays: [1, 2, 3, 4, 5, 6, 0], weekday_backup_types: { 0: 'full', 1: 'incremental', 2: 'incremental', 3: 'incremental', 4: 'incremental', 5: 'full', 6: 'full' }, start_at: '2026-07-24T02:00:00+08:00', backup_location: '/backup/mysql', mysql_user: 'backup', mysql_password: '仅请求中传递', include_binlog: true, retry_count: 2, retry_interval_seconds: 60, enabled: true }, response: { id: 'policy-01', name: 'prod-daily', enabled: true } }),
  endpoint('备份恢复', 'PUT', '/backup/policies/{policy_id}', '更新备份策略', { body: { name: 'prod-daily', cluster: 'prod', machine_id: 'machine-02', port: 3306, backup_type: 'full', disk_usage_threshold: 90, schedule_type: 'weekly', weekdays: [0, 1, 2, 3, 4, 5, 6], start_at: '2026-07-24T02:00:00+08:00', backup_location: '/backup/mysql', mysql_user: 'backup', mysql_password: '', include_binlog: true, retry_count: 2, retry_interval_seconds: 60, enabled: true }, response: { id: 'policy-01', name: 'prod-daily', disk_usage_threshold: 90 }, note: 'mysql_password 留空时保留原密码；请求体若包含 id，必须与路径一致。' }),
  endpoint('备份恢复', 'DELETE', '/backup/policies/{policy_id}', '删除备份策略', { response: { id: 'policy-01' } }),
  endpoint('备份恢复', 'POST', '/backup/policies/{policy_id}/run', '立即运行备份策略', { status: 201, response: { id: 'run-01', policy_id: 'policy-01', status: 'pending', task_id: 'task-01HX...' } }),
  endpoint('备份恢复', 'GET', '/backup/runs?cluster=prod&limit=100', '查询备份记录', { query: ['cluster（可选）', 'limit（1–500，默认 100）'], response: [] }),
  endpoint('备份恢复', 'GET', '/backup/runs/{run_id}', '查询单条备份记录及执行日志', { response: { id: 'run-01', policy_id: 'policy-01', cluster: 'prod', machine_id: 'machine-02', machine_name: 'db-replica-01', machine_ip: '10.0.0.12', port: 3306, backup_type: 'full', backup_path: '/backup/mysql/prod/10.0.0.12_3306/20260723_run-01', task_id: 'task-01HX...', status: 'success', include_binlog: true, logs: [] } }),
  endpoint('备份恢复', 'POST', '/backup/cluster-runs', '批量运行集群备份', { status: 201, body: { clusters: ['prod', 'reporting'] }, response: { parent_task_id: 'task-parent', created: 3, failed: 0, items: [{ cluster: 'prod', policy_id: 'policy-01', policy: 'prod-daily', run_id: 'run-01', task_id: 'task-01HX...' }] }, note: '每个所选集群会触发全部已启用策略；所有子任务归入 parent_task_id，单条策略失败会记录在 items[].error。请求只引用服务端策略，不传递密码。' }),
  endpoint('备份恢复', 'POST', '/backup/runs/{run_id}/restore', '执行物理恢复、时间点恢复或数据闪回', { status: 201, body: { confirmation: 'RESTORE run-01', mode: 'physical', backup_path: '/backup/mysql/prod/10.0.0.12_3306/20260723_run-01', mysql_user: 'root', mysql_password: '仅请求中传递', repair_replication: true }, response: { task: { ID: 'task-restore-01', Type: 'exec', MachineID: 'machine-02', Status: 'pending', ProgressPercent: 0 }, steps: [], events: [] }, note: 'mode 取 physical、point_in_time 或 flashback。physical/point_in_time 的确认短语是 RESTORE {run_id}；flashback 是 FLASHBACK {run_id}。point_in_time 还必须提供 restore_time，且原备份 include_binlog=true；flashback 可用 database、tables、output_dir、apply_flashback。Task 领域对象沿用大写字段名。' }),

  endpoint('安装包', 'GET', '/packages?category=mysql&keyword=8.0', '查询安装包与仓库设置', { query: ['category', 'keyword'], response: { items: [], settings: { storage_path: './software', categories: [], catalog: [], bundles: [] } } }),
  endpoint('安装包', 'POST', '/packages', '上传安装包', { status: 201, contentType: 'multipart/form-data', body: { file: '<binary>', category: 'mysql', arch: 'x86_64', version: '8.0.46', description: 'MySQL Community Server' }, response: { category: 'mysql', name: 'mysql-8.0.46.tar.xz', size: 104857600, sha256: '...' } }),
  endpoint('安装包', 'POST', '/packages/fetch', '从内置目录下载单个包', { status: 201, body: { catalog_id: 'mysql-8.0.46-x86_64' }, response: { category: 'mysql', name: 'mysql-8.0.46.tar.xz', verified: true } }),
  endpoint('安装包', 'POST', '/packages/fetch-bundle', '下载推荐软件包组合', { status: 201, body: { bundle_id: 'mysql-8.0.46-x86_64' }, response: { bundle_id: 'mysql-8.0.46-x86_64', complete: true, installed: [], failed: [] }, note: '部分成功时返回 207 Multi-Status。' }),
  endpoint('安装包', 'POST', '/packages/verify', '校验安装包', { body: { category: 'mysql', name: 'mysql-8.0.46.tar.xz' }, response: { name: 'mysql-8.0.46.tar.xz', sha256: '...', verified: true } }),
  endpoint('安装包', 'GET', '/packages/{category}/{file_name}', '下载安装包', { contentType: 'application/octet-stream', response: '二进制文件' }),
  endpoint('安装包', 'DELETE', '/packages/{category}/{file_name}', '删除安装包', { status: 204, response: null }),
  endpoint('安装包', 'POST', '/packages/delete', '通过表单删除安装包', { status: 303, contentType: 'application/x-www-form-urlencoded', body: { category: 'mysql', name: 'mysql-8.0.46.tar.xz' }, response: '303 See Other，Location: /', note: '兼容传统表单的入口；API 客户端优先使用 DELETE /packages/{category}/{file_name}。' }),
  endpoint('安装包', 'GET', '/package-settings', '查询仓库设置', { response: { storage_path: './software', categories: [], catalog: [], bundles: [] } }),
  endpoint('安装包', 'PUT', '/package-settings', '修改仓库存储路径', { body: { storage_path: '/data/gmha/software' }, response: { storage_path: '/data/gmha/software', categories: [] } }),
  endpoint('安装包', 'POST', '/package-settings', '通过表单修改仓库存储路径', { status: 303, contentType: 'application/x-www-form-urlencoded', body: { storage_path: '/data/gmha/software' }, response: '303 See Other，Location: /', note: '兼容传统表单的入口；API 客户端优先使用 PUT。' }),

  endpoint('AI 与自动化', 'GET', '/ai', '读取 AI 运维工作台', { response: { providers: [], settings: { enabled: false, always_confirm_high_risk: true }, messages: [], plans: [], workflows: [], runs: [], actions: [], stats: {} }, note: '提供商凭据不会返回浏览器；高危确认策略由服务端强制开启。工作流包含子操作、依赖、检查点和父任务编号。' }),
  endpoint('AI 与自动化', 'GET', '/ai/capabilities', '发现 AI 动作及完整集群 API 契约', { response: { api_version: 'v1', actions: [{ id: 'configure_cluster_vip', label: '配置并绑定集群 VIP', risk: 'high', target_kind: 'cluster', http_method: 'POST', api_path: '/api/v1/clusters/{cluster_name}/vip/config', parameters: [] }], cluster_endpoints: [{ id: 'clusters.mysql.install', method: 'POST', path: '/api/v1/tasks/cluster-mysql-install', invocation_mode: 'secure_input_api', sensitive_parameters: ['root_password', 'accounts[].password'] }], security_boundary: 'secure_input_api 参数只能通过受保护表单或密钥通道提交，不得写入模型对话' }, note: 'actions 是内置 AI 可审批执行的白名单；cluster_endpoints 覆盖集群管理全部接口。secure_input_api 表示 API 已存在但密钥必须走安全输入，并非平台不支持。' }),
  endpoint('AI 与自动化', 'POST', '/ai/providers', '接入大模型', { body: { name: '生产运维模型', type: 'deepseek', base_url: 'https://api.deepseek.com', model: 'deepseek-v4-flash', api_key: '请替换', enabled: true, is_default: true }, response: { id: 'provider-01', name: '生产运维模型', has_api_key: true, api_key: '••••••••' }, note: 'API 密钥由 Manager 使用 AES-256-GCM 加密保存。远程模型地址必须使用 HTTPS。' }),
  endpoint('AI 与自动化', 'PUT', '/ai/providers', '更新模型配置', { body: { id: 'provider-01', name: '生产运维模型', type: 'deepseek', base_url: 'https://api.deepseek.com', model: 'deepseek-v4-flash', api_key: '••••••••', enabled: true, is_default: true }, response: { id: 'provider-01', has_api_key: true } }),
  endpoint('AI 与自动化', 'DELETE', '/ai/providers?id={provider_id}', '移除模型配置', { response: { deleted: true } }),
  endpoint('AI 与自动化', 'POST', '/ai/providers/test', '测试模型连接', { body: { id: 'provider-01' }, response: { status: 'connected' } }),
  endpoint('AI 与自动化', 'PUT', '/ai/settings', '保存自动化安全策略', { body: { enabled: true, default_provider_id: 'provider-01', auto_analyze_alerts: true, analysis_interval_minutes: 15, analysis_scope: 'all', auto_execute_low_risk: true, require_approval_medium: true, always_confirm_high_risk: true, allowed_actions: ['create_cluster', 'update_cluster', 'register_cluster_members', 'remove_cluster_members', 'configure_cluster_vip', 'remove_cluster_vip', 'scan_cluster_vip', 'configure_cluster_architecture', 'run_cluster_backup', 'rolling_upgrade_cluster_mysql', 'uninstall_cluster_mysql', 'cleanup_cluster', 'delete_cluster'] }, response: { enabled: true, always_confirm_high_risk: true }, note: '服务端始终把 always_confirm_high_risk 设为 true，客户端无法关闭；升级前使用默认动作集合的环境会自动加入新增的受控动作。' }),
  endpoint('AI 与自动化', 'POST', '/ai/chat', '向 AI 运维助手提问', { body: { session_id: 'default', provider_id: 'provider-01', message: '先诊断 DB-01，再根据结果安全修复' }, response: { message: { role: 'assistant', content: '已结合平台架构生成分步方案。' }, plans: [], workflows: [] }, note: '模型只读取脱敏上下文并提出白名单动作；Manager 把同一目标的多个动作编排为持久化父子工作流。' }),
  endpoint('AI 与自动化', 'POST', '/ai/analyze', '立即执行全局 AI 分析', { body: { provider_id: 'provider-01' }, response: { id: 'run-01', status: 'succeeded', findings: [], plan_ids: [] } }),
  endpoint('AI 与自动化', 'POST', '/ai/plans/execute', '审批并启动 AI 工作流', { body: { id: 'plan-01', approved: true, confirmation: '确认执行工作流 DB-01（2项）' }, response: { id: 'plan-01', workflow_id: 'workflow-01', status: 'executing' }, note: '一次审批覆盖经过展示的完整工作流；高风险和极高风险仍必须精确匹配确认短语。每个子操作提交前都会重新预检。' }),
  endpoint('AI 与自动化', 'POST', '/ai/plans/reject', '拒绝 AI 执行计划', { body: { id: 'plan-01' }, response: { id: 'plan-01', status: 'rejected' } }),
  endpoint('AI 与自动化', 'POST', '/ai/workflows/pause', '暂停工作流后续步骤', { body: { id: 'workflow-01' }, response: { id: 'workflow-01', status: 'paused', resume_required: true }, note: '已提交的子任务继续被监控；暂停只阻止新步骤启动。' }),
  endpoint('AI 与自动化', 'POST', '/ai/workflows/resume', '从安全检查点恢复工作流', { body: { id: 'workflow-01' }, response: { id: 'workflow-01', status: 'running' }, note: '恢复前重新读取架构、监控、告警和活动任务；提交结果不明确的步骤禁止自动重试。' }),

  endpoint('告警', 'GET', '/alerts/rules', '查询告警规则', { response: [] }),
  endpoint('告警', 'POST', '/alerts/rules', '创建告警规则', { body: { name: '复制延迟过高', metric: 'mysql_replication_lag', operator: '>', threshold: 30, duration_seconds: 60, severity: 'critical', enabled: true, channel_ids: ['channel-01'] }, response: { id: 'rule-01', name: '复制延迟过高', enabled: true } }),
  endpoint('告警', 'PUT', '/alerts/rules', '更新告警规则', { body: { id: 'rule-01', name: '复制延迟过高', metric: 'mysql_replication_lag', operator: '>', threshold: 60, duration_seconds: 60, severity: 'critical', enabled: true }, response: { id: 'rule-01', threshold: 60 } }),
  endpoint('告警', 'DELETE', '/alerts/rules?id={rule_id}', '删除告警规则', { response: deletedResponse }),
  endpoint('告警', 'GET', '/alerts/filters', '查询告警过滤器', { response: [] }),
  endpoint('告警', 'POST', '/alerts/filters', '创建告警过滤器', { body: { name: '忽略测试集群', cluster_id: 'test', enabled: true }, response: { id: 'filter-01', enabled: true } }),
  endpoint('告警', 'PUT', '/alerts/filters', '更新告警过滤器', { body: { id: 'filter-01', name: '忽略测试集群', cluster_id: 'test', enabled: false }, response: { id: 'filter-01', enabled: false } }),
  endpoint('告警', 'DELETE', '/alerts/filters?id={filter_id}', '删除告警过滤器', { response: deletedResponse }),
  endpoint('告警', 'GET', '/alerts/events?status=firing&severity=critical&cluster_id=prod&limit=100&offset=0', '查询告警事件', { query: ['status', 'severity', 'cluster_id', 'keyword', 'limit', 'offset'], response: [] }),
  endpoint('告警', 'POST', '/alerts/events/action', '确认、静默或关闭告警', { body: { ID: 'event-01', Action: 'acknowledge', Actor: 'dba', silence_seconds: 0 }, response: { updated: true }, note: '静默支持 1、2、3、5、12 或 24 小时。' }),
  endpoint('告警', 'POST', '/alerts/events/automation', '更新告警自动化状态', { body: { id: 'event-01', state: 'running', expected_state: 'pending' }, response: { updated: true } }),
  endpoint('告警', 'PUT', '/alerts/events/automation', '以幂等方式更新告警自动化状态', { body: { id: 'event-01', state: 'running', expected_state: 'pending' }, response: { updated: true } }),
  endpoint('告警', 'GET', '/alerts/channels', '查询通知渠道', { response: [{ id: 'channel-01', name: 'DBA Webhook', type: 'webhook', config: { url: '******' } }], note: '密码、令牌、密钥和 URL 会被掩码。' }),
  endpoint('告警', 'POST', '/alerts/channels', '创建通知渠道', { body: { name: 'DBA Webhook', type: 'webhook', config: { url: 'https://example.invalid/webhook' }, enabled: true }, response: { id: 'channel-01', config: { url: '******' } } }),
  endpoint('告警', 'PUT', '/alerts/channels', '更新通知渠道', { body: { id: 'channel-01', name: 'DBA Webhook', type: 'webhook', config: { url: '******' }, enabled: true }, response: { id: 'channel-01', config: { url: '******' } } }),
  endpoint('告警', 'DELETE', '/alerts/channels?id={channel_id}', '删除通知渠道', { response: deletedResponse }),
  endpoint('告警', 'POST', '/alerts/channels/test', '测试通知渠道', { body: { id: 'channel-01', name: 'DBA Webhook', type: 'webhook', config: { url: '******' } }, response: { status: 'delivered' } }),
  endpoint('告警', 'GET', '/alerts/deliveries?limit=100', '查询通知投递记录', { response: [] }),
  endpoint('告警', 'GET', '/alerts/metrics', '查询可用告警指标与运行状态', { response: { host: {}, mysql: {}, catalog: [], runtime: {} } }),
  endpoint('告警', 'GET', '/alerts/summary', '查询告警统计', { response: { counts: {}, total: 0, active_acknowledged: 0, active_silenced: 0, last_24_hours: 0, runtime: {} } }),
  endpoint('告警', 'GET', '/alerts/export/prometheus', '导出 Prometheus 指标', { contentType: 'text/plain; version=0.0.4', response: '# HELP gmha_alert_info Active GMHA alerts\n# TYPE gmha_alert_info gauge\n...' }),
  endpoint('告警', 'GET', '/alerts/export/zabbix', '导出 Zabbix 数据', { response: [{ host: 'machine-01', key: 'gmha.alert.mysql_replication_lag', value: 65, severity: 'critical', clock: 1784800800 }] }),

  endpoint('Manager 与升级', 'GET', '/manager/status', '查询 Manager 运行状态', { response: { running: true, pid: 1234, version: 'v1.2.0', started_at: '2026-07-23T08:00:00Z', config: {} } }),
  endpoint('Manager 与升级', 'GET', '/manager/config', '读取 Manager 配置', { response: { listen_http: ':8080', listen_grpc: ':9100', database_driver: 'sqlite', db_path: './data/manager.db' } }),
  endpoint('Manager 与升级', 'PUT', '/manager/config', '保存 Manager 配置', { body: { listen_http: ':8080', listen_grpc: ':9100', manager_http_addr: 'http://10.0.0.10:8080', manager_grpc_addr: '10.0.0.10:9100', database_driver: 'mysql', database_host: '10.0.0.20', database_port: 3306, database_name: 'gmha', database_username: 'gmha', database_password: '请替换', test_token: 'database-test-token', agent_binary_path: './bin/agentd' }, response: { listen_http: ':8080', listen_grpc: ':9100', database_driver: 'mysql', database_host: '10.0.0.20', database_password_set: true }, note: '数据库发生变化时必须携带十分钟内检测成功返回的 test_token；密码和 DSN 不会回显。' }),
  endpoint('Manager 与升级', 'POST', '/manager/database/test', '测试 Manager 数据库连接', { body: { database_driver: 'mysql', database_host: '10.0.0.20', database_port: 3306, database_name: 'gmha', database_username: 'gmha', database_password: '请替换' }, response: { ok: true, message: '数据库连接成功，可以保存配置', test_token: 'database-test-token', driver: 'mysql', address: '10.0.0.20:3306/gmha' } }),
  endpoint('Manager 与升级', 'POST', '/manager/start', '启动 Manager Runtime', { body: { config: { listen_http: ':8080', listen_grpc: ':9100' } }, response: { running: true, pid: 1234, config: {} } }),
  endpoint('Manager 与升级', 'POST', '/manager/restart', '重启 Manager Runtime', { body: { config: { listen_http: ':8080', listen_grpc: ':9100' } }, response: { running: true, pid: 1235, config: {} } }),
  endpoint('Manager 与升级', 'POST', '/manager/stop', '关闭当前 Manager Runtime', { response: { running: false }, note: '响应返回后当前进程延迟退出，控制台连接会中断；可由 systemd、启动器或其他 Manager 节点恢复。' }),
  endpoint('Manager 与升级', 'GET', '/manager/ha', '查询 Manager 高可用概览与拓扑', { response: { config: { enabled: true, vip: '10.0.0.100', prefix: 24, interface: 'eth0' }, nodes: [], active_node_id: 'manager-01', current_node_id: 'manager-01', shared_database: true, ready: true, warnings: [] } }),
  endpoint('Manager 与升级', 'PUT', '/manager/ha/config', '保存 Manager 高可用与 VIP 配置', { body: { enabled: true, vip: '10.0.0.100', prefix: 24, interface: 'eth0', install_dir: '/opt/gmha', service_name: 'gmha-manager' }, response: { enabled: true, vip: '10.0.0.100', prefix: 24, interface: 'eth0' }, note: '启用 Manager 高可用前必须切换到 MySQL 或 PostgreSQL 共享元数据库。' }),
  endpoint('Manager 与升级', 'GET', '/manager/ha/interfaces?node_id={manager_node_id}&vip=10.0.0.100&prefix=24', '读取 Manager 节点网络接口', { query: ['node_id', 'vip', 'prefix'], response: { node_id: 'manager-02', node_name: 'manager-02', interfaces: [{ name: 'eth0', ips: ['10.0.0.12'], recommended: true, reason: '与 Manager VIP 位于同一网段' }], recommended: 'eth0' } }),
  endpoint('Manager 与升级', 'POST', '/manager/ha/interfaces?node_id={manager_node_id}&vip=10.0.0.100&prefix=24', '重新探测 Manager 节点网络接口', { query: ['node_id', 'vip', 'prefix'], response: { node_id: 'manager-02', node_name: 'manager-02', interfaces: [{ name: 'eth0', ips: ['10.0.0.12'], recommended: true, reason: '与 Manager VIP 位于同一网段' }], recommended: 'eth0' }, note: 'POST 会触发目标节点重新采集网卡信息，系统优先推荐与 VIP 同网段的接口。' }),
  endpoint('Manager 与升级', 'POST', '/manager/ha/nodes', '安装并加入 Manager 节点', { status: 202, body: { machine_id: 'machine-02', http_port: 8080, grpc_port: 9100, interface: 'eth0', install_dir: '/opt/gmha' }, response: { id: 'manager-02', role: 'standby', state: 'installing', task_id: 'task-01HX...' } }),
  endpoint('Manager 与升级', 'POST', '/manager/ha/nodes/action', '执行 Manager 节点动作', { body: { node_id: 'manager-02', action: 'restart' }, response: { node_id: 'manager-02', status: 'pending' } }),
  endpoint('Manager 与升级', 'POST', '/manager/ha/vip/switch', '切换 Manager VIP', { body: { target_node_id: 'manager-02', interface: 'eth0' }, response: { target_node_id: 'manager-02', switched: true } }),
  endpoint('Manager 与升级', 'GET', '/manager/ha/bootstrap/config?token={bootstrap_token}', '下载 Manager 高可用引导配置', { contentType: 'text/plain', response: 'GMHA_MANAGER_...', note: '仅供一次性引导任务使用，需要短期令牌。' }),
  endpoint('Manager 与升级', 'GET', '/manager/ha/bootstrap/binary?token={bootstrap_token}', '下载 Manager 高可用引导二进制', { contentType: 'application/octet-stream', response: '二进制文件', note: '仅供一次性引导任务使用，需要短期令牌。' }),
  endpoint('Manager 与升级', 'GET', '/upgrades/overview', '查询升级概览', { response: { manager_version: 'v1.2.0', agent_total: 3, agent_versions: [], manager_packages: [], agent_packages: [], storage: {} } }),
  endpoint('Manager 与升级', 'GET', '/upgrades/jobs', '查询组件升级记录', { response: { items: [] } }),
  endpoint('Manager 与升级', 'GET', '/upgrades/{job_id}', '查询升级任务', { response: { id: 'upgrade-01', component: 'agent', status: 'running', current_version: 'v1.2.0', target_version: 'v1.3.0' } }),
  endpoint('Manager 与升级', 'POST', '/upgrades/agent', '批量升级 Agent', { status: 202, body: { package_name: 'gmha-agent-v1.3.0-linux-amd64', targets: ['10.0.0.11', '10.0.0.12'] }, response: { id: 'upgrade-01', component: 'agent', status: 'pending' } }),
  endpoint('Manager 与升级', 'POST', '/upgrades/manager', '升级 Manager', { status: 202, body: { package_name: 'gmha-manager-v1.3.0-linux-amd64' }, response: { id: 'upgrade-02', component: 'manager', status: 'pending' } }),
  endpoint('Manager 与升级', 'POST', '/upgrades/manager/rebuild', '重编译、安装并重启 Manager 内核', { status: 202, body: { source_dir: '/opt/gmha-src', confirmation: 'REBUILD' }, response: { id: 'upgrade-rebuild-01', component: 'manager-build', status: 'pending' }, note: '服务端从指定本地源码目录执行 Go 编译；候选自检通过后备份、原子替换并重启。' }),

  endpoint('采集配置', 'GET', '/dynamic-collect/config', '读取主机动态采集配置', { response: { enabled: true, version: '20260723T100000Z', updated_at: '2026-07-23T10:00:00Z', tasks: [{ name: 'cpu_usage_percent', enabled: true, type: 'builtin', category: 'cpu', interval_seconds: 5, timeout_seconds: 1 }] } }),
  endpoint('采集配置', 'PUT', '/dynamic-collect/config', '更新主机动态采集配置', { body: { enabled: true, tasks: [{ name: 'cpu_usage_percent', enabled: true, type: 'builtin', category: 'cpu', interval_seconds: 5, timeout_seconds: 1 }] }, response: { enabled: true, version: '20260723T100000Z', tasks: [{ name: 'cpu_usage_percent', enabled: true, type: 'builtin', interval_seconds: 5, timeout_seconds: 1 }] }, note: '建议先 GET 完整配置，在原 tasks 数组上修改后 PUT，避免覆盖未包含的采集器。' }),
  endpoint('采集配置', 'POST', '/dynamic-collect/config', '更新主机动态采集配置（兼容方法）', { body: { enabled: true, tasks: [{ name: 'cpu_usage_percent', enabled: true, type: 'builtin', interval_seconds: 5, timeout_seconds: 1 }] }, response: { enabled: true, version: '20260723T100000Z', tasks: [{ name: 'cpu_usage_percent', enabled: true, type: 'builtin', interval_seconds: 5, timeout_seconds: 1 }] } }),
  endpoint('采集配置', 'GET', '/mysql-dynamic-collect/config', '读取 MySQL 动态采集配置', { response: { enabled: true, version: '20260723T100000Z', updated_at: '2026-07-23T10:00:00Z', tasks: [{ name: 'mysql_threads_running', enabled: true, type: 'builtin', category: 'performance', interval_seconds: 5, timeout_seconds: 1 }] } }),
  endpoint('采集配置', 'PUT', '/mysql-dynamic-collect/config', '更新 MySQL 动态采集配置', { body: { enabled: true, tasks: [{ name: 'mysql_threads_running', enabled: true, type: 'builtin', category: 'performance', interval_seconds: 5, timeout_seconds: 1 }] }, response: { enabled: true, version: '20260723T100000Z', tasks: [{ name: 'mysql_threads_running', enabled: true, type: 'builtin', interval_seconds: 5, timeout_seconds: 1 }] }, note: 'MySQL 采集间隔最小 5 秒；建议先 GET 完整配置，在原 tasks 数组上修改后 PUT。' }),
  endpoint('采集配置', 'POST', '/mysql-dynamic-collect/config', '更新 MySQL 动态采集配置（兼容方法）', { body: { enabled: true, tasks: [{ name: 'mysql_threads_running', enabled: true, type: 'builtin', interval_seconds: 5, timeout_seconds: 1 }] }, response: { enabled: true, version: '20260723T100000Z', tasks: [{ name: 'mysql_threads_running', enabled: true, type: 'builtin', interval_seconds: 5, timeout_seconds: 1 }] } })
]

// The instance workspace exposes fourteen tabs. Keep this matrix next to the
// endpoint catalog so a user can verify that every visible operation is also
// available to an API client, including how to retrieve asynchronous results.
export const instanceManagementOperations = [
  { name: '实例', mode: '查询 / 生命周期', apis: ['GET /mysql/instances', 'POST /tasks/mysql-lifecycle', 'POST /tasks/mysql-uninstall', 'DELETE /mysql/instances'] },
  { name: '数据库巡检', mode: '异步任务 + 报告', apis: ['POST /tasks/cluster-automation', 'GET /tasks/database-inspection/results', 'GET /tasks/database-inspection/report', 'GET /tasks/database-inspection/data'] },
  { name: '执行计划', mode: '同步只读', apis: ['POST /sql-diagnostics/explain'] },
  { name: '在线 DDL', mode: '预演 / 执行', apis: ['POST /tasks/mysql-online-ddl', 'GET /tasks?id={task_id}'] },
  { name: '索引管理', mode: '查询 / 变更', apis: ['POST /tasks/mysql-indexes', 'GET /tasks?id={task_id}'] },
  { name: '直方图', mode: '查询 / 更新 / 删除', apis: ['GET /mysql/histograms', 'POST /mysql/histograms', 'DELETE /mysql/histograms'] },
  { name: '数据归档', mode: '预演 / 执行', apis: ['POST /tasks/mysql-archive', 'GET /tasks?id={task_id}'] },
  { name: 'binlog分析', mode: '异步分析', apis: ['GET /mysql/binlog-analysis', 'POST /mysql/binlog-analysis', 'GET /mysql/binlog-analysis/{task_id}', 'DELETE /mysql/binlog-analysis/{task_id}'] },
  { name: '创建安装', mode: '制品查询 / 安装', apis: ['GET /mysql/packages', 'POST /tasks/mysql-install', 'POST /clusters/{cluster_name}/bootstrap'] },
  { name: '用户管理', mode: '查询 / 变更', apis: ['POST /tasks/mysql-users', 'GET /tasks?id={task_id}'] },
  { name: '预设账号', mode: '查询 / 保存', apis: ['GET /mysql/account-presets', 'PUT /mysql/account-presets'] },
  { name: '参数管理', mode: '采集 / 变更', apis: ['POST /tasks/mysql-parameters', 'GET /tasks?id={task_id}'] },
  { name: 'Agent采集', mode: '查询 / 保存', apis: ['GET /mysql-dynamic-collect/config', 'PUT /mysql-dynamic-collect/config'] },
  { name: '版本升级', mode: '预检 / 单机 / 滚动', apis: ['POST /tasks/mysql-upgrade/precheck', 'POST /tasks/mysql-upgrade', 'POST /tasks/mysql-cluster-upgrade/plan', 'POST /tasks/mysql-cluster-upgrade/start', 'GET /tasks/mysql-cluster-upgrade?run_id={run_id}'] }
]

export const manualModules = [
  {
    id: 'overview',
    number: '01',
    title: '运行概览',
    scope: '平台入口',
    summary: '快速判断 Manager、Agent、机器、MySQL 与任务是否处于健康状态。',
    steps: ['先看顶部核心指标确认异常范围', '查看最近任务定位失败操作', '进入对应集群查看拓扑、容量和性能趋势'],
    principle: '概览聚合 Manager 运行状态、资源台账、Agent 心跳和任务统计，只做读模型汇总，不直接改变远端状态。',
    implementation: '前端并行调用 Manager、机器、集群、Agent、实例与任务接口；后端服务层统一读取持久化状态和最新心跳快照。',
    caution: '概览值是当前快照；趋势判断应进入集群性能页选择合适时间范围。'
  },
  {
    id: 'machines',
    number: '02',
    title: '机器与 SSH 凭证',
    scope: '资源管理',
    summary: '纳管 Linux 主机、复用 SSH 凭证、采集资产并维护机器与集群关系。',
    steps: ['先创建密码或私钥凭证', '点击“纳管机器”并完成 SSH/环境预检', '等待 Agent 安装与心跳上线', '按需加入集群并重新采集静态信息'],
    principle: 'Manager 只在引导、修复和卸载阶段使用 SSH；纳管后日常操作通过 Agent 任务通道执行。',
    implementation: '纳管请求进入 MachineService，完成连接预检、机器记录持久化、Agent 分发与注册等待；凭证密文不通过列表接口回显。',
    caution: '删除前先运行预检。解除纳管、卸载 Agent、卸载 MySQL 是三个不同影响等级的动作。'
  },
  {
    id: 'agents',
    number: '03',
    title: 'Agent 管理',
    scope: '资源管理',
    summary: '查看在线状态、资源占用和版本，执行重装、修复、升级、卸载与手动恢复。',
    steps: ['按状态或版本筛选异常节点', '查看最后心跳与健康检查', '优先执行配置修复或版本识别', '离线时使用手动恢复并观察恢复流程'],
    principle: 'Agent 周期上报心跳和采集结果，同时保持任务 WebSocket 通道；Manager 根据心跳租约判断在线状态。',
    implementation: '心跳服务保存最新快照与时间序列，任务分发器向在线 Agent 推送命令，步骤和事件持续写回任务中心。',
    caution: '“在线”表示心跳正常，不等同于 MySQL 实例健康；还需检查实例指标和告警。'
  },
  {
    id: 'clusters',
    number: '04',
    title: '集群列表与成员',
    scope: '集群运维',
    summary: '建立业务集群边界，管理成员、查看拓扑并进入集群级运维工作台。',
    steps: ['创建集群并填写用途', '从未分配机器中选择成员', '确认 Agent 覆盖率与 MySQL 实例登记', '进入详情检查拓扑关系'],
    principle: '集群是 GMHA 的逻辑运维边界，成员关系连接机器台账、实例拓扑、告警、备份和批量任务。',
    implementation: 'MachineService 维护成员关系；拓扑接口将实例角色、复制边、心跳指标与存储信息组装为统一视图。',
    caution: '删除集群前应清空成员；清理操作只修复失效关系，不会直接卸载远端软件。'
  },
  {
    id: 'cluster-architecture',
    number: '05',
    title: '架构、复制、VIP 与故障切换',
    scope: '集群运维',
    summary: '设计主从关系、预览变更风险、执行架构调整并管理业务 VIP。',
    steps: ['在架构画布中设定角色与复制来源', '先生成执行计划并逐项查看风险', '确认窗口和回退方案后启动', '观察步骤结果和心跳收敛', '最后校验 VIP 实际归属'],
    principle: '所有拓扑改变都采用“期望状态 → 计划 → 风险校验 → 任务执行 → 实际状态收敛”模型。',
    implementation: 'ArchitecturePlanner 计算差异和顺序，HAService 创建父子任务，Agent 执行复制/VIP 命令，心跳结果用于最终校验。',
    caution: '强制继续会绕过部分保护条件，只应在已核对复制位点、数据一致性和业务流量后使用。'
  },
  {
    id: 'cluster-observability',
    number: '06',
    title: '性能监控与容量',
    scope: '集群运维',
    summary: '按集群或实例观察 QPS/TPS、连接、锁、事务、复制、CPU、IO、磁盘和网络。',
    steps: ['先选择集群时间范围', '从总览识别异常指标', '切换到单指标时间范围放大问题窗口', '结合 SQL 诊断和火焰图定位原因'],
    principle: '指标目录描述采集间隔、单位和聚合方式；查询层按范围与步长读取心跳时间序列并聚合。',
    implementation: 'Agent 内置采集器生成主机/MySQL 指标，HeartbeatService 持久化样本，PerformanceHandler 提供目录和时序 API。',
    caution: '集群聚合值可能掩盖单机热点；发现异常后切换实例或机器维度。'
  },
  {
    id: 'mysql',
    number: '07',
    title: 'MySQL 实例与安装',
    scope: '数据库管理',
    summary: '安装、登记、卸载实例，并按版本与架构选择软件包、目录、参数和初始化账号。',
    steps: ['先在安装包管理准备匹配架构的软件包', '选择机器、端口、server_id 和目录', '核对版本兼容提示与初始化账号', '提交任务并在任务中心观察每一步'],
    principle: '安装配置先在 Manager 侧验证和模板化，再由 Agent 在目标机解压、初始化、写配置并注册 systemd。',
    implementation: 'TaskService 把安装请求编排为幂等步骤；Agent 使用内置处理器执行并返回结构化结果，成功后登记实例。',
    caution: '同一机器端口与 server_id 必须唯一；密码只在提交时使用，不应写入脚本或日志。'
  },
  {
    id: 'mysql-governance',
    number: '08',
    title: '参数、账号、索引与直方图',
    scope: '数据库管理',
    summary: '以受控表单替代任意命令，执行常见配置和结构治理。',
    steps: ['选择目标实例并读取当前状态', '填写期望变更并查看 SQL/配置预览', '确认动态生效或重启范围', '提交后核对任务结果与最终状态'],
    principle: '请求只接受结构化字段；后端生成受限 SQL 或配置变更，避免把任意凭证和命令暴露给浏览器。',
    implementation: '版本兼容层决定变量名、动态权限和重启要求；批量参数变更按实例拆成子任务并保留父任务关系。',
    caution: '索引和重启类变更必须评估锁、复制延迟与业务窗口；先使用预览或在线方案。'
  },
  {
    id: 'mysql-data-ops',
    number: '09',
    title: '在线 DDL、归档、巡检与升级',
    scope: '数据库管理',
    summary: '执行大表在线结构变更、数据归档、数据库巡检及带兼容性门禁的滚动升级。',
    steps: ['先检查目标表、磁盘、事务和复制状态', '设置负载、延迟、锁等待和执行范围门禁', '完成预检或 dry-run 后再执行', '持续观察进度、报告与校验产物'],
    principle: '写操作使用专用工具的在线算法并加 GMHA 风险门禁；升级采用与目标实例绑定的预检和分阶段健康校验。',
    implementation: 'Agent 使用短期 defaults 文件调用在线变更、归档、检查或升级程序；Manager 只编排结构化参数并保存任务证据。',
    caution: '归档删除、结构变更和版本升级均可能放大磁盘、网络及复制压力，必须从小范围开始并保留恢复路径。'
  },
  {
    id: 'binlog-analysis',
    number: '10',
    title: 'Binlog 分析',
    scope: '数据库管理',
    summary: '按时间或起始文件只读解析 ROW 事件，定位写入热点、历史复制延迟、DDL 和大事务。',
    steps: ['选择已登记实例和较短时间范围', '按行数或字节设置大事务阈值', '提交任务并观察文件级进度', '查看表聚合、延迟趋势、DDL 与大事务结果'],
    principle: 'Manager 使用 MySQL 复制协议读取 Binlog，不执行写 SQL；事务边界、Rotate、GTID 与行事件在解析器中还原。',
    implementation: '分析服务从启用的 MHA 账号预设解析凭据，限制全局并发和结果内存，按文件自适应并行并支持取消。',
    caution: '分析会消耗源库网络、Binlog 发送线程和 Manager CPU；任务及结果当前保存在内存中，Manager 重启后不会保留。'
  },
  {
    id: 'sql-diagnostics',
    number: '10',
    title: 'SQL 诊断与执行计划',
    scope: '数据库管理',
    summary: '查看会话、历史、Top/慢 SQL，执行 EXPLAIN，并对阻塞会话进行带审计的终止。',
    steps: ['从当前会话或 Top SQL 找到目标', '使用执行计划确认访问路径', '结合锁等待和事务时长判断影响', '必要时填写原因并终止查询/连接', '事后查看 Kill 审计'],
    principle: '诊断查询使用受限只读语句；Kill 操作要求结构化目标、模式、操作者和原因，并写入审计记录。',
    implementation: 'SQLDiagnosticService 统一连接实例、规范化摘要并限制返回行数；Kill 审计独立持久化。',
    caution: 'Kill connection 会回滚未提交事务，影响通常大于 Kill query。'
  },
  {
    id: 'flamegraph',
    number: '11',
    title: '火焰图与内存分析',
    scope: '性能诊断',
    summary: '对 mysqld 或指定进程进行 CPU/内存剖析，保存结果并支持周期计划。',
    steps: ['选择机器、目标进程和采样后端', '设置较短的代表性采样窗口', '等待任务生成剖析文件', '在火焰图中寻找宽栈并结合业务时间线分析'],
    principle: '采样在目标机本地执行，Manager 只负责编排、记录元数据和提供结果访问。',
    implementation: 'FlameGraphService 根据能力选择 perf 等后端，计划调度器创建标准任务并保留采样结果。',
    caution: '高频或长时间采样有额外开销；生产环境应先小范围验证。'
  },
  {
    id: 'automation',
    number: '12',
    title: '集群自动化',
    scope: '集群运维',
    summary: '跨一个或多个集群执行资产采集、巡检、脚本或受控数据库操作，并汇总报告。',
    steps: ['选择集群和操作类型', '限定目标机器或实例', '核对脚本/参数和并发范围', '提交后查看父任务', '下载汇总 CSV、HTML 或任务产物'],
    principle: '一次用户操作创建一个业务父任务，每台机器是可独立重试和审计的子任务。',
    implementation: 'TaskHandler 展开集群成员并创建子任务；结果接口只在所有任务终态后标记 ready，并按操作生成结构化行。',
    caution: '自定义脚本拥有目标机上的 Agent 权限，应保持最小影响并先在单机验证。'
  },
  {
    id: 'backup',
    number: '13',
    title: '备份策略与恢复',
    scope: '数据保护',
    summary: '创建 XtraBackup 全量/增量物理备份计划，批量触发集群备份，并执行物理恢复、时间点恢复或 Binlog 数据闪回。',
    steps: ['选择合适的副本和备份目录', '设置周期、磁盘阈值、重试与 Binlog 策略', '手动试跑一次并核对产物', '定期演练恢复和复制修复'],
    principle: '策略只保存调度与目标；每次运行都会生成独立备份记录和标准任务，恢复必须显式确认。',
    implementation: 'BackupService 聚合目标、持久化与调度策略并调用 TaskService；Agent 执行参数化 XtraBackup/bin2sql 脚本，任务事件由查询 API 合并为运行状态和日志。',
    caution: '备份成功不代表可恢复；必须验证文件完整性、权限、保留周期和恢复演练。'
  },
  {
    id: 'alerts',
    number: '14',
    title: '告警中心',
    scope: '可观测性',
    summary: '管理规则、过滤器、事件和通知渠道，并向 Prometheus/Zabbix 暴露状态。',
    steps: ['从指标目录选择可用指标', '配置阈值、持续时间与严重级别', '绑定并测试通知渠道', '对事件确认、静默或关闭', '观察投递记录和队列运行状态'],
    principle: '每次心跳触发规则计算；持续时间和状态机抑制瞬时抖动，通知通过持久化 outbox 重试投递。',
    implementation: 'AlertService 维护规则、事件、通知队列和投递记录；敏感渠道配置在读取时统一掩码。',
    caution: '静默只影响通知，不修复故障；过滤范围过宽可能隐藏真实风险。'
  },
  {
    id: 'ai-automation',
    number: '15',
    title: 'AI 运维中心',
    scope: '智能运维',
    summary: '接入大模型理解拓扑、监控与任务上下文，把一个运维目标转换为可恢复、分级审批且可审计的父子工作流。',
    steps: ['接入并测试 HTTPS 或本机模型', '选择默认模型和允许动作', '先核对模型对架构、业务入口和告警证据的理解', '审阅子操作、依赖、验证标准与回滚', '按风险审批后在聊天侧栏跟踪检查点', '异常时查看 AI 分析，修正上下文后再恢复或审批新的恢复方案'],
    principle: '模型只负责分析和提出结构化意图；Manager 负责动作白名单、依赖图校验、最高风险继承、逐步预检、任务提交、监控后验和断点恢复。任何步骤失败都会停止后续步骤。',
    implementation: 'AIService 持久化 WorkflowRun、子操作和检查点，并把每个 Agent 或平台任务挂到 ai_workflow 父任务。GET /ai/capabilities 同时公开可执行动作与完整 cluster_endpoints；VIP、备份、滚动升级和卸载均映射到真实应用服务，密钥型 API 标为 secure_input_api。Manager 重启后从最后一个确定检查点恢复；若中断发生在任务提交边界，则标记 interrupted 并禁止猜测重试。',
    caution: '模型输出不是事实证明。高风险工作流必须核对全部子操作和确认短语；暂停不会强杀已提交任务。secure_input_api 的密码不得写入聊天。监控后验未通过、上下文冲突或任务结果不明确时必须人工核对。'
  },
  {
    id: 'packages',
    number: '15',
    title: '安装包与版本升级',
    scope: '平台运维',
    summary: '集中管理 MySQL、工具、Manager 与 Agent 制品，校验完整性并执行受控升级。',
    steps: ['使用内置目录下载或手工上传制品', '核对架构、版本和 SHA256', '先升级少量 Agent 验证', 'Manager 升级前确认回退文件和维护窗口'],
    principle: '制品仓库是任务执行的唯一软件来源；升级服务比较语义版本并拒绝相同版本或降级。',
    implementation: 'PackageService 管理分类目录、元数据和校验；UpgradeService 负责备份、原子替换、重启健康检查和失败回滚。',
    caution: '不要修改已经验证的文件；变更存储路径前确保 Manager 进程具备读写权限。'
  },
  {
    id: 'tasks',
    number: '16',
    title: '任务中心',
    scope: '平台运维',
    summary: '查看所有异步操作的父子关系、步骤进度、事件日志、结果与失败原因。',
    steps: ['按状态、类型或对象筛选任务', '进入父任务查看各机器子任务', '定位当前步骤并阅读 ERROR 事件', '修复根因后从对应功能页重试'],
    principle: '所有有副作用的远端操作都落为持久化任务；状态从 pending、sent、running 到 success/failed 单向推进。',
    implementation: 'Manager 保存任务、步骤与事件，WebSocket 分发给 Agent；前端轮询详情并展示父子编排关系。',
    caution: '清理任务只删除历史记录，不撤销已经执行的远端变化；运行中的任务不能删除。'
  },
  {
    id: 'manager',
    number: '17',
    title: 'Manager 控制台',
    scope: '平台运维',
    summary: '管理 Manager 进程、账号式数据库配置、多节点高可用拓扑、VIP 漂移、内核重编译和版本升级。',
    steps: ['单节点阶段选择数据库类型并填写地址、库名、账号和密码，检测成功后保存', '切换到 MySQL 或 PostgreSQL 共享数据库后启用 VIP', '从已纳管机器安装第二个 Manager 节点', '在拓扑中确认主备在线，并在维护窗口演练 VIP 漂移、节点重启和关闭', '通过制品升级版本，或输入 REBUILD 执行本机源码重编译'],
    principle: '多节点 Manager 共享同一元数据库，VIP 始终指向唯一 ACTIVE 节点；节点心跳、角色和运维任务共同构成控制平面拓扑。',
    implementation: 'ManagerRuntimeService 负责本机进程和安全数据库切换，ManagerHAService 持久化节点与 VIP 拓扑并通过在线 Agent 安装和控制远端 systemd，UpgradeService 提供带备份和健康后检的升级与重编译。',
    caution: 'SQLite 不能用于多节点；平台已有业务数据后禁止直接换库。VIP 漂移和关闭节点前应确认目标节点在线，数据库密码、DSN 和引导令牌不会在状态接口回显。'
  },
  {
    id: 'manager-ha',
    number: '19',
    title: 'Manager 高可用',
    scope: '平台运维',
    summary: '在共享元数据库上安装多个 Manager 节点，维护主备角色并迁移控制台 VIP。',
    steps: ['先把 Manager 元数据库切换为 MySQL 或 PostgreSQL', '配置 Manager VIP、网卡、安装目录和服务名', '从已纳管机器安装备用节点', '验证节点健康后再执行 VIP 切换'],
    principle: '多个 Manager 共享控制平面状态，每个节点周期登记自身状态；活动节点和控制台 VIP 决定当前入口。',
    implementation: '当前节点生成短期引导令牌，经 Agent 安装同版本二进制和 0600 配置，再由 systemd 管理备用节点；VIP 使用 L2 地址迁移。',
    caution: 'Manager VIP 当前不支持 BGP，也没有业务 VIP 的全节点扫描和连续两轮唯一持有者证明；切换前需人工确认二层网络与旧节点可控。'
  }
]

manualModules.forEach((item, index) => {
  Object.assign(item, manualDeepDive[item.id] || {})
  item.number = String(index + 1).padStart(2, '0')
})

const commonErrors = [
  ['400', '请求格式、必填字段、状态或风险确认不满足要求', '{"error":"具体校验原因"}'],
  ['404', '对象、任务、制品或接口路径不存在', '{"error":"not found"}'],
  ['405', '该路径不支持当前 HTTP 方法', '通常无响应体'],
  ['409', '告警等资源发生并发状态冲突', '{"error":"conflict detail"}'],
  ['422', '指标存在但当前版本或采集条件不可用', '{"error":"...","metric":"..."}'],
  ['500', 'Manager 内部或持久化读取失败', '{"error":"具体错误"}']
]

const asJSON = value => {
  if (value === null) return '无响应体'
  if (typeof value === 'string') return value
  return JSON.stringify(value, null, 2)
}

const curlFor = (item, baseURL) => {
  const path = item.path.replaceAll('{machine_id}', 'machine-01').replaceAll('{credential_id}', 'cred-01').replaceAll('{cluster_name}', 'prod').replaceAll('{task_id}', 'task-01HX').replaceAll('{profile_id}', 'profile-01').replaceAll('{schedule_id}', 'schedule-01').replaceAll('{policy_id}', 'policy-01').replaceAll('{run_id}', 'run-01').replaceAll('{rule_id}', 'rule-01').replaceAll('{filter_id}', 'filter-01').replaceAll('{channel_id}', 'channel-01').replaceAll('{job_id}', 'upgrade-01').replaceAll('{category}', 'mysql').replaceAll('{file_name}', 'package.tar.xz')
  const parts = [`curl -i -X ${item.method} '${baseURL}${path}'`]
  if (item.contentType === 'application/json' && item.body !== undefined) {
    parts.push(`  -H 'Content-Type: application/json'`)
    parts.push(`  --data '${JSON.stringify(item.body)}'`)
  } else if (item.contentType === 'multipart/form-data') {
    parts.push(`  -F 'file=@/path/to/package.tar.xz'`)
    Object.entries(item.body || {}).filter(([key]) => key !== 'file').forEach(([key, value]) => parts.push(`  -F '${key}=${value}'`))
  } else if (item.contentType === 'application/x-www-form-urlencoded') {
    parts.push(`  -H 'Content-Type: application/x-www-form-urlencoded'`)
    parts.push(`  --data '${new URLSearchParams(item.body || {}).toString()}'`)
  }
  return parts.join(' \\\n')
}

const jsFor = item => {
  const path = item.path.replaceAll('{machine_id}', 'machine-01').replaceAll('{credential_id}', 'cred-01').replaceAll('{cluster_name}', 'prod').replaceAll('{task_id}', 'task-01HX').replaceAll('{profile_id}', 'profile-01').replaceAll('{schedule_id}', 'schedule-01').replaceAll('{policy_id}', 'policy-01').replaceAll('{run_id}', 'run-01').replaceAll('{rule_id}', 'rule-01').replaceAll('{filter_id}', 'filter-01').replaceAll('{channel_id}', 'channel-01').replaceAll('{job_id}', 'upgrade-01').replaceAll('{category}', 'mysql').replaceAll('{file_name}', 'package.tar.xz')
  const options = [`method: '${item.method}'`]
  if (item.body !== undefined && item.contentType === 'application/json') {
    options.push(`headers: { 'Content-Type': 'application/json' }`)
    options.push(`body: JSON.stringify(${JSON.stringify(item.body, null, 2)})`)
  } else if (item.body !== undefined && item.contentType === 'application/x-www-form-urlencoded') {
    options.push(`headers: { 'Content-Type': 'application/x-www-form-urlencoded' }`)
    options.push(`body: new URLSearchParams(${JSON.stringify(item.body, null, 2)})`)
  }
  const responseReader = item.contentType.startsWith('application/json')
    ? 'await response.json()'
    : item.contentType.startsWith('text/') || item.contentType === 'application/x-www-form-urlencoded'
      ? 'await response.text()'
      : 'await response.blob()'
  return `const response = await fetch('/api/v1${path}', {\n  ${options.join(',\n  ')}\n})\nconst result = ${responseReader}\nif (!response.ok) throw new Error(result.error || \`HTTP \${response.status}\`)`
}

export const UserManual = {
  name: 'UserManual',
  emits: ['navigate'],
  setup(_, { emit }) {
    const keyword = ref('')
    const expanded = ref('overview')
    const filteredModules = computed(() => {
      const needle = keyword.value.trim().toLowerCase()
      if (!needle) return manualModules
      return manualModules.filter(item => [
        item.title,
        item.scope,
        item.summary,
        item.principle,
        item.implementation,
        item.steps.join(' '),
        item.flow.join(' '),
        item.mechanisms.map(section => `${section.title} ${section.detail}`).join(' '),
        item.invariants.join(' '),
        item.components.join(' '),
        item.limitations.join(' ')
      ].join(' ').toLowerCase().includes(needle))
    })
    const toggle = id => { expanded.value = expanded.value === id ? '' : id }
    return { keyword, expanded, filteredModules, toggle, emit }
  },
  template: `
    <div class="docs-page manual-page">
      <section class="docs-hero">
        <div><span>GMHA USER GUIDE · CONTROL PLANE</span><h2>从第一次纳管，到稳定运行</h2><p>面向 DBA 与平台运维的完整使用手册。每个模块都说明操作路径、系统原理、实现边界和生产注意事项。</p>
          <div class="docs-hero-actions"><button type="button" class="primary" @click="emit('navigate','machines')">开始纳管机器</button><button type="button" class="secondary" @click="emit('navigate','api-docs')">查看 API 文档</button></div>
        </div>
        <dl><div><dt>{{ filteredModules.length }}</dt><dd>功能主题</dd></div><div><dt>5</dt><dd>核心层次</dd></div><div><dt>100%</dt><dd>任务可追踪</dd></div></dl>
      </section>

      <section class="docs-start">
        <header><span>QUICK START</span><h3>推荐落地顺序</h3><p>按依赖关系完成基础配置，后续集群与数据库操作才有可靠的执行通道。</p></header>
        <ol>
          <li><i>1</i><div><b>配置 Manager</b><span>确认 HTTP、gRPC、数据库和 Agent 制品路径</span></div></li>
          <li><i>2</i><div><b>准备 SSH 凭证</b><span>建议使用专用账号与最小权限私钥</span></div></li>
          <li><i>3</i><div><b>纳管机器</b><span>完成预检、Agent 安装和静态资产采集</span></div></li>
          <li><i>4</i><div><b>建立集群</b><span>分配成员、登记实例并核对拓扑</span></div></li>
          <li><i>5</i><div><b>开启保护</b><span>配置监控、告警、备份并演练恢复</span></div></li>
        </ol>
      </section>

      <section class="docs-architecture">
        <header><div><span>ARCHITECTURE</span><h3>控制平面如何工作</h3></div><p>浏览器从不直接连接生产主机或 MySQL。Manager 验证意图、编排任务，Agent 在目标机执行并持续回报。</p></header>
        <div class="architecture-flow" role="img" aria-label="GMHA 分层架构：控制台调用 Manager API，Manager 经过应用服务、领域编排和数据存储，通过任务通道、心跳与 SSH 管理 Agent，Agent 操作 Linux 和 MySQL">
          <article class="layer console"><em>01 · EXPERIENCE</em><b>Web 控制台 / API 客户端</b><small>意图输入 · 状态查询 · 报告下载</small></article>
          <i>HTTP / JSON</i>
          <article class="layer manager"><em>02 · CONTROL PLANE</em><b>Manager API 与应用服务</b><small>校验 · 权限边界 · 聚合读模型</small></article>
          <i>领域命令</i>
          <article class="layer domain"><em>03 · ORCHESTRATION</em><b>任务 / HA / 告警 / 调度</b><small>计划 · 父子任务 · 风险控制 · 审计</small></article>
          <div class="architecture-split"><span>持久化状态</span><span>WebSocket / 心跳 / SSH 引导</span></div>
          <div class="architecture-pair"><article class="layer store"><em>04A · STATE</em><b>Manager 数据库与制品库</b><small>台账 · 步骤 · 事件 · 样本 · 产物</small></article><article class="layer agent"><em>04B · DATA PLANE</em><b>GMHA Agent</b><small>内置采集器 · 受控处理器 · 结果上报</small></article></div>
          <i>系统命令 / 本地连接</i>
          <article class="layer target"><em>05 · TARGETS</em><b>Linux · MySQL · VIP · 备份存储</b><small>实际运行状态最终由心跳与任务结果收敛</small></article>
        </div>
        <div class="architecture-notes"><article><b>读路径</b><span>Agent 心跳 → 样本/状态 → 聚合接口 → 控制台</span></article><article><b>写路径</b><span>API 请求 → 校验/计划 → 持久化任务 → Agent 执行 → 结果审计</span></article><article><b>故障路径</b><span>超时/失败 → 步骤事件 → 告警或人工修复 → 幂等重试</span></article></div>
      </section>

      <section class="manual-directory">
        <header class="docs-section-head"><div><span>MODULE DIRECTORY</span><h3>全部功能模块</h3><p>输入模块、操作或原理关键词快速定位。</p></div><label><i>⌕</i><input v-model="keyword" placeholder="搜索：架构、备份、在线 DDL…"></label></header>
        <div class="manual-list">
          <article v-for="item in filteredModules" :key="item.id" :class="{open:expanded===item.id}">
            <button type="button" @click="toggle(item.id)">
              <span class="manual-number">{{ item.number }}</span><span class="manual-title"><em>{{ item.scope }}</em><b>{{ item.title }}</b><small>{{ item.summary }}</small></span><i>{{ expanded===item.id ? '−' : '+' }}</i>
            </button>
            <div v-if="expanded===item.id" class="manual-detail">
              <div class="manual-overview">
                <section><span>PRINCIPLE</span><h4>核心原理</h4><p>{{ item.principle }}</p></section>
                <section><span>IMPLEMENTATION</span><h4>当前实现</h4><p>{{ item.implementation }}</p></section>
                <aside><b>生产注意</b><p>{{ item.caution }}</p><button v-if="['overview','machines','agents','clusters','automation','mysql','alerts','ai-automation','packages','tasks','manager','manager-ha'].includes(item.id)" type="button" @click="emit('navigate',item.id==='manager-ha' ? 'manager' : item.id)">进入功能模块 →</button></aside>
              </div>

              <section class="manual-flow">
                <header><span>EXECUTION FLOW</span><h4>实现链路 / 状态机</h4></header>
                <ol><li v-for="(stage,index) in item.flow" :key="stage"><i>{{ index+1 }}</i><span>{{ stage }}</span></li></ol>
              </section>

              <section class="manual-usage">
                <header><span>OPERATIONS</span><h4>怎么使用</h4></header>
                <ol><li v-for="(step,index) in item.steps" :key="step"><i>{{ index+1 }}</i><span>{{ step }}</span></li></ol>
              </section>

              <section v-if="item.id==='cluster-architecture'" class="vip-design" aria-label="业务 VIP 的 L2 ARP、BGP 宣告与防脑裂设计">
                <header><span>VIP CONTROL DESIGN</span><h4>业务 VIP：两种宣告路径，一套防脑裂证明</h4><p>路由方式不同，但切换安全屏障相同；“命令退出成功”不能替代集群级持有者证明。</p></header>
                <div class="vip-lanes">
                  <article><em>L2 · ARP</em><div><b>VIP / prefix</b><i>→</i><b>目标物理网卡</b><i>→</i><b>arping -U</b><i>→</i><b>同网段 ARP 更新</b></div><small>验证：目标机地址存在；网卡来自显式配置或安全匹配。</small></article>
                  <article><em>L3 · BGP</em><div><b>VIP /32 on loopback</b><i>→</i><b>FRR / vtysh</b><i>→</i><b>Peer Established</b><i>→</i><b>advertised-routes</b></div><small>验证：邻居已建立且指定 peer 确实收到该 /32 前缀。</small></article>
                  <article class="proof"><em>SHARED SAFETY BARRIER</em><div><b>全节点撤销</b><i>→</i><b>0 持有者</b><i>→</i><b>绑定目标</b><i>→</i><b>唯一持有者 × 2</b></div><small>任何扫描失败、MISMATCH 或 CONFLICT：撤销新目标并记录 FAILED。</small></article>
                </div>
                <div class="vip-states"><span>UNBOUND · 0</span><span>BOUND · 1 个期望节点</span><span>MISMATCH · 1 个错误节点</span><span>CONFLICT · 多于 1 个节点</span><span>FAILED · 校验或补偿失败</span></div>
              </section>

              <section class="manual-mechanisms">
                <header><span>UNDER THE HOOD</span><h4>底层技术拆解</h4><p>以下内容对应当前代码中的协议、算法、状态判断和执行顺序。</p></header>
                <div><article v-for="(section,index) in item.mechanisms" :key="section.title"><i>{{ String(index+1).padStart(2,'0') }}</i><div><b>{{ section.title }}</b><p>{{ section.detail }}</p></div></article></div>
              </section>

              <div class="manual-safety">
                <section><span>SAFETY INVARIANTS</span><h4>必须成立的安全不变量</h4><ul><li v-for="rule in item.invariants" :key="rule">{{ rule }}</li></ul></section>
                <section><span>COMPONENTS</span><h4>关键组件与依赖</h4><div class="manual-chips"><code v-for="component in item.components" :key="component">{{ component }}</code></div></section>
                <section><span>BOUNDARIES</span><h4>当前边界与限制</h4><ul><li v-for="boundary in item.limitations" :key="boundary">{{ boundary }}</li></ul></section>
              </div>
            </div>
          </article>
          <div v-if="!filteredModules.length" class="docs-empty">没有匹配的功能模块，请尝试更短的关键词。</div>
        </div>
      </section>
    </div>`
}

export const APIDocumentation = {
  name: 'APIDocumentation',
  setup() {
    const keyword = ref('')
    const category = ref('全部')
    const method = ref('全部')
    const expanded = ref('')
    const exampleLanguage = ref('curl')
    const copied = ref('')
    const baseURL = typeof window === 'undefined' ? 'http://127.0.0.1:8080/api/v1' : `${window.location.origin}/api/v1`
    const categories = ['全部', ...new Set(apiEndpoints.map(item => item.category))]
    const methods = ['全部', 'GET', 'POST', 'PUT', 'DELETE']
    const filteredEndpoints = computed(() => {
      const needle = keyword.value.trim().toLowerCase()
      return apiEndpoints.filter(item => {
        if (category.value !== '全部' && item.category !== category.value) return false
        if (method.value !== '全部' && item.method !== method.value) return false
        return !needle || [item.path, item.title, item.category, item.note].join(' ').toLowerCase().includes(needle)
      })
    })
    const toggle = item => {
      const key = `${item.method}:${item.path}`
      expanded.value = expanded.value === key ? '' : key
    }
    const keyFor = item => `${item.method}:${item.path}`
    const requestExample = item => exampleLanguage.value === 'curl' ? curlFor(item, baseURL) : jsFor(item)
    const copy = async (value, key) => {
      try {
        await navigator.clipboard.writeText(value)
        copied.value = key
        setTimeout(() => { if (copied.value === key) copied.value = '' }, 1600)
      } catch (_) {
        copied.value = ''
      }
    }
    return { keyword, category, method, expanded, exampleLanguage, copied, baseURL, categories, methods, filteredEndpoints, instanceManagementOperations, commonErrors, asJSON, toggle, keyFor, requestExample, copy, total: apiEndpoints.length }
  },
  template: `
    <div class="docs-page api-page">
      <section class="api-hero">
        <div><span>GMHA REST API · v1</span><h2>可调用、可验证、可追踪</h2><p>覆盖当前 Manager 注册的全部 HTTP 能力。路径、方法、请求、状态码与示例返回均按现有实现整理。</p></div>
        <dl><div><dt>{{ total }}</dt><dd>调用定义</dd></div><div><dt>{{ categories.length-1 }}</dt><dd>接口分组</dd></div><div><dt>JSON</dt><dd>默认格式</dd></div></dl>
      </section>

      <section class="api-contract">
        <article><span>BASE URL</span><div><code>{{ baseURL }}</code><button type="button" @click="copy(baseURL,'base')">{{ copied==='base' ? '已复制' : '复制' }}</button></div><p>示例默认与控制台同源。反向代理部署时，以实际 HTTPS 域名替换主机部分。</p></article>
        <article><span>AUTHENTICATION</span><b>应用路由当前未内置登录鉴权</b><p>生产环境必须通过反向代理、VPN/内网、mTLS 或统一身份网关保护；Agent 注册与心跳接口应限制来源。</p></article>
        <article><span>ASYNC OPERATIONS</span><b>远端变更返回 task_id / run_id</b><p>收到 200/201/202 表示已接受或创建，不一定代表远端执行完成；继续查询任务或运行状态直到终态。</p></article>
      </section>

      <section class="api-call-flow">
        <div><span>1</span><b>提交请求</b><small>Content-Type: application/json</small></div><i>→</i><div><span>2</span><b>检查 HTTP 状态</b><small>非 2xx 读取 error 字段</small></div><i>→</i><div><span>3</span><b>保存任务标识</b><small>task_id / run_id</small></div><i>→</i><div><span>4</span><b>轮询到终态</b><small>success / failed</small></div>
      </section>

      <section class="instance-api-coverage">
        <header><span>INSTANCE WORKSPACE COVERAGE</span><h3>实例管理 14 项操作均可通过 API 完成</h3><p>任务型接口返回任务标识后，继续读取任务详情或专用结果接口，不能把“请求已接受”当作远端执行成功。</p></header>
        <div>
          <article v-for="item in instanceManagementOperations" :key="item.name">
            <span>✓</span><section><b>{{ item.name }}</b><small>{{ item.mode }}</small><code v-for="api in item.apis" :key="api">{{ api }}</code></section>
          </article>
        </div>
      </section>

      <section class="api-reference">
        <header class="docs-section-head"><div><span>ENDPOINT REFERENCE</span><h3>接口目录</h3><p>示例中的 ID、IP、密码、集群名和文件名都需要替换为实际值。</p></div><label><i>⌕</i><input v-model="keyword" placeholder="搜索路径或功能…"></label></header>
        <div class="api-filters">
          <select v-model="category" aria-label="接口分类"><option v-for="item in categories" :key="item">{{ item }}</option></select>
          <div><button v-for="item in methods" :key="item" type="button" :class="{active:method===item}" @click="method=item">{{ item }}</button></div>
          <span>显示 {{ filteredEndpoints.length }} / {{ total }}</span>
        </div>
        <div class="endpoint-list">
          <article v-for="item in filteredEndpoints" :key="keyFor(item)" :class="{open:expanded===keyFor(item)}">
            <button type="button" class="endpoint-summary" @click="toggle(item)">
              <span :class="['method',item.method.toLowerCase()]">{{ item.method }}</span><code>{{ item.path }}</code><b>{{ item.title }}</b><em>{{ item.category }}</em><i>{{ expanded===keyFor(item) ? '−' : '+' }}</i>
            </button>
            <div v-if="expanded===keyFor(item)" class="endpoint-detail">
              <p v-if="item.note" class="endpoint-note">{{ item.note }}</p>
              <div v-if="item.query.length" class="endpoint-params"><b>查询参数</b><span v-for="param in item.query" :key="param"><code>{{ param }}</code></span></div>
              <div class="endpoint-status"><span>成功状态 <b>{{ item.status }}</b></span><span>响应类型 <b>{{ item.contentType.startsWith('multipart') ? 'application/json' : item.contentType }}</b></span></div>
              <section class="endpoint-code">
                <header><b>调用示例</b><div><button type="button" :class="{active:exampleLanguage==='curl'}" @click="exampleLanguage='curl'">cURL</button><button type="button" :class="{active:exampleLanguage==='js'}" @click="exampleLanguage='js'">JavaScript</button><button type="button" @click="copy(requestExample(item),'request:'+keyFor(item))">{{ copied==='request:'+keyFor(item) ? '已复制' : '复制' }}</button></div></header>
                <pre><code>{{ requestExample(item) }}</code></pre>
              </section>
              <section class="endpoint-code response">
                <header><b>示例返回</b><div><button type="button" @click="copy(asJSON(item.response),'response:'+keyFor(item))">{{ copied==='response:'+keyFor(item) ? '已复制' : '复制' }}</button></div></header>
                <pre><code>{{ asJSON(item.response) }}</code></pre>
              </section>
            </div>
          </article>
          <div v-if="!filteredEndpoints.length" class="docs-empty">没有匹配的接口，请清除部分筛选条件。</div>
        </div>
      </section>

      <section class="api-errors">
        <header><span>ERROR CONTRACT</span><h3>错误处理约定</h3><p>业务错误默认返回 JSON，调用方必须先判断 HTTP 状态，再读取响应体。</p></header>
        <div><article v-for="item in commonErrors" :key="item[0]"><b>{{ item[0] }}</b><span>{{ item[1] }}</span><code>{{ item[2] }}</code></article></div>
        <pre><code>const response = await fetch(url, options)
const contentType = response.headers.get('content-type') || ''
const payload = contentType.includes('json') ? await response.json() : await response.text()
if (!response.ok) throw new Error(payload.error || \`HTTP \${response.status}\`)</code></pre>
      </section>
    </div>`
}
