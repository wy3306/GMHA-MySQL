# Linux 火焰图

GMHA 的火焰图功能由 Manager、Agent 和浏览器共同完成：

1. Manager 持久化采集记录与自动任务，并在到期时向目标 Agent 下发 typed task。
2. Agent 优先用目标 Linux 已有的 `perf` 采集调用栈，聚合为标准 folded stacks 后回传。
3. PID 或进程模式在 `perf` 不可用、`perf_event_paranoid` 受限时，可自动回退到 `/proc/<pid>/stack`。
4. Web 页面直接渲染 folded stacks，支持函数搜索、单击下钻和导出，不依赖服务端 Perl、Node 或外网。

## 使用入口

进入“集群列表 → 集群详情 → 性能监控 → 火焰图”。

- “立即采集”支持全系统、PID 和进程名称三种目标，采样窗口为 1–600 秒。
- “自动任务”支持单次、固定分钟间隔和每日执行。
- 采集记录、失败原因、实际后端、样本数和 folded stacks 均保存在 Manager 数据库。
- 删除采集记录只删除火焰图数据，不会删除任务中心的审计任务。

## Linux 权限

全系统采集需要 `perf`，并受 `kernel.perf_event_paranoid`、内核锁定模式和容器能力限制。
Agent 通常以 root 服务运行，可以直接采集。若安全基线不允许系统级 perf，请选择 PID/进程目标；
自动模式会尝试 `/proc` 兼容采样。兼容模式主要提供内核等待栈，用户态符号的完整度不及 perf。

## 制作离线包

在可联网、且与目标机发行版/版本/架构/内核系列一致的制品机上，先把 `perf` 及其依赖下载到一个目录，
但不要在目标机访问公网。然后在 GMHA 源码目录执行：

```sh
./scripts/build-flamegraph-offline-bundle.sh V0.0.2 amd64 ./perf-packages
```

生成的 `dist/gmha-flamegraph-V0.0.2-linux-amd64-offline.tar.gz` 包含：

- 支持火焰图任务的静态 Agent；
- Debian/Ubuntu、RPM 系、Alpine 或 Arch 的本地 perf 软件包；
- 自动识别发行版的离线安装器和 SHA-256 清单。

把 Agent 文件上传到“平台运维 → 版本升级”的 GMHA Agent 分类，由 Manager 分发；或者按现有 Agent
离线部署流程替换。将离线包复制到目标机后解压并运行：

```sh
sudo ./install.sh
```

若包内提供与架构匹配的静态 `bin/perf-amd64` 或 `bin/perf-arm64`，安装器会放到
`/opt/gmha-tools/flamegraph/bin/perf`；否则使用本地 `.deb`、`.rpm`、`.apk` 或
`.pkg.tar.*` 事务安装。Linux 的 perf 通常必须匹配内核工具版本，因此不要把一套发行版软件包
跨发行版复用。
