# 备份恢复 API 手册

本文描述 GMHA Manager 内核提供的备份恢复接口。所有路径均以 `/api/v1` 为前缀，请求和响应默认使用 `application/json`。

## 能力与接口对照

| 操作 | 方法与路径 | 结果 |
| --- | --- | --- |
| 获取可选机器和 MySQL 实例 | `GET /backup/targets` | 返回聚合后的备份/恢复目标与就绪状态 |
| 查询策略列表 | `GET /backup/policies` | 返回策略数组 |
| 查询单个策略 | `GET /backup/policies/{policy_id}` | 返回完整策略，但不回显密码 |
| 创建策略 | `POST /backup/policies` | 保存并返回新策略 |
| 更新策略 | `PUT /backup/policies/{policy_id}` | 更新并返回策略 |
| 删除策略 | `DELETE /backup/policies/{policy_id}` | 删除调度策略，不删除已有备份记录或远端文件 |
| 立即执行一个策略 | `POST /backup/policies/{policy_id}/run` | 创建备份运行记录和 Agent 任务 |
| 批量执行集群策略 | `POST /backup/cluster-runs` | 触发所选集群内全部已启用策略 |
| 查询备份记录 | `GET /backup/runs` | 返回备份运行、动态任务状态和日志 |
| 查询单条备份记录 | `GET /backup/runs/{run_id}` | 返回目标、路径、任务、状态和日志 |
| 执行恢复或闪回 | `POST /backup/runs/{run_id}/restore` | 创建物理恢复、时间点恢复或闪回任务 |

异步操作返回 `task_id`，可继续调用 `GET /api/v1/tasks?id={task_id}` 查询任务步骤、事件与最终状态。

## 1. 查询目标

```http
GET /api/v1/backup/targets?cluster=prod
```

`cluster` 可省略；省略时返回所有已分配集群且已登记 MySQL 实例的目标。

```json
[
  {
    "cluster": "prod",
    "machine_id": "machine-02",
    "machine_name": "db-replica-01",
    "machine_ip": "10.0.0.12",
    "agent_status": "agent_online",
    "port": 3306,
    "instance_status": "running",
    "mysql_version": "8.4.10",
    "architecture": "x86_64",
    "package_name": "mysql-8.4.10-linux-glibc2.28-x86_64",
    "backup_ready": true,
    "restore_ready": true
  }
]
```

`blocking_reasons` 说明登记状态层面的阻塞原因，例如 Agent 离线或实例异常。`backup_ready` 不替代执行期检查：Agent 仍会检查 XtraBackup 与 MySQL 版本系列、备份目录磁盘使用率、复制延迟、实例锁和文件完整性。

此接口不会返回 SSH/MySQL 凭据、Socket、配置文件或数据目录。

## 2. 策略 API

### 查询列表

```http
GET /api/v1/backup/policies?cluster=prod
```

`cluster` 可省略。结果按创建时间倒序返回，`mysql_password` 永不出现在响应中。

### 查询单个策略

```http
GET /api/v1/backup/policies/policy-01
```

策略不存在时返回 `404`。

### 创建策略

```http
POST /api/v1/backup/policies
Content-Type: application/json
```

```json
{
  "name": "prod-daily",
  "cluster": "prod",
  "machine_id": "machine-02",
  "port": 3306,
  "backup_type": "full",
  "disk_usage_threshold": 95,
  "schedule_type": "weekly",
  "weekdays": [0, 1, 2, 3, 4, 5, 6],
  "weekday_backup_types": {
    "0": "full",
    "1": "incremental",
    "2": "incremental",
    "3": "incremental",
    "4": "incremental",
    "5": "full",
    "6": "full"
  },
  "interval_minutes": 1440,
  "start_at": "2026-07-24T02:00:00+08:00",
  "retry_count": 2,
  "retry_interval_seconds": 60,
  "include_binlog": true,
  "backup_location": "/backup/mysql",
  "mysql_user": "backup",
  "mysql_password": "secret",
  "enabled": true
}
```

