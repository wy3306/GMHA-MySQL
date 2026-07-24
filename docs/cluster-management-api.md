# 集群管理 API 手册

本文覆盖 Web“集群列表”和集群详情中的全部服务端能力。所有路径均以 `/api/v1` 为前缀，请求与响应默认使用 `application/json`。

## 1. 集群与成员

| 方法 | 路径 | 作用 | 风险 |
| --- | --- | --- | --- |
| GET | `/clusters?page=1&page_size=20&keyword=` | 分页查询集群 | 只读 |
| POST | `/clusters` | 创建集群登记 | 低 |
| GET | `/clusters/{cluster_name}` | 查询集群详情 | 只读 |
| PUT | `/clusters/{cluster_name}` | 修改名称和描述 | 中 |
| DELETE | `/clusters/{cluster_name}` | 删除集群登记并解除普通成员归属；VIP、备份或活动任务会阻止 | 极高 |
| GET | `/clusters/{cluster_name}/machines` | 分页查询集群机器 | 只读 |
| POST | `/clusters/{cluster_name}/members` | 批量设置集群成员 | 中 |
| POST | `/machines/{machine_id}/assign-cluster` | 将单台机器加入集群 | 中 |
| DELETE | `/machines/{machine_id}/assign-cluster` | 将单台机器移出集群 | 中 |
| POST | `/clusters/{cluster_name}/cleanup` | 卸载集群内 MySQL 与 Agent、清理记录并删除集群 | 极高 |

创建集群：

```http
POST /api/v1/clusters
Content-Type: application/json

{"name":"prod","description":"生产高可用集群"}
```

批量加入成员：

```http
POST /api/v1/clusters/prod/members
Content-Type: application/json

{"machine_ids":["machine-01","machine-02"]}
```

`cleanup` 不是“清理失效成员”。它会执行与 CLI 一键清理相同的远端卸载流程：先阻止并发任务，安全撤销业务 VIP 并删除备份策略，再卸载 MySQL、清理残留、卸载 Agent、清理本地记录并删除集群。响应中的 `removed_vips`、`deleted_backup_policies` 和 `items` 给出实际处理范围。调用方必须显示影响范围，并要求输入 `CLEAN CLUSTER {cluster_name}` 后才可提交。

空集群调用 `cleanup` 会直接删除登记并返回空 `items`，不会因为“没有机器”留下无法清理的集群。

集群重命名只会在引用可原子处理时执行。存在 VIP、备份策略或活动任务时，服务端返回错误并保持原名称；只更新说明时把 `new_name` 设为当前名称。

## 2. 拓扑、性能和实例

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/clusters/{cluster_name}/topology?range_minutes=60&instance=` | 查询节点、复制边和概览指标 |
| GET | `/performance/catalog` | 查询指标目录 |
| GET | `/performance/metrics?cluster={cluster_name}&metric=mysql_qps&range_minutes=60` | 查询集群指标时序 |
| GET | `/mysql/instances` | 查询实例登记；按响应中的 `cluster` 过滤 |
| POST | `/tasks/cluster-mysql-install` | 为集群机器批量创建 MySQL 安装任务 |
| POST | `/tasks/cluster-mysql-uninstall` | 为集群机器批量创建 MySQL 卸载任务；VIP、备份或活动任务会阻止 |
| POST | `/tasks/mysql-cluster-upgrade/plan` | 生成滚动升级计划 |
| POST | `/tasks/mysql-cluster-upgrade/start` | 启动滚动升级 |
| GET | `/tasks/mysql-cluster-upgrade?run_id={run_id}` | 查询滚动升级状态 |

`topology` 支持 `range_minutes`（1 到 10080）、`start_at`、`end_at` 和 `instance`。自定义时间范围最长 7 天。

批量卸载请求：

```json
{"cluster":"prod","port":3306}
```

这是永久删除数据的极高风险操作。服务端会再次检查目标端口实例、业务 VIP、备份策略和活动任务；任一引用仍存在时不创建任何卸载任务。成功响应中的 `parent.task.id` 是统一监控编号。

滚动升级的 `plan` 和 `start` 使用：

```json
{"cluster":"prod","target_version":"8.4.10","port":3306,"risk_acknowledged":true}
```

AI 审批时由服务端设置 `risk_acknowledged`，模型不能自行绕过确认。`start` 返回的 `run_id` 同时是任务中心的持久化跟踪编号。

## 3. 业务 VIP

| 方法 | 路径 | 作用 | 风险 |
| --- | --- | --- | --- |
| GET | `/clusters/{cluster_name}/vip/config` | 查询 VIP 配置 | 只读 |
| POST | `/clusters/{cluster_name}/vip/config` | 保存配置、绑定并复检 | 高 |
| DELETE | `/clusters/{cluster_name}/vip/config?vip={vip_address}` | 全节点撤销后删除配置 | 极高 |
| GET | `/clusters/{cluster_name}/vip/status` | 查询持有者状态 | 只读 |
| POST | `/clusters/{cluster_name}/vip/scan` | 通过 Agent 扫描实机状态 | 低 |
| POST | `/clusters/{cluster_name}/vip/adopt` | 采纳策略允许的手工 VIP | 高 |
| POST | `/clusters/{cluster_name}/vip/validate` | 验证全部 VIP | 只读 |

新增或更新并立即绑定：

```http
POST /api/v1/clusters/prod/vip/config
Content-Type: application/json

