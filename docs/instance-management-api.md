# 实例管理 API 手册

本文档对应控制台“集群管理 → 实例管理”的 14 个操作入口，说明如何只通过 HTTP API 获取目标信息、提交操作并读取最终结果。

## 1. 通用约定

- Base URL：`http://<manager-host>:8080/api/v1`
- JSON 请求：`Content-Type: application/json`
- 成功响应：HTTP `200`、`201` 或 `202`
- 失败响应：通常为 `{"error":"具体原因"}`
- `machine_id` 指 Manager 中的机器 ID；部分任务接口的 `machine` 同时接受机器 ID 或管理 IP。
- 数据库密码不由以下接口接收。Manager/Agent 使用已登记实例的 MHA 管理账号，并在 Agent 端通过权限为 `0600` 的临时 defaults 文件注入凭据。
- 安装接口中的初始化 root 密码和账号密码属于例外，只用于创建目标实例；调用方必须通过 HTTPS、访问控制和秘密管理系统保护请求。

远端操作大多是异步任务。创建接口返回任务详情后，应取得 `task.id`（兼容响应中也可能是 `Task.ID`），轮询：

```http
GET /api/v1/tasks?id=<task_id>
```

只有任务状态进入 `success` 才表示远端操作成功。`pending`、`sent`、`running` 以及创建请求的 `200/202` 都不能视为最终成功。

## 2. 14 项能力覆盖

| 操作 | 获取/处理能力 | 执行 API | 结果 API |
|---|---|---|---|
| 实例 | 列表、状态、详情、生命周期 | `GET/DELETE /mysql/instances`、`POST /tasks/mysql-lifecycle`、`POST /tasks/mysql-uninstall` | `GET /tasks?id=...` |
| 数据库巡检 | 标准/深度巡检、汇总、Word、Excel | `POST /tasks/cluster-automation` | `/tasks/database-inspection/*` |
| 执行计划 | 对单条只读或可解释 SQL 执行 `EXPLAIN` | `POST /sql-diagnostics/explain` | 同步返回 |
| 在线 DDL | dry-run、负载门禁、执行、后验验证 | `POST /tasks/mysql-online-ddl` | `GET /tasks?id=...` |
| 索引管理 | 列表、创建、重命名、删除 | `POST /tasks/mysql-indexes` | `GET /tasks?id=...` |
| 直方图 | 元数据、创建/更新、删除 | `GET/POST/DELETE /mysql/histograms` | 同步返回 |
| 数据归档 | dry-run、复制或搬迁、限速、后验统计 | `POST /tasks/mysql-archive` | `GET /tasks?id=...` |
| binlog 分析 | 任务列表、分析、进度、结果、取消 | `/mysql/binlog-analysis` | 专用任务详情 |
| 创建安装 | 制品查询、单机安装、集群引导 | `GET /mysql/packages`、`POST /tasks/mysql-install`、`POST /clusters/{cluster}/bootstrap` | `GET /tasks?id=...` |
| 用户管理 | 列表、授权、密码、锁定、删除 | `POST /tasks/mysql-users` | `GET /tasks?id=...` |
| 预设账号 | 查询、保存安装账号模板 | `GET/PUT /mysql/account-presets` | 同步返回 |
| 参数管理 | 采集、动态修改、配置修改、显式重启 | `POST /tasks/mysql-parameters` | `GET /tasks?id=...` |
| Agent 采集 | 查询、更新 MySQL 动态采集配置 | `GET/PUT /mysql-dynamic-collect/config` | 同步返回 |
| 版本升级 | 制品查询、预检、单机升级、滚动升级 | `/tasks/mysql-upgrade*`、`/tasks/mysql-cluster-upgrade*` | 任务或滚动运行详情 |

## 3. 实例

查询实例：

```http
GET /api/v1/mysql/instances
```

安全重启：

```json
POST /api/v1/tasks/mysql-lifecycle
{
  "machine": "10.0.0.11",
  "port": 3306,
  "action": "restart",
  "confirmation": "RESTART 10.0.0.11:3306",
  "risk_acknowledged": true,
  "primary_acknowledged": false,
  "deep_data_check": true
}
```

`action` 为 `restart` 或 `shutdown`，确认短语必须为 `ACTION IP:PORT`。遗忘实例登记使用 `DELETE /mysql/instances`，不会卸载远端 MySQL；真正卸载使用 `POST /tasks/mysql-uninstall`。

## 4. 数据库巡检

创建单实例标准巡检：

```json
POST /api/v1/tasks/cluster-automation
{
  "clusters": ["prod"],
  "target_machine_id": "machine-01",
  "operation": "database_inspection",
  "port": 3306
}
```

深度巡检把 `operation` 改为 `database_deep_inspection`。从创建响应的 `items[].task_id` 收集任务 ID，然后调用：

