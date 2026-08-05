# GMHA 告警管理接口

告警判断与第三方推送均在 Manager 内执行。Agent 只负责按下发配置采集并随心跳上报，避免在数据库主机上执行规则计算或网络推送。

## 等级与生命周期

- `notice`：通知
- `warning`：警告
- `critical`：严重
- `fatal`：致命

事件状态为 `firing` 或 `resolved`。事件具有稳定的 `id`、`fingerprint`、对象标签和 `automation_state`；确认与静默不会丢失原始事件。

活动告警在同一规则、机器和资源对象收到正常样本后自动转为 `resolved`，并保留在事件历史中，不再计入活动告警和等级统计。告警对象身份只使用稳定资源标签；采集范围、展示名称、MySQL 连接地址或实例描述变化不会产生无法恢复的“孤儿告警”。升级前由旧标签算法产生的活动事件，会在下一次同资源样本到达时自动合并或恢复。

规则通过 `consecutive_count` 控制连续命中次数，通过 `repeat_interval_seconds` 控制重复间隔，通过 `max_notifications` 控制一次持续故障最多推送次数（`0` 为不限）。

Manager 会持续比较同一 MySQL 实例相邻样本推算出的启动时间，并把启动时间变化记录为 `mysql_restart_detected` 事件。若重启时间与 GMHA 任务审计中的安全重启、参数重启、升级、恢复或架构调整任务吻合，事件记为“手动重启”，等级为 `notice`（通知）；没有匹配到平台任务时记为“意外重启”，等级为 `critical`（严重）。事件标签会保留实例端口、重启时间、前后运行时长，以及可用的任务 ID 和操作编码，便于追溯。

MySQL 告警在事件生成时会合并平台登记信息与持久化拓扑意图，保留实例名称、所属集群、IP、端口、角色、架构类型、复制关系及上游实例。拓扑记录暂不可用时，Manager 会用同一心跳内的运行角色和复制源信息补全可获得字段。邮件和页面详情均从事件中的同一份上下文渲染，后续拓扑变化不会改写已经解决的历史事件。

## 内置告警覆盖

Manager 首次启动或升级时会按稳定 ID 补齐缺失的内置规则，不覆盖用户已经调整过的阈值。规则中心按以下十类展示覆盖数量：

| 分类 | 主要内置告警 |
| --- | --- |
| 主机资源 | CPU、内存、文件系统、Inode、Swap、磁盘 IO、NTP 绝对偏移、SSH 探测 |
| Agent 健康 | 心跳中断、总体健康、子检查失败、Agent CPU 与 RSS |
| 实例可用性 | MySQL 连接、进程、监听端口 |
| 连接容量 | 连接使用率、长时间空闲连接 |
| 复制高可用 | 延迟、IO/SQL 线程、复制错误、追平状态、GTID 状态 |
| 事务与锁 | 长事务、行锁、元数据锁、阻塞时长与阻塞链、Undo History List |
| SQL 性能 | 临时表落盘、全表扫描、无索引 Join、最慢 SQL、缓存命中率与脏页 |
| 存储容量 | 数据、Binlog、Redo、Tmp、Undo 目录使用率与文件/表缓存容量 |
| 数据库碎片 | 高碎片表数量、单表最大碎片率、可回收碎片总空间 |
| 日志与安全 | 慢日志开关、近期 ERROR、OOM、磁盘写满、表损坏、崩溃恢复、权限失败 |

默认阈值是安全基线而非容量结论。上线后应按实例规格、业务峰谷、复制拓扑和维护窗口调整；连续命中次数用于过滤瞬时波动。

### 数据库碎片化口径

碎片化采集每 300 秒执行一次，只统计系统库之外的 InnoDB 表，并组合三个信号：

- `mysql_fragmented_table_count`：分配空间至少 1 GiB、`DATA_FREE` 至少 256 MiB 且碎片率至少 30% 的表数量；默认阈值为 1 / 5 / 20 张。
- `mysql_max_table_fragment_percent`：满足上述容量门槛的单表最大碎片率；默认阈值为 30% / 50% / 70%。
- `mysql_tablespace_fragment_total_bytes`：业务表可回收空间估算总量；默认阈值为 10 GiB / 50 GiB。

这里的 `DATA_FREE` 是 MySQL 元数据给出的已分配但未使用空间估算。共享表空间可能对多个表报告同一份空闲量，分区表在 `INFORMATION_SCHEMA.PARTITIONS` 中更精确，因此告警只用于发现候选对象，不能直接等同于操作系统可立即回收的磁盘空间。处理前应确认表空间模式、分区口径、可用磁盘与维护窗口，再选择在线重建或 `OPTIMIZE TABLE`；GMHA 不会根据该告警自动执行表重建。

## HTTP API