{
  "vip_name": "业务 VIP",
  "vip_address": "10.0.0.100",
  "vip_prefix": 24,
  "default_interface": "eth0",
  "target_machine_id": "machine-01",
  "arping_count": 3
}
```

服务端执行顺序固定为：

1. 保存并规范化 L2 ARP 或 BGP 配置。
2. 从全部集群节点撤销同一地址。
3. 扫描并确认零持有者。
4. 在目标机器的指定业务网卡绑定地址并宣告。
5. 连续两轮扫描，确认只有目标机器持有。
6. 任一复检失败时撤销新目标地址并记录失败。

`vip_address` 必须来自已确认的网络规划。AI 不会响应“随便定”而猜测生产地址；它会生成可执行但被服务端标记为缺少地址的计划，提示补齐 `vip_address`、`vip_prefix`、`target_machine_id` 和 `default_interface`。

## 4. 复制架构与故障切换

| 方法 | 路径 | 作用 | 风险 |
| --- | --- | --- | --- |
| POST | `/clusters/{cluster_name}/architecture/plan` | 生成只读预检计划 | 只读 |
| POST | `/clusters/{cluster_name}/architecture/start` | 启动架构调整或 VIP 漂移 | 高 |
| GET | `/clusters/{cluster_name}/architecture/{run_id}` | 查询执行状态 | 只读 |
| POST | `/clusters/{cluster_name}/architecture/{run_id}/force` | 复制未追平时强制继续 | 极高 |
| POST | `/clusters/{cluster_name}/failover/plan` | 生成故障切换计划 | 只读 |
| POST | `/clusters/{cluster_name}/failover/start` | 启动受保护的故障切换 | 极高 |
| GET | `/clusters/{cluster_name}/failover/{failover_id}` | 查询故障切换状态 | 只读 |
| POST | `/clusters/{cluster_name}/bootstrap` | 组合安装、架构和 VIP 初始化 | 极高 |

架构预检与启动使用同一个请求结构。客户端必须先调用 `plan`，展示 `blocking_reasons`、`warnings` 和 `steps`，只有 `executable=true` 才能在审批后调用 `start`。

```json
{
  "architecture": "master_slave",
  "current_architecture": "dual_master",
  "current_master_machine_id": "machine-01",
  "preferred_new_master_machine_id": "machine-02",
  "move_vip": true,
  "nodes": [
    {"machine_id":"machine-01","port":3306,"role":"S","source_machine_id":"machine-02"},
    {"machine_id":"machine-02","port":3306,"role":"M"}
  ]
}
```

## 5. 集群备份

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/backup/policies?cluster={cluster_name}` | 查询集群备份策略 |
| POST | `/backup/policies` | 创建或更新策略 |
| DELETE | `/backup/policies/{policy_id}` | 删除策略 |
| POST | `/backup/policies/{policy_id}/run` | 立即运行 |
| GET | `/backup/runs?cluster={cluster_name}&limit=100` | 查询备份记录 |
| POST | `/backup/cluster-runs` | 批量运行多个集群的备份 |
| POST | `/backup/runs/{run_id}/restore` | 创建恢复任务 |

