# 性能监控 API

GMHA Manager 将 Agent 上报的数据库和机器指标保存为规范化时序样本。默认保留 7 天，所有查询均以持久化样本为准，不使用前端估算值。

## 指标目录

```http
GET /api/v1/performance/catalog
```

可选参数：

- `scope=mysql|machine`
- `category=<分类>`
- `available=true`：只返回当前可采集的指标

响应中的 `value_kind` 用来区分瞬时值（`gauge`）、累计计数器（`counter`）和状态值（`state`）。累计计数器在时序接口中自动换算为每秒或每分钟速率，发生计数器重置时不会产生负值。

## 查询单个指标

```http
GET /api/v1/performance/metrics?cluster=prod&metric=mysql_qps&range_minutes=60
```

参数：

- `cluster`：必填，集群名称或集群 ID。
- `metric`：必填，指标目录中的指标名。
- `range_minutes`：相对查询窗口，未指定时间时默认 60 分钟。
- `start_at`、`end_at`：自定义绝对时间，使用 RFC3339，例如 `2026-07-23T08:00:00+08:00`。
- `machine_id`：可选，只查询一台机器。
- `instance`：可选，只查询一个 MySQL 实例。
- `step_seconds`：可选，5–3600 秒；未指定时自动选择约 120 个数据点。

`start_at` 必须早于 `end_at`，单次查询不能超过 7 天。

响应示例：

```json
{
  "metric": {
    "name": "mysql_qps",
    "display_name": "QPS",
    "scope": "mysql",
    "unit": "次/s",
    "value_kind": "counter",
    "aggregation": "sum"
  },
  "query": {
    "cluster_id": "prod",
    "start_at": "2026-07-23T00:00:00Z",
    "end_at": "2026-07-23T01:00:00Z",
    "step_seconds": 30
  },
  "statistics": {
    "current": 126.4,
    "min": 21.2,
    "max": 418.8,
    "average": 119.3,
    "p95": 302.1
  },
  "freshness": {
    "last_collected_at": "2026-07-23T00:59:55Z",
    "age_seconds": 5,
    "stale": false,
    "successful_samples": 720,
    "failed_samples": 0
  },
  "series": [
    {
      "timestamp": "2026-07-23T00:00:00Z",
      "value": 105.2,
      "min": 48.1,
      "max": 180.4,
      "samples": 12
    }
  ],
  "latest_values": [
    {
      "machine_id": "machine-1",
      "instance": "10.0.0.8:3306",
      "value": 126.4,
      "success": true,
      "collected_at": "2026-07-23T00:59:55Z"
    }
  ],
  "data_points": 120,
  "generated_at": "2026-07-23T01:00:00Z"
}
```

## 数据语义

- 每个原始采集结果保留完整 JSON，便于审计与问题排查。
- `latest_values` 返回各机器或实例最新的原始采集值，结构化指标也不会因无法画折线而丢失。
- 可绘图数值另外保存为规范化样本。磁盘以 `device`、网卡以 `interface`、文件系统以 `mount` 作为标签。
- QPS、TPS、慢查询等累计计数器由相邻样本差值和实际采集间隔计算速率。
- `aggregation=sum` 汇总所有机器、实例或设备；`avg` 取平均值；`max` 取最大值。
- `freshness.stale=true` 表示最新样本超过指标采集周期的 3 倍（最低 30 秒），前端不得把它展示为实时数据。
- ERROR、WARNING、错误码和错误日志关键字指标按错误日志中可解析的时间戳统计最近 5 分钟；日志文件大小和增长率使用实际文件元数据。
