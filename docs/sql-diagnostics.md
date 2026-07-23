# SQL 诊断

SQL 诊断用于跨 GMHA 已登记的 MySQL 实例查看实时 SQL、TOP-SQL、SLOW-SQL 和历史 SQL，并提供带并发校验与审计的 `KILL QUERY`。

Web 入口位于 **集群管理 → 选择集群 → SQL 诊断**。页面固定在当前集群范围内，实例筛选、历史回放与查杀审计不会跨集群展示。

## 数据口径

| 功能 | 主要来源 | 时间与完整性口径 |
| --- | --- | --- |
| 实时 SQL | `information_schema.processlist` 联合 `performance_schema.events_statements_current` | 每次打开或刷新页面都直连目标实例；优先使用 Performance Schema 皮秒计时器，降级时使用秒级 `PROCESSLIST.TIME` |
| TOP-SQL | `events_statements_summary_by_digest` | 保存累计计数器快照，查询时对相邻快照求差；识别 MySQL 重启和计数器重置，绝不把 MySQL 启动以来的累计值直接算入所选区间 |
| 已完成 SQL | `events_statements_history_long` | 保存单次语句耗时、锁等待、扫描/返回行数、错误、是否未使用索引等字段 |
| SLOW-SQL | 已完成 SQL、仍在执行的长 SQL、可用时的 `mysql.slow_log` | `slow_query_log=ON` 且 `log_output` 包含 `TABLE` 时自动只读回收慢日志；系统不会自动修改目标实例慢日志配置 |
| 历史会话 | 实时采样形成的 SQL 生命周期与已完成 SQL | 会话记录保存首次/末次发现时间、最大执行时长和采样次数；支持集群、实例、用户、库、SQL/Digest 和自定义时间段筛选 |

所有 API 结果中的 `coverage` 都是结果的一部分。`complete=false` 或 `warnings` 非空表示所选区间存在采集间断、实例不可达、消费者关闭、缺少 TOP 快照基线或明细达到安全上限。不要把不完整结果解释为“该时段没有 SQL”。

## 目标实例要求

GMHA 复用“预设账号”中的监控账号进行只读采集，复用 MHA 管理账号执行查杀。

- 监控账号至少需要 `PROCESS`、`SELECT` 和 `REPLICATION CLIENT`。项目默认监控账号权限已覆盖。
- MySQL 8.0 查杀账号需要 `CONNECTION_ADMIN`；MySQL 5.7 需要 `SUPER`。项目按版本初始化的 MHA 账号已覆盖。
- 必须启用 `performance_schema` 才能获得高精度执行时长、Digest TOP 和已完成语句。
- `performance_schema_max_sql_text_length` 决定 MySQL 能提供的 SQL 文本上限；GMHA 的 `max_sql_text_bytes` 只能进一步收紧，不能突破目标实例上限。

建议在维护窗口核对消费者。下面的修改是 MySQL 运行时配置，GMHA 不会代替 DBA 自动执行：

```sql
SELECT NAME, ENABLED
FROM performance_schema.setup_consumers
WHERE NAME IN ('events_statements_history_long', 'statements_digest');

UPDATE performance_schema.setup_consumers
SET ENABLED = 'YES'
WHERE NAME IN ('events_statements_history_long', 'statements_digest');
```

`events_statements_history_long` 是环形缓冲区。极高吞吐下，如果缓冲在 GMHA 下一采集周期前被覆盖，单条已完成 SQL 明细可能缺失；Digest 累计计数仍可用于区间 TOP。对要求逐条留痕的慢 SQL，建议同时启用 MySQL 慢日志并将 `log_output` 配置为包含 `TABLE`，或使用数据库既有的集中日志链路。

## 查杀安全规则

SQL 诊断只执行 `KILL QUERY <process_id>`，不会主动断开客户端连接。服务端在执行前重新读取目标会话并校验：

1. 实例必须是 GMHA 当前登记的实例；
2. 进程 ID 仍存在且仍在执行 SQL；
3. SQL Digest 与页面快照一致；
4. SQL 开始时间在计时精度允许的误差内一致；
5. 目标不能是 MySQL 系统线程或系统账号；
6. 操作者必须输入精确确认短语 `KILL <process_id>` 和至少 3 个字符的原因。

连接 ID 被复用或客户端已切换到另一条 SQL 时返回 HTTP `409`，不会执行查杀。每次请求都会写入 `sql_diagnostic_kill_audit`，包含目标 SQL 快照、用户、客户端、原因、请求来源、结果和时间。

## 默认配置与存储

- 采集间隔：5 秒，可配置 2–60 秒；
- 默认慢 SQL 阈值：1000 毫秒；
- 历史保留：24 小时，可配置 1–8760 小时；
- 单条 SQL 应用侧上限：64 KiB；
- SQL 原文：默认保存；
- 通用字符串/数字字面量遮蔽：默认关闭；
- `CREATE/ALTER USER ... IDENTIFIED BY` 等认证秘密：无条件遮蔽。

保留期数据每小时清理一次。Manager 元数据库可使用 SQLite、MySQL 或 PostgreSQL；持续采集表使用固定宽度 UTC 时间、64 位计数器和双精度耗时，确保跨数据库排序及聚合口径一致。

如果业务 SQL 可能包含隐私或密钥，建议开启“遮蔽字符串和数字字面量”、缩短保留期，并限制 Manager 元数据库的文件和数据库访问权限。

## API

- `GET /api/v1/sql-diagnostics/current`
- `GET /api/v1/sql-diagnostics/top`
- `GET /api/v1/sql-diagnostics/slow`
- `GET /api/v1/sql-diagnostics/history`
- `GET|PUT /api/v1/sql-diagnostics/config`
- `POST /api/v1/sql-diagnostics/kill`
- `GET /api/v1/sql-diagnostics/kill-audits`

时间参数使用 RFC3339，例如 `2026-07-23T01:00:00Z`。历史、TOP 和慢 SQL 支持 `start`、`end`、`cluster`、`machine`、`port`、`database`、`keyword` 和 `limit`；历史额外支持 `user`、`offset`。TOP 支持 `order_by=total_latency_ms|execution_count|average_latency_ms|rows_examined|error_count`，慢 SQL 支持 `threshold_ms` 和 `sort_by=started_at|duration_ms|rows_examined|rows_sent|error_count`；两者均支持 `direction=asc|desc`。
