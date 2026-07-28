# GMHA Linux 兼容性

本文档描述 Agent 纳管和 MySQL 自动部署的明确支持边界。Manager 与 Agent 均使用
`CGO_ENABLED=0` 构建，Agent 不依赖目标机 glibc；MySQL、Percona Toolkit 和
XtraBackup 仍必须匹配目标发行版、CPU 架构及 glibc。

## 支持矩阵

| 发行版 | 等级 | 自动部署 | 说明 |
|---|---|---:|---|
| Ubuntu 20.04、22.04、24.04 | 支持 | 是 | 主要 apt 测试范围 |
| Ubuntu 18.04 | 老版本兼容 | 是 | 建议升级；工具制品需单独核对 |
| Ubuntu 16.04 及更早 | 不支持 | 否 | systemd、运行库和软件源低于当前基线 |
| CentOS Stream 8、9 | 支持 | 是 | dnf 路径 |
| CentOS Linux 7 | 老版本兼容 | 是 | glibc 2.17；发行版已停止维护 |
| CentOS Linux 8 | 老版本兼容 | 是 | 可运行，但发行版已停止维护 |
| CentOS 6 及更早 | 不支持 | 否 | 缺少受支持的 systemd 基线，运行库过旧 |
| Rocky Linux / AlmaLinux / RHEL / Oracle Linux 8、9 | 支持 | 是 | RHEL/dnf 路径 |
| 上述 RHEL 系 7 | 老版本兼容 | 是 | yum 与 glibc 2.17 兼容路径 |
| Debian 11、12 | 支持 | 是 | apt 路径 |
| Debian 10 | 老版本兼容 | 是 | 建议升级 |
| 未识别的 Linux | 未验证 | 否 | 先纳入兼容矩阵和测试后再开放 |

“老版本兼容”表示代码和依赖路径仍受保护，但操作系统本身可能已停止安全维护，
不建议作为新生产环境。平台会在 Agent 安装及 MySQL 任务创建前重新检测，不能
通过修改浏览器请求绕过。

## 必要条件

- Linux 使用 systemd，且 systemd 为正在运行的初始化系统。
- CPU 为 x86_64/amd64 或 aarch64/arm64；安装器会核对 Agent ELF 架构。
- MySQL 自动部署要求 glibc 2.17 或更高，并且本地 MySQL 包声明的 glibc 版本不高于目标机。
- 目标机具有 root 权限，以及 apt、dnf 或 yum 中的一种。
- 离线 PT 包必须包含 DBI、DBD::mysql、IO::Socket::SSL、Term::ReadKey；XtraBackup
  包必须与 MySQL 系列、架构和 glibc 匹配。

Debian/Ubuntu 依赖安装会按软件源动态选择 `libncurses6`/`libncurses5` 和
`libaio1`/`libaio1t64`，兼容新旧包名。RHEL 系会在 dnf 与 yum 之间选择，并处理
`ncurses-compat-libs` 和 `libaio-devel`。

## 制品与验证

- 标准 Linux 发布包中的 Manager 为 linux/amd64。
- Agent 同时构建 linux/amd64 和 linux/arm64；Manager 会按目标机架构选择。
- `internal/platform/linuxcompat` 的矩阵测试覆盖新旧 Ubuntu、CentOS Stream、
  CentOS 7、Rocky、Alma、Debian，以及拒绝 CentOS 6、Ubuntu 16、未知发行版和
  不支持的 CPU。
- GitHub Actions 的容器烟雾测试在 Ubuntu 18.04/20.04/22.04/24.04、
  CentOS 7 和 CentOS Stream 8/9 中运行静态 Agent 二进制；systemd 的启用和运行
  状态由安装单元测试及真实部署后的 `is-enabled`/`is-active` 双重检查验证。

容器烟雾测试不等同于真实 systemd 虚拟机验收。生产发布前仍应在目标发行版 VM
执行一次完整流程：纳管 Agent、重启主机、确认 Agent 自动上线、安装 MySQL、
重启主机、确认对应 `mysqld-<端口>.service` 自动启动。
