# GMHA-MySQL

GMHA-MySQL 是一个基于 Go 语言开发的 MySQL 高可用（High Availability）管理工具。它旨在简化 MySQL 集群的部署、管理、监控和故障切换流程。

## 🚀 功能特性

*   **多模式交互**：支持 交互式向导 (Wizard)、Web API 和 命令行 (CLI) 三种操作模式。
*   **集群管理**：轻松创建、查看和管理 MySQL 集群信息。
*   **资源纳管**：统一管理物理机/虚拟机、网卡及 IP 地址资源。
*   **持久化存储**：内置 SQLite 数据库 (`gmha.db`)，自动维护数据模型。
*   **高可用架构**：(开发中) 支持 VIP 管理、主从切换、一键部署和故障修复。

## 📂 项目结构

```
GMHA-MySQL/
├── main.go                 # 程序入口
├── gmha.db                 # SQLite 数据库文件 (自动生成)
├── internal/
│   ├── interaction/        # 交互层
│   │   ├── wizard/         # 命令行向导模式逻辑
│   │   ├── web/            # Web API 服务逻辑
│   │   └── cmdline/        # 单行命令模式逻辑
│   ├── intercore/          # 核心业务逻辑接口
│   ├── model/              # 数据库模型定义 (GORM)
│   ├── store/              # 数据存储初始化
│   └── terminal_utils/     # 终端交互工具库
├── logger/                 # 日志模块
└── README.md               # 项目说明文档
```

## 🛠️ 快速开始

### 1. 编译项目

确保您的环境中已安装 Go 1.24+。

```bash
# 下载依赖
go mod tidy

# 编译生成可执行文件
go build -o gmha main.go
```

### 2. 运行工具

GMHA-MySQL 支持多种运行模式：

#### 🖥️ 交互式向导模式 (推荐)
直接运行程序，无需参数，即可进入菜单驱动的交互界面。
```bash
./gmha
# 或
./gmha interactive
```
在此模式下，您可以通过数字菜单进行集群信息的查看和管理。

#### 🌐 Web 服务模式
启动 HTTP API 服务，提供 RESTful 接口供外部调用。
```bash
./gmha web
# 默认监听端口: 8080
```

#### ⌨️ 命令行模式 (CLI)
通过子命令直接执行特定任务（开发中）。
```bash
./gmha help
```

## 💾 数据库连接

项目使用 **SQLite** 作为元数据存储。
*   **数据库文件**: `gmha.db` (位于项目根目录)
*   **连接方式**: 您可以使用 Navicat、DBeaver 或 SQLite 命令行工具直接连接该文件查看数据。
*   **主要数据表**:
    *   `clusters`: 集群信息
    *   `machines`: 机器信息
    *   `network_interfaces`: 网卡信息
    *   `ip_addresses`: IP 地址信息

## 📝 开发计划

- [x] 基础项目结构与数据库模型
- [x] 交互式向导框架
- [x] 集群信息查看功能
- [ ] 机器纳管流程
- [ ] MySQL 一键部署
- [ ] VIP 漂移与故障切换
- [ ] Web 控制台前端

## 📄 License

MIT License