```http
GET /api/v1/tasks/database-inspection/results?task_ids=task-01,task-02
GET /api/v1/tasks/database-inspection/report?task_ids=task-01,task-02
GET /api/v1/tasks/database-inspection/data?task_ids=task-01,task-02
```

`report` 返回 DOCX，`data` 返回 XLSX。任务未完成时下载接口返回 `409`。

## 5. 执行计划

```json
POST /api/v1/sql-diagnostics/explain
{
  "machine_id": "machine-01",
  "port": 3306,
  "database": "app",
  "sql": "SELECT * FROM orders WHERE user_id = 42"
}
```

响应包含 `instance`、`database`、`sql`、`columns`、`rows` 和 `generated_at`。接口拒绝多语句、注释、写语句、调用方自行提供的 `EXPLAIN` 以及 `EXPLAIN ANALYZE`。

## 6. 在线 DDL

先把 `action` 设为 `dry_run`。确认预演成功后，使用相同安全参数提交：

```json
POST /api/v1/tasks/mysql-online-ddl
{
  "machine": "machine-01",
  "port": 3306,
  "action": "execute",
  "schema": "app",
  "table": "orders",
  "alter": "ADD COLUMN source varchar(32) NULL",
  "purpose": "记录订单来源",
  "impact": "新增可空字段，无数据回填",
  "max_load_threads_running": 25,
  "critical_threads_running": 50,
  "max_lag_seconds": 10,
  "chunk_time_seconds": 0.5,
  "check_interval_seconds": 1,
  "alter_foreign_keys_method": "auto",
  "risk_acknowledged": true,
  "confirmation": "app.orders"
}
```

`alter` 只接受 `ALTER TABLE` 后面的子句。接口始终通过 `pt-online-schema-change` 执行，不会静默降级为阻塞式 COPY。

## 7. 索引管理

读取索引使用 `action: "list"`。创建示例：

```json
POST /api/v1/tasks/mysql-indexes
{
  "machine": "machine-01",
  "port": 3306,
  "action": "create",
  "schema": "app",
  "table": "orders",
  "name": "idx_user_id",
  "kind": "btree",
  "columns": [{"name": "user_id", "direction": "ASC"}],
  "purpose": "加速用户订单查询",
  "impact": "降低扫描行数",
  "lock_mode": "none",
  "lock_acknowledged": true,
  "online_with_pt": false
}
```

`action` 支持 `list`、`create`、`rename`、`delete`；`kind` 支持 `btree`、`unique`、`fulltext`、`spatial`。删除必须提交精确的 `schema.table.index` 确认字符串，主键删除不受支持。

## 8. 直方图

查询元数据和已有直方图：

```http
GET /api/v1/mysql/histograms?machine_id=machine-01&port=3306&schema=app&table=orders
```

创建或更新：

```json
POST /api/v1/mysql/histograms
{
  "machine_id": "machine-01",
  "port": 3306,
  "schema": "app",
  "table": "orders",
  "columns": ["status"],
  "buckets": 16
}
```

删除使用相同请求体调用 `DELETE /mysql/histograms`，无需 `buckets`。仅支持 MySQL 8.0+，桶数为 1–1024；操作使用 `NO_WRITE_TO_BINLOG`，不会复制到其他实例。

## 9. 数据归档

```json
POST /api/v1/tasks/mysql-archive
{
  "machine": "machine-01",
  "port": 3306,
  "action": "execute",
  "source_schema": "app",
  "source_table": "orders",
  "destination_schema": "archive",
  "destination_table": "orders_2025",
  "where": "created_at < '2026-01-01'",
  "batch_size": 1000,
  "sleep_seconds": 1,
  "run_time_seconds": 3600,
  "delete_source": false,
  "risk_acknowledged": true,
  "confirmation": "app.orders->archive.orders_2025"
}
```

先使用 `dry_run`。`delete_source: false` 为只复制；`true` 才删除源行。WHERE 条件拒绝 `1=1`、注释、多语句和修改类 SQL。

## 10. binlog 分析

创建任务：

```json
POST /api/v1/mysql/binlog-analysis
{
  "machine_id": "machine-01",
  "port": 3306,
  "start_time": "2026-07-23T09:00",
  "end_time": "2026-07-23T10:00",
  "start_file": "",
  "big_txn_mode": "rows",
  "big_txn_rows_threshold": 1000,
  "big_txn_bytes_threshold": 0
}
```

读取与取消：

```http
GET /api/v1/mysql/binlog-analysis
GET /api/v1/mysql/binlog-analysis/<task_id>
DELETE /api/v1/mysql/binlog-analysis/<task_id>
```

单次时间范围最长 7 天。列表只返回摘要；完整聚合、DDL、大事务和明细在专用任务详情的 `result` 中。