恢复必须携带精确确认短语 `RESTORE {run_id}` 或 `FLASHBACK {run_id}`，具体取决于恢复模式。

`POST /backup/cluster-runs` 只接收 `{"clusters":["prod"]}`，复用服务端策略中保存的凭据。响应中的 `parent_task_id` 是统一父任务；`items[].task_id` 是各策略的子任务。密码不会出现在 AI 对话、AI 计划或该请求中。

## 6. 集群自动化

`POST /tasks/cluster-automation` 接收 `clusters` 和 `operation`，支持机器采集、MySQL 采集、Shell、账号、参数、数据库巡检等批量工作流。滚动升级使用独立的 `/tasks/mysql-cluster-upgrade/*` 状态机。结果和报告接口：

- `GET /tasks/cluster-automation/results`
- `GET /tasks/cluster-automation/report`
- `GET /tasks/cluster-automation/artifacts/{task_id}/{file_name}`
- `GET /tasks/database-inspection/results`
- `GET /tasks/database-inspection/report`
- `GET /tasks/database-inspection/data`

## 7. AI 调用

外部 AI 或自动化客户端先读取：

```http
GET /api/v1/ai/capabilities
```

响应包含两层机器可读契约：

- `actions`：平台内置 AI 可以生成、审批、执行并监控的白名单动作；每项包含稳定的 `id`、`risk`、`target_kind`、`http_method`、`api_path` 和参数契约。
- `cluster_endpoints`：集群管理页面的完整 API 清单；`invocation_mode` 为 `read`、`ai_action`、`approval_api` 或 `secure_input_api`，并通过 `ai_action_id` 关联可直接执行的动作。

`secure_input_api` 不是“不支持”。它表示 API 已存在，但列在 `sensitive_parameters` 中的密码只能从受保护表单或密钥通道提交，不能写入模型对话。平台内置 AI 也读取这份目录，因此会给出准确入口和所缺参数，不再误报“平台没有 API”。

VIP 变更还有一层确定性兜底：当用户明确提出添加、绑定、漂移或撤销 VIP，而模型没有返回结构化计划时，Manager 会直接把意图映射到固定 VIP 白名单动作。地址、前缀、目标机器或网卡缺失时生成 `blocked` 计划并列出缺项；不会猜测地址，也不会回答“平台不支持”。

VIP 对应动作：

- `configure_cluster_vip`
- `remove_cluster_vip`
- `scan_cluster_vip`

集群登记与成员对应动作：

- `create_cluster`
- `update_cluster`
- `register_cluster_members`
- `remove_cluster_members`
- `cleanup_cluster`
- `delete_cluster`

备份与 MySQL 生命周期对应动作：

- `run_cluster_backup`
- `rolling_upgrade_cluster_mysql`
- `uninstall_cluster_mysql`

`cleanup_cluster` 会卸载并清理数据，属于极高风险；`delete_cluster` 只允许删除没有机器、MySQL、VIP、备份和活动任务的空登记。AI 和 API 客户端不得互换这两个动作。

AI 调用流程：

1. `POST /ai/chat` 生成白名单计划。客户端每轮只发送本次新增消息和稳定的 `session_id`，不需要重新提交完整对话。
2. 服务端重新读取集群、机器、网卡、复制、VIP、备份、告警和活动任务。
3. 中高风险计划在 UI 展示审批；高风险和极高风险还要求逐字确认短语。
4. `POST /ai/plans/execute` 提交审批结果。
5. Manager 调用内部应用服务执行，不允许模型生成 Shell、SQL 或任意 URL。
6. 任务状态和实机后置条件通过后，计划才标记成功。

### 7.1 会话与记忆

Manager 采用与 Claude Code 类似的分层记忆方式，但按生产运维安全边界做了收紧：

- `sessions`：服务端持久化对话标识和完整原始消息。新建对话拥有独立上下文；归档只隐藏对话，不删除消息、计划、记忆和审计。模型上下文压缩不会触发原始记录清理。
- `recent messages`：每轮加载同一会话最近 16 条、最多 24000 个字符的用户/助手原文，不会混入其他会话。
- `rolling summary`：模型每轮返回简洁的目标、约束、决定、进度和待确认项，Manager 按会话保存；较早原文不必在每轮重复发送。
- `validated active intent`：当前动作、目标和参数来自服务端已校验计划，独立于模型摘要保存。VIP 地址等执行关键值不会只凭模型记忆进入执行。
- `instructions`：用户可为会话保存长期说明；密码、Token、API Key、私钥等赋值会被拒绝。

