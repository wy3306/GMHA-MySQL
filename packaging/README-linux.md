# GMHA 完整程序包（Linux x86_64）

此程序包不要求目标机器安装 Go 或 Node.js。

## 一键启动

```bash
chmod +x start-web.sh gmha-web gmha bin/agentd
./start-web.sh
```

浏览器访问：

```text
http://服务器IP:8079
```

在“GMHA 启动中心”点击“启动 Manager”，健康检查通过后点击“进入 GMHA 控制台”。

## 程序包内容

- `gmha-web`：轻量 Web 启动器，默认监听 `0.0.0.0:8079`
- `gmha`：Manager、业务 API 和内嵌 Web 控制台
- `bin/agentd`：由 Manager 部署到受管服务器的 Agent
- `data/`：默认 SQLite 数据目录
- `logs/`：Manager 日志目录
- `start-web.sh`：一键启动脚本

Manager 默认监听 HTTP `:8080` 和 gRPC `:9100`。请在防火墙中放通 8079、8080、9100 端口。

如果浏览器访问 Manager 需要使用指定域名或 IP：

```bash
GMHA_MANAGER_URL=http://gmha.example.com:8080 ./start-web.sh
```

如果 Manager SSH 密钥不在运行用户的默认 `~/.ssh/id_ed25519` 或
`~/.ssh/id_rsa` 路径，可指定公钥路径；启动器会同时使用对应的私钥验证已有互信：

```bash
GMHA_MANAGER_PUBKEY=/opt/gmha/manager_ed25519.pub ./start-web.sh
```

数据库默认保存到程序包内的 `data/manager.db`，Manager 日志保存到 `logs/manager.log`。

## 版本升级

Manager 和 Agent 的当前版本均为 `V0.0.3`。执行 `scripts/build-release.sh V0.0.3`
会在 `dist/` 额外生成可直接上传到 Web 控制台的两个升级制品：

- `gmha-manager-V0.0.3-linux-amd64.bin`：上传到 `GMHA Manager` 分类。
- `gmha-agent-V0.0.3-linux-amd64.bin`：上传到 `GMHA Agent` 分类。

上传后进入“平台运维 → 版本升级”。Manager 升级会校验候选版本、备份当前程序、
原子替换并重启；Agent 升级会检查在线状态与架构，逐台备份替换，并以新鲜心跳上报的
版本作为升级后检查结果。升级记录与各阶段结果保存在 `~/.gmha/upgrade-jobs.json`。

## Percona Toolkit 离线包

目标机安装 PT 时不会访问公网软件源。先在一台可联网、且与目标 Linux
发行版及 CPU 架构一致的制品机上下载 Perl、DBI、DBD::mysql 等依赖的本地
安装包（`.deb`、`.rpm`、`.apk` 或 Arch Linux 的 `.pkg.tar.zst`），然后执行：

```sh
./scripts/build-pt-offline-bundle.sh \
  percona-toolkit-3.7.1-noarch.tar.gz \
  ./pt-dependencies \
  percona-toolkit-3.7.1-ubuntu22-offline-x86_64.tar.gz
```

把生成文件上传到 Manager 的 `percona-toolkit` 分类。Agent 会从 Manager
下载制品，按目标机的本地包格式离线安装依赖，并将 PT 安装到
`/opt/gmha-tools/percona-toolkit`。同一套流程支持 Debian/Ubuntu、
RHEL/CentOS/Rocky/Alma/openSUSE、Alpine 和 Arch 系发行版；不同 ABI 或架构
应分别制作离线包，并在文件名中保留 `ubuntu`、`debian`、`rocky`、`rhel`、
`centos`、`alma`、`suse`、`alpine` 或 `archlinux` 发行版标识，Manager 会按
目标机 OS 自动选择。也可以在包内提供 `toolkit/vendor/perl5`，安装器会自动
加入 `PERL5LIB`。

## MySQL 版本兼容矩阵