成功返回 `201` 和已保存策略。

### 更新策略

```http
PUT /api/v1/backup/policies/policy-01
Content-Type: application/json
```

请求体字段与创建相同。`mysql_password` 传空字符串时保留已有密码。请求体可以不传 `id`；如传入，必须与路径中的 `policy_id` 一致。策略不存在返回 `404`。

为兼容早期调用方，`POST /backup/policies` 仍接受带 `id` 的完整策略并执行更新；新调用方应使用 `PUT`。

### 字段约束

| 字段 | 约束 |
| --- | --- |
| `name`、`cluster` | 必填 |
| `machine_id` | 应来自目标查询；为空时内核尝试选择集群内已登记实例 |
| `port` | 默认 `3306`，必须存在对应实例登记 |
| `backup_type` | `full` 或 `incremental` |
| `disk_usage_threshold` | `1`–`99`，默认 `95` |
| `schedule_type` | `weekly`、`custom` 或 `once` |
| `weekdays` | `weekly` 必填；`0` 表示周日，`1`–`6` 表示周一至周六 |
| `weekday_backup_types` | 每个执行日可指定 `full` 或 `incremental` |
| `interval_minutes` | `custom` 模式至少 `1` |
| `start_at` | RFC 3339 时间，必填 |
| `retry_count` | `0`–`5` |
| `retry_interval_seconds` | 默认 `60` |
| `backup_location` | 目标机器上的安全绝对路径 |
| `include_binlog` | 为时间点恢复保存 Binlog；会增加空间占用 |

增量备份执行时，内核会选择同一机器、同一端口的最近成功全量或增量备份作为基础；没有成功基础备份时拒绝执行。

### 删除策略

```http
DELETE /api/v1/backup/policies/policy-01
```

成功响应：

```json
{"id":"policy-01"}
```

删除仅停止并移除策略，不删除 `backup_runs` 历史记录、任务历史或目标机器上的备份目录。

## 3. 执行备份

### 立即执行一个策略

```http
POST /api/v1/backup/policies/policy-01/run
```

成功返回 `201`：

```json
{
  "id": "run-01",
  "policy_id": "policy-01",
  "cluster": "prod",
  "machine_id": "machine-02",
  "port": 3306,
  "backup_type": "full",
  "backup_path": "/backup/mysql/prod/10.0.0.12_3306/20260723_run-01",
  "task_id": "task-01",
  "status": "pending",
  "include_binlog": true,
  "created_at": "2026-07-23T10:00:00Z"
}
```

### 批量执行集群策略

```http
POST /api/v1/backup/cluster-runs
Content-Type: application/json
```

```json
{"clusters":["prod","reporting"]}
```

响应中的每个 `item` 对应一个已启用策略：

```json
{
  "created": 2,
  "failed": 1,
  "items": [
    {
      "cluster": "prod",
      "policy_id": "policy-01",
      "policy": "prod-daily",
      "run_id": "run-01",
      "task_id": "task-01"
    },
    {
      "cluster": "reporting",
      "policy_id": "policy-02",
      "policy": "reporting-daily",
      "error": "增量备份前必须先完成一次同实例的全量备份"
    }
  ]
}
```

批量提交不是事务：单个策略失败不会撤销此前已经创建的任务。所有所选集群都没有已启用策略时返回 `400`。

## 4. 查询备份记录

```http
GET /api/v1/backup/runs?cluster=prod&limit=100
GET /api/v1/backup/runs/run-01
```

`cluster` 可省略。`limit` 范围为 `1`–`500`，缺省或越界时使用 `100`。

查询时内核会合并当前任务状态、Agent 事件日志、机器名称和 IP，因此 `status`、`logs`、`last_error` 是动态信息。重要字段如下：