## 11. 创建安装

先读取兼容制品：

```http
GET /api/v1/mysql/packages
```

再提交单机安装：

```json
POST /api/v1/tasks/mysql-install
{
  "machine": "machine-01",
  "port": 3306,
  "server_id": 101,
  "mysql_user": "mysql",
  "root_password": "请替换",
  "profile": "prod",
  "package_name": "mysql-8.0.46-linux-glibc2.17-x86_64.tar.xz",
  "install_pt_tools": true,
  "install_xtrabackup": true,
  "runtime_parameters": {},
  "accounts": []
}
```

集群安装并建立初始架构使用 `POST /clusters/{cluster_name}/bootstrap`。端口、`server_id`、制品架构、glibc、目录和配置均由内核再次校验。

## 12. 用户管理

统一入口：

```json
POST /api/v1/tasks/mysql-users
{
  "machine": "machine-01",
  "port": 3306,
  "action": "create",
  "target_username": "app_user",
  "target_password": "请替换",
  "target_host": "10.%",
  "privileges": ["SELECT", "INSERT"]
}
```

`action` 支持：

- `list`：列出账号、锁定状态与全局权限；
- `query`：读取指定账号的 GRANT；
- `create`、`update`、`delete`；
- `grant`、`revoke`；
- `lock`、`unlock`。

查询类操作仍是异步任务，数据位于任务详情的 `events` 中。MySQL 5.7 会拒绝不支持的动态权限。

## 13. 预设账号

```http
GET /api/v1/mysql/account-presets
PUT /api/v1/mysql/account-presets
```

PUT 请求体是账号数组：

```json
[
  {
    "role": "monitor",
    "username": "gmha_monitor",
    "password": "请替换",
    "host": "%",
    "enabled": true,
    "privileges": ["PROCESS", "REPLICATION CLIENT"]
  }
]
```

预设用于后续安装，不会修改已经运行的实例。

## 14. 参数管理

采集参数：

```json
POST /api/v1/tasks/mysql-parameters
{"machine":"machine-01","port":3306,"action":"collect"}
```

修改动态参数：

```json
POST /api/v1/tasks/mysql-parameters
{
  "targets": [
    {"machine":"machine-01","port":3306,"config_path":"/etc/my.cnf","systemd_unit":"mysqld"}
  ],
  "restart_targets": [],
  "restart_confirmed": false,
  "changes": [
    {"action":"update","name":"max_connections","value":"1000"}
  ]
}
```

变更 action 只能是 `update` 或 `delete`。内核会区分动态参数和需要重启的参数；后者必须显式提供 `restart_targets` 并设置 `restart_confirmed: true`。

## 15. Agent 采集

```http
GET /api/v1/mysql-dynamic-collect/config
PUT /api/v1/mysql-dynamic-collect/config
```

```json
{
  "enabled": true,
  "interval_seconds": 5,
  "tasks": [
    {"name":"performance","type":"builtin","enabled":true}
  ]
}
```

保存后 Manager 通过心跳响应向 Agent 下发配置，Agent 无需重启。实际字段以 GET 返回的当前配置为模板修改，避免覆盖不认识的采集器。

## 16. 版本升级

单机升级必须先预检：

```json
POST /api/v1/tasks/mysql-upgrade/precheck
{"machine":"machine-01","port":3306,"package_name":"mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz"}
```

等待预检任务成功，再提交：

```json
POST /api/v1/tasks/mysql-upgrade
{
  "machine":"machine-01",
  "port":3306,
  "package_name":"mysql-8.4.6-linux-glibc2.17-x86_64.tar.xz",
  "precheck_task_id":"task-01HX...",
  "force":false,
  "risk_acknowledged":true
}
```

集群滚动升级：

```http
POST /api/v1/tasks/mysql-cluster-upgrade/plan
POST /api/v1/tasks/mysql-cluster-upgrade/start
GET  /api/v1/tasks/mysql-cluster-upgrade?run_id=<run_id>
```

plan 请求包含 `cluster`、`target_version`、`port`；start 还必须包含 `risk_acknowledged: true`。滚动升级按拓扑先处理副本，并在必要时通过受控架构切换避开当前主库。

## 17. 安全与幂等

- 对 DDL、归档、生命周期和升级保留调用方审批记录，并使用内核要求的精确确认字符串。
- 客户端超时后先查询任务列表或专用运行接口，不要立即重复提交高风险操作。
- 任务命令、步骤与事件用于审计；涉及用户密码的任务在完成后会清理命令中的敏感内容。
- 当前应用路由未内置登录鉴权。生产部署必须置于 HTTPS 反向代理、VPN/内网、mTLS 或统一身份网关后，并限制 Agent 注册、心跳和任务通道来源。