平台按版本能力而不是简单的主版本号生成配置、账号、复制、HA、备份和升级命令。
当前可识别 Oracle 已发布的 MySQL 5.7、8.x 与 9.x 系列；内置目录提供常用稳定线，
其他 Innovation 小版本可上传对应 Linux Generic 制品后使用。

| Server 系列 | 平台支持范围 | 关键兼容策略 | XtraBackup |
| --- | --- | --- | --- |
| 5.7 | 5.7.9–5.7.44 | 旧复制名称、旧 redo 配置、静态权限；无 Clone/SET PERSIST | 2.4.x |
| 8.0 | 8.0.11 及以上 8.0.x | 8.0.26 前使用旧复制名称；8.0.30 前使用旧 redo 配置；Clone 从 8.0.17 开启 | 8.0.x；8.0.11–8.0.33 需匹配的历史 PXB |
| 8.1–8.3 | 已发布 Innovation 系列 | 按实际变量边界过滤参数，禁止跨过 8.4 直接进入 9.x | 与 Server 相同的 8.x 系列 |
| 8.4 | 8.4.x LTS | 使用新复制/redo 配置；仅 8.4 提供旧认证插件兼容开关 | 8.4.x |
| 9.0–9.6 | 已发布 Innovation 系列 | 移除旧认证参数；升级路径必须经过 8.4 | 与 Server 相同的 9.x 系列 |
| 9.7 | 9.7.x LTS | 当前 LTS 配置、动态权限和 Clone 路径 | 9.7.x；当前上游只有 RC，生产前必须恢复演练 |

MySQL 5.7 生产环境建议直接使用最终版 5.7.44。安装包可使用 Oracle Linux
Generic 的 `.tar.gz` 格式，例如
`mysql-5.7.44-linux-glibc2.12-x86_64.tar.gz`。平台会自动使用 5.7 的配置项、
账号静态权限和 `MASTER/SLAVE` 复制命令。低于 glibc 2.17 或非 x86_64 的目标机
需要上传自行构建且 ABI 匹配的 XtraBackup 2.4 包。

物理备份会在执行前读取 `SELECT VERSION()` 并核对 XtraBackup 系列，备份元数据会
记录 Server 与 XtraBackup 版本；恢复在停库前再次核对，避免用错误系列 prepare
造成不可恢复的数据目录。升级路径强制为 `5.7.44 → 8.0 → 8.4 → 9.x`，同系列内
只允许向更高版本升级，禁止降级和同版本覆盖。

Percona Toolkit 3.7.1 的上游发布说明明确增加了 MySQL 8.4 支持，但尚未明确声明
MySQL 9.7 支持。平台允许在 9.x 主机离线部署 PT，并验证 Perl 依赖和命令入口；
`pt-online-schema-change`、`pt-table-sync` 等变更类命令在 9.x 生产执行前仍必须先在
同版本演练环境验证，不能把“安装成功”当作上游兼容性承诺。

## Linux 火焰图离线包

性能监控中的火焰图由 Agent 原生采集 folded stacks，并由浏览器渲染，不需要目标机安装
Go、Node.js 或 FlameGraph Perl 脚本。PID/进程模式在没有 `perf` 时会自动使用 Linux
`/proc` 兼容采样；全系统采集以及完整的用户态/内核态调用栈建议安装与目标内核匹配的 `perf`。

在与目标发行版、CPU 架构和内核系列一致的制品机上准备好 perf 的 `.deb`、`.rpm`、`.apk`
或 `.pkg.tar.*` 及全部依赖，然后执行：

```sh
./scripts/build-flamegraph-offline-bundle.sh \
  V0.0.3 amd64 ./perf-packages \
  ./dist/gmha-flamegraph-V0.0.3-linux-amd64-offline.tar.gz
```

将包内 `bin/agentd` 通过“平台运维 → 版本升级”分发；目标机解压后执行 `sudo ./install.sh`
即可离线安装 perf。支持构建 amd64、arm64、386、ppc64le、s390x 和 riscv64 的静态 Agent。
详细说明见 `docs/flamegraph.md`。
