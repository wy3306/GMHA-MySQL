# MySQL 安装版本兼容性

GMHA 的二进制安装流程支持 MySQL `8.0.35` 至当前 MySQL `9.x`。安装包文件名必须包含完整版本、glibc 与架构，例如：

```text
mysql-9.7.1-linux-glibc2.28-x86_64.tar.xz
```

Manager 会先验证 MySQL 版本，再按目标机器的架构与 glibc 选择安装包。明确选择安装包时仍会执行同样的服务端校验。低于 8.0.35 或尚未核验的未来主版本不会进入可选列表。

## 版本规则

| 安装包版本 | 页面专属参数 | 后端规则 |
| --- | --- | --- |
| 8.0.35–8.0.x | `default_authentication_plugin`、`binlog_transaction_dependency_tracking`、`transaction_write_set_extraction` | 只允许在 8.0 中写入 |
| 8.4.x–8.x | `restrict_fk_on_non_standard_key`、`mysql_native_password` | `mysql_native_password` 仅在 9.0 之前允许 |
| 9.x | `restrict_fk_on_non_standard_key` | 拒绝 8.x 已移除的认证参数 |

`8.9.x` 按用户要求作为 8.4 之后、9.0 之前的 8.x 前向兼容版本处理。Oracle 的正式发布轨迹从 8.4 LTS 进入 9.0 Innovation，并没有发布 8.9 Server 系列；因此该规则用于识别和隔离参数边界，并不表示 Oracle 存在正式 8.9 版本。

页面返回的字段元数据与任务创建时的白名单来自同一份 Go 兼容目录。切换安装包后，页面只提交当前版本允许的专属参数；即使绕过页面，服务端也会拒绝已移除或不属于该版本的参数。

## 安装安全检查

安装步骤在初始化数据目录之前执行：

```text
mysqld --defaults-file=<my.cnf> --validate-config
```

只有配置验证成功才执行 `--initialize-insecure`。这可以阻止未知或已移除的启动参数导致一个半初始化实例。官方说明指出 `--defaults-file` 必须位于命令参数首位，当前命令顺序遵循该要求。

Percona Toolkit 不属于 MySQL Server 本体。当前自动 PT 安装只对 8.x 开放；9.x 仍可正常安装 MySQL，但页面会禁用自动 PT 安装，避免把工具链兼容性误当成 Server 兼容性。

## 官方依据

- [MySQL Releases: Innovation and LTS](https://dev.mysql.com/doc/refman/9.7/en/mysql-releases.html)
- [MySQL 8.4 新增、弃用和移除的变量](https://dev.mysql.com/doc/refman/8.4/en/added-deprecated-removed.html)
- [MySQL 9.0.0 Release Notes：移除 mysql_native_password](https://dev.mysql.com/doc/relnotes/mysql/9.7/en/news-9-0-0.html)
- [MySQL Server 配置验证](https://dev.mysql.com/doc/refman/8.0/en/server-configuration-validation.html)
- [MySQL 9.7 Server Option / Variable Reference](https://dev.mysql.com/doc/refman/9.7/en/server-option-variable-reference.html)
