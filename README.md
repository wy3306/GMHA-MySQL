# GMHA-MySQL: MySQL High Availability Management Tool

GMHA-MySQL 是一个基于 Go 语言开发的 MySQL 高可用管理工具，旨在提供简单、高效的集群管理、监控纳管和故障切换功能。它支持交互式（Interactive）和单行命令（One-Liner）两种操作模式，方便运维人员根据场景灵活选择。

## 项目架构与文件说明

本项目采用分层架构设计，确保交互层、业务逻辑层与数据层的解耦。

```
GMHA-MySQL/
├── cmd/
│   └── repl.go               # [交互层] 包含 REPL 模式的主菜单、子菜单交互逻辑
├── internal/
│   ├── cli/                  # [业务逻辑层] 核心命令行功能实现
│   │   ├── commands.go       # 定义具体命令的执行逻辑 (Execute, runClusterAdd 等)
│   │   ├── oneliner.go       # 单行命令模式入口 (RunOneLiner)
│   │   ├── parse.go          # 命令参数解析器 (ParseLine, ParseArgs)
│   │   └── topology.go       # 集群拓扑展示逻辑
│   └── store/                # [数据层] 数据持久化
│       └── store.go          # 基于 bbolt 的数据库操作 (增删改查集群/主机/实例)
├── logger/
│   └── logger.go             # 日志工具包
├── data/
│   └── gmha.db               # 本地数据库文件 (运行时生成)
├── log/                      # 运行时日志目录
├── main.go                   # [程序入口] 根据参数决定进入 One-Liner 还是 REPL 模式
├── go.mod                    # Go 模块定义
├── go.sum                    # Go 依赖校验
└── README.md                 # 项目文档
```

## 功能特性

*   **双模式支持**：
    *   **交互模式 (REPL)**：提供菜单导航，适合人工操作和管理。
    *   **单行命令 (One-Liner)**：支持直接传参，适合脚本集成和自动化运维。
*   **集群管理**：支持集群的增删改查、主机纳管、MySQL 实例纳管。
*   **拓扑展示**：自动生成并打印集群的拓扑结构。
*   **持久化存储**：使用 bbolt 内嵌数据库，无需额外部署数据库组件。

## 快速开始

### 1. 编译项目

```bash
go build -v .
```

### 2. 运行交互模式

不带任何参数直接运行，进入交互式菜单：

```bash
./GMHA-MySQL
```

### 3. 运行单行命令模式

直接在命令行后追加参数：

```bash
# 列出所有集群
./GMHA-MySQL cluster list

# 添加一个新集群
./GMHA-MySQL cluster add --id=cluster-test --listen=127.0.0.1:9001
```

## 开发说明

*   **新增命令**：请在 `internal/cli/commands.go` 中注册新的命令处理逻辑，并在 `internal/cli/parse.go` 中添加解析规则。
*   **修改交互**：如需调整菜单结构，请修改 `cmd/repl.go`。
*   **数据模型**：如需修改存储结构，请参考 `internal/store/store.go`。
