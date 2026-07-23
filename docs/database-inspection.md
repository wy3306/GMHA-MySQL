# 数据库巡检

数据库巡检位于 **集群管理 → 实例管理 → 数据库巡检**，也可以从 **集群自动化** 对多个集群并行执行。

## 巡检类型

- **标准巡检**：检查数据库可用性、运行时长、连接使用率、活跃线程、慢查询比例、失败连接、Binlog 格式、事务持久性、GTID 和数据容量。
- **深度巡检**：在标准巡检基础上，增加无主键表、非 InnoDB 表、高碎片表、长事务、磁盘临时表比例、Buffer Pool 命中率、Undo 历史链和超大表检查。

每个检查项包含分类、严重级别、状态、当前值、期望阈值、说明和整改建议。健康评分从 100 分开始，警告项扣 8 分，严重项扣 20 分，最低为 0 分。

巡检通过已有 Agent 任务通道执行。Manager 不接收数据库用户名或密码，Agent 使用实例本地托管凭据生成临时 defaults file，执行结束后删除。

## 导出

- **Word 报告**：`.docx`，包括实例概况、健康评分、风险统计和逐项建议。
- **Excel 数据**：`.xlsx`，每个检查项一行，适合筛选、归档和二次分析。

## API

| 接口 | 方法 | 用途 |
| --- | --- | --- |
| `/api/v1/tasks/cluster-automation` | POST | 创建单实例或多集群巡检任务 |
| `/api/v1/tasks/database-inspection/results` | GET | 按 `task_ids` 查询结构化巡检结果 |
| `/api/v1/tasks/database-inspection/report` | GET | 按 `task_ids` 导出 Word 报告 |
| `/api/v1/tasks/database-inspection/data` | GET | 按 `task_ids` 导出 Excel 明细 |

创建标准巡检时使用 `operation=database_inspection`，创建深度巡检时使用 `operation=database_deep_inspection`。请求还需要 `clusters` 和 `port`；单实例巡检额外传递 `target_machine_id`。