| 接口 | 方法 | 用途 |
| --- | --- | --- |
| `/api/v1/alerts/summary` | GET | 告警数量汇总 |
| `/api/v1/alerts/events` | GET | 按状态、等级、集群和关键字查询事件 |
| `/api/v1/alerts/events/action` | POST | 确认、静默或手动恢复事件 |
| `/api/v1/alerts/rules` | GET/POST/PUT/DELETE | 管理阈值规则 |
| `/api/v1/alerts/metrics` | GET | 查询主机、MySQL 与 Agent 健康指标目录 |
| `/api/v1/alerts/roles` | GET/POST/PUT/DELETE | 管理通知角色 |
| `/api/v1/alerts/recipients` | GET/POST/PUT/DELETE | 管理通知人员、邮箱与人员角色 |
| `/api/v1/alerts/channels` | GET/POST/PUT/PATCH/DELETE | 管理邮件、钉钉、飞书、Webhook、Zabbix 渠道；PATCH 仅更新单个渠道启用状态 |
| `/api/v1/alerts/channels/test` | POST | 发送测试消息 |
| `/api/v1/alerts/export/prometheus` | GET | Prometheus 文本暴露接口 |
| `/api/v1/alerts/export/zabbix` | GET | Zabbix 中转数据接口 |
| `/api/v1/alerts/events/automation` | PUT | 自愈/AI 处理器回写执行状态 |

事件查询支持 `status`、`severity`、`cluster_id`、`keyword`、`limit` 和 `offset`。`limit` 默认 `200`、最大 `1000`；`keyword` 会匹配规则、指标、机器、Agent、集群以及对象标签中的机器名称和 IP。

自动化状态仅允许 `pending`、`claimed`、`running`、`succeeded`、`failed`、`skipped`。当前版本不会自动执行恢复或破坏性动作；处理器应以 `{"id":"...","state":"claimed","expected_state":"pending"}` 原子声明事件。状态已被其他处理器改变时接口返回 `409 Conflict`，完成风险检查后再进入 `running`。

`summary` 和 `metrics` 响应均包含 `runtime`，可直接检查评估队列、通知队列、合并等待数量、持久化延后数量、丢弃数量与第三方推送成功/失败计数。Prometheus 导出同时暴露：

- `gmha_alert_evaluation_queue_depth`
- `gmha_alert_evaluation_overflow`
- `gmha_alert_notification_queue_depth`
- `gmha_alert_notification_outbox_pending`
- `gmha_alert_notifications_deferred_total`
- `gmha_alert_notifications_dropped_total`
- `gmha_alert_deliveries_total{result="success|failed"}`

`metrics.catalog` 是创建规则时应使用的标准指标目录，包含展示名称、范围、分类、单位、值类型、聚合方式、标准采集周期和可用性。磁盘、文件系统、Swap、网卡、系统负载、SSH 等结构化采集结果会被展开为带 `device`、`mount` 或 `interface` 标签的数值指标，因此可以直接参与阈值判断。

Zabbix 推送渠道支持原生 Sender/Trapper 协议，默认连接 Server 或 Proxy 的 `10051` 端口；同时保留 JSON 导出接口，便于已有中转程序接入。

## 资源保护

- 主机轻量指标默认 5 秒；磁盘、SSH、NTP 等较重指标默认 30–60 秒。
- MySQL 指标最低 5 秒，查询型指标的 API 配置同样会被 Manager 限制为至少 5 秒。
- Agent 自身 CPU、RSS 指标默认 15 秒，并直接读取 `/proc`，不会启动外部命令。
- 单项采集超时限制为 1–10 秒；每组最多 256 个采集器。
- 告警评估与第三方推送使用有界异步队列，不阻塞 Agent 心跳；评估积压按 Agent 合并为最新样本。
- 通知写入持久化 outbox 后再进入内存队列；队列满或 Manager 重启时会自动重新调度。单一采集样本即使随多次心跳重复上报，也只参与一次连续命中判断。

推送密码和令牌在查询接口中会显示为 `******`，不会下发至 Agent，也不会写入任务日志。生产环境仍应限制 Manager API 和元数据库的访问权限，并定期轮换第三方凭据。

推送配置页支持对每个渠道单独关闭或启用。关闭只更新该渠道的启用状态，不删除渠道配置、凭据或历史推送记录，也不会影响其他渠道；停用渠道不会接收后续告警推送。

渠道开关使用独立的原子更新接口。关闭操作会等待该渠道已经开始的单次网络投递结束，再持久化停用状态；接口返回后，队列中的后续告警和重试任务都会重新读取渠道状态并跳过该渠道。投递结果只更新状态、错误和最近投递时间，不会再用旧快照覆盖渠道开关。已发送到第三方平台的消息无法撤回。

通知角色与推送渠道分开管理。通知人员直接绑定唯一邮箱，并可被赋予一个或多个角色；邮箱渠道选择具体人员，不通过角色隐式扩展收件范围。投递前会重新读取人员信息，仅向当前启用人员的最新邮箱发送。已被人员使用的角色、已被渠道使用的人员不能直接删除，以避免产生悬空配置。

每个渠道可按通知阶段（告警触发、恢复通知）和告警内容分类进行筛选。渠道的最低等级、内容分类和通知阶段采用“并且”关系，只有全部满足的事件才会进入该渠道；内容分类留空表示接收全部分类。旧邮箱渠道中直接填写的收件地址继续兼容，进入编辑器后可逐步迁移为人员绑定。
