# MySQL 直方图管理

集群详情的“实例管理 → 直方图”用于管理 MySQL 优化器列统计。功能读取
`INFORMATION_SCHEMA.COLUMN_STATISTICS`，并通过 `ANALYZE TABLE ... UPDATE/DROP
HISTOGRAM` 更新目标实例。

## 兼容性边界

- 仅支持 MySQL 8.0 及以上版本（包括当前已验证的 8.x、9.x）。
- 明确不支持 MySQL 5.7。前端会禁用 5.7 实例，API 也会先检查登记版本，再连接实例读取
  `@@version` 做最终校验。
- JSON、空间类型以及被单列唯一索引覆盖的列不可创建直方图。
- 桶数范围为 1–1024，默认 100。

## API

所有接口都使用 Manager 保存的 MHA 管理账号，不接收也不返回数据库密码。

### 查询目录与现有直方图

```http
GET /api/v1/mysql/histograms?machine_id=machine-1&port=3306&schema=orders&table=sales
```

`schema`、`table` 为渐进式可选参数。未传 `schema` 时返回业务库列表和实例上全部现有
直方图；传入库后返回表；同时传入库和表后返回列、列兼容性及当前直方图。

### 创建或更新

```http
POST /api/v1/mysql/histograms
Content-Type: application/json

{
  "machine_id": "machine-1",
  "port": 3306,
  "schema": "orders",
  "table": "sales",
  "columns": ["region", "status"],
  "buckets": 100
}
```

### 删除

```http
DELETE /api/v1/mysql/histograms
Content-Type: application/json

{
  "machine_id": "machine-1",
  "port": 3306,
  "schema": "orders",
  "table": "sales",
  "columns": ["region"]
}
```

更新与删除使用 `NO_WRITE_TO_BINLOG`，直方图按实例维护，不会自动复制到其他节点。
`ANALYZE TABLE` 需要目标表的 `SELECT`、`INSERT` 权限，并可能短暂持有读锁，建议在业务
低峰执行。
