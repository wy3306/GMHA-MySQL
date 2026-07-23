# 索引管理

索引管理位于 **集群管理 → 实例管理 → 索引管理**。所有读取和 DDL 都通过目标机器的 Agent 执行，并使用 Agent 已登记的 MHA 管理账号；浏览器请求和 Manager 任务记录不保存数据库密码。

## 功能范围

- 按实例读取业务库索引，展示数据库、表、索引名、索引种类、唯一性、字段顺序、表行数以及索引空间。
- 根据 `mysql.innodb_index_stats` 的索引页估算单个 InnoDB 索引空间，同时展示表级索引总空间。
- 识别同表同列的重复索引，以及被其他 BTREE 索引左前缀覆盖的非唯一索引。唯一索引只在另一唯一索引定义完全相同时标记；主键不会标记或删除。
- 支持创建、重命名和删除二级索引。删除必须输入完整的 `schema.table.index`。
- 创建前必须填写业务目标、预期影响，并确认元数据锁和空间增长风险。

## 创建方式

### MySQL 原生在线 DDL

默认使用 `ALTER TABLE ... ALGORITHM=INPLACE, LOCK=NONE`。若目标版本、存储引擎或索引类型不能满足所选锁策略，任务直接失败，不会静默升级为更强的锁。

任务包含：

1. 评估表行数、数据/索引空间和等待中的元数据锁；
2. 使用 10 秒 `lock_wait_timeout` 执行 DDL；
3. 查询 `information_schema.statistics` 核验结果并刷新空间。

### PT 在线创建

勾选“使用 PT 工具在线创建”后使用 `pt-online-schema-change`：

1. 检查 `pt-online-schema-change` 已安装，并读取表引擎、主键、触发器和外键情况；
2. 执行 `--dry-run --print`；
3. dry-run 成功后执行 `--execute`，采用 `chunk-time=0.5`、`max-lag=10`、`Threads_running=25/50` 暂停/中止门禁；
4. 解析 PT 的复制百分比并实时写入任务进度；
5. 核验新索引并刷新索引空间。

PT 会创建影子表和同步触发器，可能临时占用接近原表大小的额外空间，并增加写放大、binlog 和复制压力。已有触发器、缺少主键/唯一键、复制过滤、外键处理失败或超过负载门禁时，PT 会拒绝或暂停操作。具体约束以 [Percona pt-online-schema-change 文档](https://docs.percona.com/percona-toolkit/pt-online-schema-change.html) 为准。

## API

`POST /api/v1/tasks/mysql-indexes`

共同字段：

```json
{
  "machine": "10.0.0.8",
  "port": 3306,
  "action": "list"
}
```

创建请求示例：

```json
{
  "machine": "10.0.0.8",
  "port": 3306,
  "action": "create",
  "schema": "app",
  "table": "orders",
  "name": "idx_status_created",
  "kind": "btree",
  "columns": [
    {"name": "status"},
    {"name": "created_at", "direction": "DESC"}
  ],
  "purpose": "降低订单状态查询扫描行数",
  "impact": "增加索引空间；低峰执行并观察写入 TPS",
  "lock_mode": "none",
  "lock_acknowledged": true,
  "online_with_pt": true
}
```

`action` 支持 `list`、`create`、`rename` 和 `delete`。所有操作返回标准任务详情；调用方可通过 `GET /api/v1/tasks?id=<task-id>` 查询步骤、日志和进度。

## 验证

自动化验证覆盖：

- 目标、影响和锁确认门禁；
- 标识符、索引种类、字段方向和前缀长度校验；
- 原生 DDL 的显式 `LOCK=NONE` 与不升级策略；
- PT 工具检查、dry-run、正式执行、负载/复制门禁和进度解析；
- 主键删除保护和删除确认；
- 索引输出解析、空间格式化、重复/左前缀冗余识别；
- 流式 stdout/stderr 并发回调的竞态检查；
- 全量 Go 测试与前端生产构建。