会话接口：

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/ai/sessions?include_archived=true` | 查询活动及可选的已归档对话 |
| POST | `/ai/sessions` | 新建独立对话 |
| POST | `/ai/sessions/archive` | 归档对话，保留完整审计 |
| POST | `/ai/sessions/restore` | 恢复已归档对话 |
| GET | `/ai/memory?session_id={id}` | 查看会话滚动摘要、待确认项和当前受控意图 |
| PUT | `/ai/memory` | 更新会话长期说明或启停自动记忆 |
| DELETE | `/ai/memory?session_id={id}` | 清除摘要和会话说明，不删除原始消息或审计 |

新建对话：

```http
POST /api/v1/ai/sessions
Content-Type: application/json

{"title":"Demo01 VIP 变更"}
```

返回的 `id` 用于后续聊天：

```http
POST /api/v1/ai/chat
Content-Type: application/json

{
  "session_id": "session-01",
  "message": "给 Demo01 集群加入 VIP 192.168.31.222/24"
}
```

归档：

```http
POST /api/v1/ai/sessions/archive
Content-Type: application/json

{"id":"session-01"}
```

查看或维护记忆：

```http
GET /api/v1/ai/memory?session_id=session-01
```

```http
PUT /api/v1/ai/memory
Content-Type: application/json

{
  "session_id": "session-01",
  "enabled": true,
  "instructions": "变更方案优先使用业务网卡；任何高风险动作都先展示影响范围"
}
```

连续对话中的参数按以下规则处理：

- 新消息中明确给出的值优先；缺失值可从该会话上一条待处理计划和近期用户消息中继承。
- 机器名称、管理 IP 或机器 ID 只有唯一匹配时才转换成实际 `machine id`。
- 用户要求“选择同网段网卡”时，只有目标机器上恰好一个网卡与明确给出的 VIP/前缀同网段，Manager 才会自动选择；零个或多个匹配均保持阻断并要求确认。
- VIP 地址不会由模型或 Manager 猜测。它必须来自本会话中用户明确提供的 IPv4 地址、已登记 VIP，或受信任的网络规划数据。

例如可以分两轮提交，第二轮会继承第一轮的 VIP 和集群：

```http
POST /api/v1/ai/chat
Content-Type: application/json

{
  "session_id": "vip-demo01",
  "message": "给 Demo01 集群加入 VIP 192.168.31.222/24，网卡选择与目标机器同网段的网卡"
}
```

```http
POST /api/v1/ai/chat
Content-Type: application/json

{
  "session_id": "vip-demo01",
  "message": "绑定到 DB-01"
}
```

当 `DB-01` 在 `Demo01` 内唯一匹配，且其采集到的网卡中只有一个与 `192.168.31.222/24` 同网段时，生成的计划会包含完整的 `vip_address`、`vip_prefix`、`target_machine_id` 和 `default_interface`。若机器名或网卡匹配不唯一，计划状态为 `blocked`。

示例：

```http
POST /api/v1/ai/chat
Content-Type: application/json

{
  "session_id": "session-01",
  "provider_id": "provider-01",
  "message": "给 prod 集群绑定业务 VIP 10.0.0.100/24，目标 machine-01，网卡 eth0"
}
```

审批：

```http
POST /api/v1/ai/plans/execute
Content-Type: application/json

{
  "id": "plan-01",
  "approved": true,
  "confirmation": "确认配置并绑定集群 VIP prod"
}
```

## 8. 状态码与安全约束

- `200`：查询或同步操作成功。
- `201`、`202`：任务或异步运行已创建。
- `400`：参数错误或服务端安全预检阻止。
- `404`：资源或运行 ID 不存在。
- `409`：计划状态、工作流状态或高可用操作锁冲突。
- `405`：方法不受支持。
- `502`：AI 模型连接失败。

Manager 当前应部署在受信任管理网络，并由反向代理统一实施身份认证、TLS、来源限制和审计。Agent 注册、心跳和引导下载接口不应暴露给业务网络。
