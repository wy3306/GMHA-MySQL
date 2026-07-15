# GMHA 告警管理接口

告警判断与第三方推送均在 Manager 内执行。Agent 只负责按下发配置采集并随心跳上报，避免在数据库主机上执行规则计算或网络推送。

## 等级与生命周期

- `notice`：通知
- `warning`：警告
- `critical`：严重
- `fatal`：致命

事件状态为 `firing` 或 `resolved`。事件具有稳定的 `id`、`fingerprint`、对象标签和 `automation_state`；确认与静默不会丢失原始事件。

规则通过 `consecutive_count` 控制连续命中次数，通过 `repeat_interval_seconds` 控制重复间隔，通过 `max_notifications` 控制一次持续故障最多推送次数（`0` 为不限）。

## HTTP API

| 接口 | 方法 | 用途 |
| --- | --- | --- |
| `/api/v1/alerts/summary` | GET | 告警数量汇总 |
| `/api/v1/alerts/events` | GET | 按状态、等级、集群和关键字查询事件 |
| `/api/v1/alerts/events/action` | POST | 确认、静默或手动恢复事件 |
| `/api/v1/alerts/rules` | GET/POST/PUT/DELETE | 管理阈值规则 |
| `/api/v1/alerts/metrics` | GET | 查询主机、MySQL 与 Agent 健康指标目录 |
| `/api/v1/alerts/channels` | GET/POST/PUT/DELETE | 管理邮件、钉钉、飞书、Webhook、Zabbix 渠道 |
| `/api/v1/alerts/channels/test` | POST | 发送测试消息 |
| `/api/v1/alerts/export/prometheus` | GET | Prometheus 文本暴露接口 |
| `/api/v1/alerts/export/zabbix` | GET | Zabbix 中转数据接口 |
| `/api/v1/alerts/events/automation` | PUT | 自愈/AI 处理器回写执行状态 |

自动化状态仅允许 `pending`、`claimed`、`running`、`succeeded`、`failed`、`skipped`。当前版本不会自动执行恢复或破坏性动作；未来处理器应先将事件原子声明为 `claimed`，完成风险检查后再进入 `running`。

Zabbix 推送渠道支持原生 Sender/Trapper 协议，默认连接 Server 或 Proxy 的 `10051` 端口；同时保留 JSON 导出接口，便于已有中转程序接入。

## 资源保护

- 主机轻量指标默认 5 秒；磁盘、SSH、NTP 等较重指标默认 30–60 秒。
- MySQL 指标最低 5 秒，查询型指标的 API 配置同样会被 Manager 限制为至少 5 秒。
- Agent 自身 CPU、RSS 指标默认 15 秒，并直接读取 `/proc`，不会启动外部命令。
- 单项采集超时限制为 1–10 秒；每组最多 256 个采集器。
- 告警评估与第三方推送使用有界异步队列，不阻塞 Agent 心跳；队列满时优先保护心跳和数据库实例性能。

推送密码和令牌在查询接口中会显示为 `******`，不会下发至 Agent，也不会写入任务日志。生产环境仍应限制 Manager API 和元数据库的访问权限，并定期轮换第三方凭据。