| 字段 | 含义 |
| --- | --- |
| `id` | 备份运行 ID，也是恢复确认短语的一部分 |
| `base_run_id` | 增量备份的直接基础记录 |
| `backup_path` | 目标机器上的备份目录 |
| `task_id` | 备份任务 ID |
| `status` | `pending`、`sent`、`running`、`success` 或 `failed` 等任务状态 |
| `include_binlog` | 是否保存了时间点恢复所需 Binlog |
| `restore_task_id` | 最近一次从该记录创建的恢复/闪回任务 |

## 5. 恢复与闪回

```http
POST /api/v1/backup/runs/run-01/restore
Content-Type: application/json
```

### 全量或增量链物理恢复

```json
{
  "confirmation": "RESTORE run-01",
  "mode": "physical",
  "backup_path": "/backup/mysql/prod/10.0.0.12_3306/20260723_run-01",
  "mysql_user": "root",
  "mysql_password": "secret",
  "repair_replication": true
}
```

默认使用记录中的 `backup_path`。若所选记录是增量备份，内核沿 `base_run_id` 追溯同实例备份链，最多 100 层，链起点必须是全量备份。手工传入不同绝对路径时，该路径按独立全量备份处理。

### 按时间点恢复

```json
{
  "confirmation": "RESTORE run-01",
  "mode": "point_in_time",
  "backup_path": "/backup/mysql/prod/10.0.0.12_3306/20260723_run-01",
  "restore_time": "2026-07-23T09:45:00+08:00",
  "mysql_user": "root",
  "mysql_password": "secret",
  "repair_replication": false
}
```

所选备份必须成功且 `include_binlog=true`；`restore_time` 必填，不能晚于当前时间。内核先执行物理恢复，再从 XtraBackup 记录的 Binlog 位点回放至指定时间。

### 数据闪回

```json
{
  "confirmation": "FLASHBACK run-01",
  "mode": "flashback",
  "restore_time": "2026-07-23T09:45:00+08:00",
  "mysql_user": "root",
  "mysql_password": "secret",
  "database": "app",
  "tables": ["orders", "users"],
  "output_dir": "/data/gmha/recovery",
  "apply_flashback": false
}
```

闪回使用目标实例当前 Binlog 和 bin2sql 生成反向 SQL。`output_dir` 必须是绝对路径，默认 `/data/gmha/recovery`。`apply_flashback=false` 只生成文件；设为 `true` 会立即执行反向 SQL，风险更高。目标实例必须使用 ROW 格式且保留了覆盖目标时间段的完整 Binlog。

### 恢复响应

三种模式成功时都返回 `201` 和标准任务详情：

```json
{
  "task": {
    "ID": "task-restore-01",
    "Type": "exec",
    "MachineID": "machine-02",
    "Status": "pending",
    "ProgressPercent": 0
  },
  "steps": [],
  "events": []
}
```

`task` 内部沿用任务领域对象的大写字段名；外层 `task`、`steps`、`events` 为小写。

物理恢复会停止 MySQL 并替换实例目录。Agent 在替换前保留原目录，失败时尝试恢复原目录并重启原实例。成功后保留带 `.before_restore_<时间>` 后缀的旧目录，确认业务无误后再由运维流程清理。

## 6. 状态码与安全约束

| 状态码 | 含义 |
| --- | --- |
| `200` | 查询、更新或删除成功 |
| `201` | 策略、备份任务或恢复任务创建成功 |
| `400` | 参数、状态、备份链、确认短语或执行前条件不满足 |
| `404` | 策略或备份记录不存在 |
| `405` | 路径不支持当前 HTTP 方法 |
| `500` | 目标、策略、运行记录或任务信息读取失败 |

错误响应格式：

```json
{"error":"具体错误原因"}
```

生产调用方应在 Manager 前配置认证、授权、TLS 与操作审计。MySQL 密码只允许出现在创建/更新/恢复请求中，不会在查询响应中回显；同时不应写入调用日志。恢复接口必须保留精确确认短语校验：

- `physical`、`point_in_time`：`RESTORE {run_id}`
- `flashback`：`FLASHBACK {run_id}`
