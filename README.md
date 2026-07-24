# GMHA 架构说明文档

实例管理控制台 14 项操作的完整 HTTP 契约、异步结果读取方式与安全约束见 [实例管理 API 手册](docs/instance-management-api.md)。
备份目标发现、策略管理、运行查询、批量备份以及物理恢复/时间点恢复/数据闪回的 HTTP 契约见 [备份恢复 API 手册](docs/backup-recovery-api.md)。

## Web 启动器与 Release 程序包

Release 程序包提供独立的 `gmha-web` 启动器。执行 `./start-web.sh` 后访问 `http://服务器IP:8079`，在启动页点击“启动 Manager”，等待健康检查通过后即可进入完整 GMHA 控制台。程序包同时包含 Manager、内嵌前端和可部署到受管机器的 Agent，不要求目标机器安装 Go 或 Node.js。

本地构建 Linux x86_64 程序包：

```bash
./scripts/build-release.sh V0.0.3
```

构建结果位于 `dist/gmha-V0.0.3-linux-amd64.tar.gz`，并同时生成 SHA-256 校验文件。

## 数据库配置

Manager 的元数据存储默认使用 SQLite（无需额外安装服务），也可切换至 MySQL 或 PostgreSQL。三种数据库使用同一套表结构和仓储逻辑；切换前请使用新的空数据库，当前版本不自动迁移已有 SQLite 数据。

```bash
# 默认：SQLite，数据写入 ./data/manager.db
./gmha serve

# MySQL：--db-dsn 使用标准 MySQL DSN
./gmha serve --db-driver mysql --db-dsn 'gmha:password@tcp(127.0.0.1:3306)/gmha?charset=utf8mb4&parseTime=true'

# PostgreSQL：--db-dsn 使用 PostgreSQL URL
./gmha serve --db-driver postgres --db-dsn 'postgres://gmha:password@127.0.0.1:5432/gmha?sslmode=disable'
```

`--db` 保留为兼容参数：SQLite 时表示数据库文件路径；在 MySQL/PostgreSQL 模式下，如未提供 `--db-dsn`，它将作为连接串使用。

## 1. 项目概述

**GMHA**（Go MySQL High Availability）是一个用 Go 语言编写的 MySQL 高可用管理平台。它提供了完整的 MySQL 实例生命周期管理能力，包括机器纳管、Agent 部署、MySQL 安装/卸载、心跳监控、自动恢复和计划性故障转移等功能。

### 核心特性

- **机器纳管**：通过 SSH 连接测试、免密配置将服务器纳入管理
- **Agent 部署**：自动将 Agent 守护进程部署到被纳管机器
- **MySQL 管理**：模板化安装、配置计算、拓扑搭建
- **心跳监控**：gRPC 双向流实现毫秒级心跳检测
- **自动恢复**：Agent 离线时自动 SSH 恢复，支持冷却抑制
- **故障转移**：候选评分、Relay 回放、VIP 漂移、旧主隔离

---

## 2. 整体架构图

```mermaid
graph TB
    subgraph "用户交互层"
        CLI["CLI 命令行<br/>cmd/gmha"]
        WEB["Web 界面<br/>HTTP API"]
    end

    subgraph "接口适配层 (Interface)"
        CMD["CLI 子命令<br/>internal/interface/cli/command"]
        MENU["交互式菜单<br/>internal/interface/cli/menu"]
        HTTP["HTTP 路由 & 处理器<br/>internal/interface/http"]
        GRPC["gRPC 心跳服务<br/>internal/interface/grpc"]
    end

    subgraph "应用服务层 (App)"
        MS["机器服务<br/>MachineService"]
        AS["Agent 服务<br/>AgentService"]
        CS["集群服务<br/>ClusterService"]
        HS["心跳服务<br/>HeartbeatService"]
        RS["恢复服务<br/>RecoveryService"]
        HA["高可用服务<br/>HAService"]
        TS["任务服务<br/>TaskService"]
        MYSQL["MySQL 服务<br/>MySQLService"]
    end

    subgraph "用例层 (UseCase)"
        UC_MACHINE["机器用例<br/>Onboard / AssignCluster"]
        UC_AGENT["Agent 用例<br/>Install / Uninstall / Upgrade"]
        UC_TASK["任务用例<br/>CreateExec / CreateMySQLInstall / ..."]
    end

    subgraph "领域层 (Domain)"
        DOM_MACHINE["机器实体<br/>Machine / Status / Repository"]
        DOM_AGENT["Agent 实体<br/>Agent / State / Repository"]
        DOM_CLUSTER["集群实体<br/>Cluster / Repository"]
        DOM_HEART["心跳实体<br/>HeartbeatPayload / LatestStatus"]
        DOM_HA["HA 实体<br/>FailoverEvent / CandidateScore"]
        DOM_TASK["任务实体<br/>Task / Step / Event"]
        DOM_RECOVERY["恢复实体<br/>RecoveryTask / LatestState"]
        DOM_DYNAMIC["动态采集<br/>CollectTaskSpec / MetricResult"]
    end

    subgraph "基础设施层 (Infrastructure)"
        SQLITE["SQLite 仓储<br/>persistence/sqlite"]
        SSH_INFRA["SSH 基础设施<br/>client / trust / recovery"]
        RENDER["模板渲染<br/>render / engine"]
    end

    subgraph "外部系统"
        AGENT["Agent (agentd)<br/>部署在被纳管机器"]
        MYSQL_DB["MySQL 实例<br/>被管理的数据库"]
    end

    CLI --> CMD
    CLI --> MENU
    WEB --> HTTP

    CMD --> MS & AS & CS & HA & TS & MYSQL
    MENU --> MS & AS & CS & HA & TS & MYSQL
    HTTP --> MS & AS & TS & HA
    GRPC --> HS

    MS --> UC_MACHINE
    AS --> UC_AGENT
    TS --> UC_TASK

    UC_MACHINE --> DOM_MACHINE
    UC_AGENT --> DOM_AGENT
    UC_TASK --> DOM_TASK

    HS --> DOM_HEART & DOM_DYNAMIC
    RS --> DOM_RECOVERY
    HA --> DOM_HA

    DOM_MACHINE & DOM_AGENT & DOM_CLUSTER & DOM_TASK & DOM_RECOVERY & DOM_HA --> SQLITE
    MS & AS & RS --> SSH_INFRA
    MYSQL --> RENDER

    HS <-.->|"gRPC 双向流"| AGENT
    TS <-.->|"WebSocket"| AGENT
    AS <-.->|"SSH"| AGENT
    AGENT --> MYSQL_DB
```

---

## 3. 分层架构详解

项目采用**整洁架构（Clean Architecture）**设计，层次职责清晰，依赖方向单向（外层依赖内层）。

```
┌─────────────────────────────────────────────────────────┐
│                    cmd/ (入口层)                          │
│         gmha (管理端)    |    agent (Agent端)             │
├─────────────────────────────────────────────────────────┤
│               internal/interface/ (接口层)                │
│    CLI 命令  |  交互菜单  |  HTTP API  |  gRPC 服务        │
├─────────────────────────────────────────────────────────┤
│                 internal/app/ (应用服务层)                 │
│  Machine | Agent | Cluster | Heartbeat | Recovery | HA  │
├─────────────────────────────────────────────────────────┤
│               internal/usecase/ (用例层)                  │
│      机器用例  |  Agent用例  |  任务用例                    │
├─────────────────────────────────────────────────────────┤
│               internal/domain/ (领域层)                   │
│   Machine | Agent | Cluster | Heartbeat | HA | Task     │
├─────────────────────────────────────────────────────────┤
│            internal/infrastructure/ (基础设施层)           │
│       SQLite仓储  |  SSH客户端  |  模板渲染引擎            │
├─────────────────────────────────────────────────────────┤
│              internal/platform/ (平台层)                  │
│     配置管理  |  HTTP服务  |  SQLite存储  |  SSH客户端      │
└─────────────────────────────────────────────────────────┘
```

### 3.1 入口层 (cmd/)

| 入口 | 说明 |
|------|------|
| `cmd/gmha/main.go` | 管理端主入口，支持 CLI 菜单、Web 服务、子命令三种模式 |
| `cmd/agent/main.go` | Agent 端主入口，加载配置后启动 Agent 守护进程 |

### 3.2 接口层 (internal/interface/)

| 组件 | 说明 |
|------|------|
| `cli/command/` | CLI 子命令分发：machine、mysql、agent、task 等 |
| `cli/menu/` | 交互式 TUI 菜单：主菜单、机器管理、MySQL 管理等 |
| `http/` | HTTP REST API 路由和处理器 |
| `grpc/` | gRPC 心跳服务端，处理双向流心跳 |

### 3.3 应用服务层 (internal/app/)

| 服务 | 职责 |
|------|------|
| `MachineService` | 机器纳管、列表、更新、删除、集群分配 |
| `AgentService` | Agent 安装、升级、卸载、列表、重试安装 |
| `ClusterService` | 集群 CRUD 操作 |
| `HeartbeatService` | 心跳处理、状态转换、协调循环、动态采集配置管理 |
| `RecoveryService` | 自动恢复：扫描离线 Agent、SSH 检查、启动/重启服务 |
| `HAService` | 故障转移：候选评分、Relay 回放、VIP 漂移、旧主隔离 |
| `TaskService` | 任务创建、分发（WebSocket）、状态跟踪 |
| `MySQLService` | MySQL 实例管理 |

### 3.4 用例层 (internal/usecase/)

| 用例 | 说明 |
|------|------|
| `machine/onboard` | 机器纳管流程：SSH 测试 → 保存 → 免密配置 |
| `machine/assign_cluster` | 集群分配 |
| `agent/install_agent` | Agent 安装：上传二进制 → 配置 → 启动服务 |
| `agent/uninstall_agent` | Agent 卸载 |
| `agent/upgrade_agent` | Agent 升级 |
| `task/create_*` | 各类任务创建（exec、采集、MySQL安装/卸载/拓扑） |

### 3.5 领域层 (internal/domain/)

| 领域 | 核心实体 |
|------|----------|
| `machine/` | Machine（机器）、Status（状态机：pending→ssh_connected→ssh_trust_ready→agent_online） |
| `agent/` | Agent（代理）、State（状态：installing/online/offline/error） |
| `cluster/` | Cluster（集群） |
| `credential/` | SSHCredential（SSH凭据） |
| `heartbeat/` | HeartbeatPayload、LatestStatus、AgentState（INIT→ONLINE→SUSPECT→DEGRADED→OFFLINE） |
| `ha/` | ClusterInfo、VIPConfig、FailoverPolicy、FencingPolicy、FailoverEvent、CandidateScore |
| `task/` | Task、Step、Event、各种 Spec/Result |
| `recovery/` | RecoveryTask、LatestState |
| `dynamic/` | CollectTaskSpec、DynamicCollectConfig、MetricResult |

### 3.6 基础设施层 (internal/infrastructure/)

| 组件 | 说明 |
|------|------|
| `persistence/sqlite/` | 所有领域实体的 SQLite 仓储实现 |
| `ssh/` | SSH 客户端、免密信任服务、恢复执行器 |
| `render/` | Go 模板渲染引擎，生成 MySQL 配置、systemd 单元等 |

---

## 4. 双进程架构

GMHA 采用 Manager + Agent 双进程架构：

```mermaid
graph LR
    subgraph "管理节点"
        MGR["Manager (gmha)<br/>HTTP :8080<br/>gRPC :9100"]
        DB["SQLite<br/>manager.db"]
        MGR --- DB
    end

    subgraph "被纳管节点 1"
        AGT1["Agent (agentd)<br/>机器 1"]
        MYSQL1["MySQL 实例 1"]
        AGT1 --- MYSQL1
    end

    subgraph "被纳管节点 2"
        AGT2["Agent (agentd)<br/>机器 2"]
        MYSQL2["MySQL 实例 2"]
        AGT2 --- MYSQL2
    end

    MGR <-.->|"gRPC 双向流<br/>心跳 + 动态配置"| AGT1
    MGR <-.->|"gRPC 双向流<br/>心跳 + 动态配置"| AGT2
    MGR <-.->|"WebSocket<br/>任务分发"| AGT1
    MGR <-.->|"WebSocket<br/>任务分发"| AGT2
    MGR <-.->|"SSH<br/>安装/恢复"| AGT1
    MGR <-.->|"SSH<br/>安装/恢复"| AGT2
```

### 通信协议

| 通道 | 协议 | 用途 |
|------|------|------|
| 心跳 | gRPC 双向流 | Agent 上报心跳/指标，Manager 下发动态采集配置 |
| 任务 | WebSocket | Manager 分发任务给 Agent，Agent 上报执行进度 |
| 管理 | SSH | Manager 通过 SSH 安装/升级/恢复 Agent |
| API | HTTP REST | CLI 和 Web 界面调用 Manager API |

---

## 5. 核心数据流

### 5.1 机器纳管流程

```mermaid
sequenceDiagram
    participant U as 用户
    participant M as Manager
    participant S as SSH
    participant DB as SQLite

    U->>M: 机器纳管请求 (name, ip, ssh-port, user, password)
    M->>S: SSH 连接测试
    S-->>M: 连接成功
    M->>DB: 保存机器 (status: ssh_connected)
    M->>S: 分发 SSH 公钥
    S-->>M: 免密配置成功
    M->>DB: 更新状态 (status: ssh_trust_ready)
    M-->>U: 纳管成功
```

### 5.2 Agent 安装流程

```mermaid
sequenceDiagram
    participant U as 用户
    participant M as Manager
    participant A as Agent
    participant DB as SQLite

    U->>M: Agent 安装请求 (machine_id)
    M->>DB: 更新机器状态 (agent_installing)
    M->>A: SSH 上传 agentd 二进制 + 配置文件
    M->>A: SSH systemctl start gmha-agent
    A->>M: gRPC 心跳连接建立
    M->>DB: 更新状态 (agent_online)
    M-->>U: 安装成功
```

### 5.3 心跳处理流程

```mermaid
sequenceDiagram
    participant A as Agent
    participant G as gRPC Server
    participant H as HeartbeatService
    participant DB as SQLite

    loop 每 5 秒
        A->>G: HeartbeatRequest (身份, 运行时, 健康检查, 主机指标, MySQL指标)
        G->>H: ProcessHeartbeat()
        H->>DB: 更新 LatestStatus
        H->>H: 同步状态到 Agent/Machine 实体
        H-->>G: HeartbeatResponse (服务器时间, 状态, 动态采集配置)
        G-->>A: 返回响应
    end

    loop 每 5 秒 (协调循环)
        H->>DB: 读取所有 LatestStatus
        H->>H: 检查超时: ONLINE→SUSPECT→OFFLINE
        H->>DB: 更新状态变更
    end
```

### 5.4 自动恢复流程

```mermaid
sequenceDiagram
    participant R as RecoveryService
    participant SSH as SSH
    participant A as Agent
    participant DB as SQLite

    loop 每 10 秒
        R->>DB: 扫描离线 Agent
        R->>R: 检查冷却抑制
        R->>DB: 创建恢复任务
        R->>SSH: 检查 Agent 服务状态
        SSH-->>R: systemctl status gmha-agent

        alt 服务未运行
            R->>SSH: systemctl start gmha-agent
        else 服务异常
            R->>SSH: systemctl restart gmha-agent
        end

        R->>DB: 更新状态 (waiting_heartbeat)
        R->>R: 等待心跳恢复

        alt 心跳恢复
            R->>DB: 标记成功 (succeeded)
        else 超时未恢复
            R->>DB: 标记失败 (failed)
        end
    end
```

### 5.5 故障转移流程

```mermaid
stateDiagram-v2
    [*] --> INIT
    INIT --> CHECK_OLD_MASTER: 检查旧主状态
    CHECK_OLD_MASTER --> ACQUIRE_FAILOVER_LOCK: 获取故障转移锁
    ACQUIRE_FAILOVER_LOCK --> FENCE_OLD_MASTER: 隔离旧主
    FENCE_OLD_MASTER --> CHECK_VIP_CONFLICT: 检查 VIP 冲突
    CHECK_VIP_CONFLICT --> SELECT_FIRST_CANDIDATE: 选择候选节点
    SELECT_FIRST_CANDIDATE --> WAIT_RELAY_REPLAY: 等待 Relay 回放
    WAIT_RELAY_REPLAY --> RESELECT_CANDIDATE: 重新选择候选
    RESELECT_CANDIDATE --> BINLOG_RESCUE: Binlog 救援
    BINLOG_RESCUE --> PROMOTE_NEW_MASTER: 提升新主
    PROMOTE_NEW_MASTER --> MOVE_VIP: 漂移 VIP
    MOVE_VIP --> VERIFY_NEW_MASTER: 验证新主
    VERIFY_NEW_MASTER --> REPOINT_REPLICAS: 重定向从库
    REPOINT_REPLICAS --> DONE: 完成

    FENCE_OLD_MASTER --> FAILED: 隔离失败
    WAIT_RELAY_REPLAY --> FAILED: 回放超时
    PROMOTE_NEW_MASTER --> FAILED: 提升失败
    MOVE_VIP --> FAILED: VIP 漂移失败
```

---

## 6. 动态指标采集架构

```mermaid
graph TB
    subgraph "Manager 端"
        CONFIG["动态采集配置<br/>BuildDefaultDynamicCollectConfig<br/>BuildDefaultMySQLDynamicCollectConfig"]
        HS["HeartbeatService<br/>管理采集配置版本"]
    end

    subgraph "Agent 端"
        DM["HostDynamicManager<br/>主机指标采集管理"]
        MM["MySQLDynamicManager<br/>MySQL 指标采集管理"]
        REG["CollectorRegistry<br/>采集器注册表"]
        BUILTIN["BuiltinCollectors<br/>内置采集器"]
        COMMAND["CommandCollectors<br/>命令采集器"]
    end

    subgraph "采集器类型"
        CPU["CPU 使用率"]
        MEM["内存使用率"]
        DISK["磁盘 IO"]
        NET["网络流量"]
        MYSQL_C["MySQL 连接/复制/性能/存储"]
    end

    CONFIG -->|"gRPC 心跳下发"| HS
    HS -->|"HeartbeatResponse"| DM
    HS -->|"HeartbeatResponse"| MM

    DM --> REG
    MM --> REG
    REG --> BUILTIN
    REG --> COMMAND

    BUILTIN --> CPU & MEM & DISK & NET
    BUILTIN --> MYSQL_C
    COMMAND --> MYSQL_C

    DM -->|"HeartbeatRequest"| HS
    MM -->|"HeartbeatRequest"| HS
```

### 指标分类

| 类别 | 指标数量 | 采集间隔 | 说明 |
|------|----------|----------|------|
| 主机基础指标 | 16 | 1秒 | CPU、内存、IO、负载、NTP、SSH、inode、MySQL存活 |
| MySQL 连接 | 17 | 1-10秒 | 连接数、线程状态、连接使用率 |
| MySQL 复制 | 14 | 1-30秒 | 主从延迟、IO/SQL线程、Relay Log、半同步 |
| MySQL 性能 | 38 | 1-300秒 | QPS、TPS、慢SQL、锁等待、临时表、全表扫描 |
| MySQL 存储 | 32 | 5-300秒 | Buffer Pool、Binlog、Redo、Undo、文件句柄 |
| MySQL 拓扑 | 4 | 1-10秒 | server_id、角色、主库变化 |
| MySQL 变量 | 3 | 1-300秒 | read_only、super_read_only、慢查询阈值 |

---

## 7. 技术栈

| 组件 | 技术 | 说明 |
|------|------|------|
| 语言 | Go 1.24.3 | 主要开发语言 |
| 数据库 | SQLite (modernc.org/sqlite) | 纯 Go 实现，无 CGo 依赖 |
| RPC | gRPC (google.golang.org/grpc) | Agent-Manager 心跳双向流 |
| SSH | golang.org/x/crypto/ssh | SSH 连接和免密配置 |
| MySQL 驱动 | github.com/go-sql-driver/mysql | Agent 端直连 MySQL |
| 终端 UI | golang.org/x/term | 交互式 CLI 菜单 |

---

## 8. 目录结构总览

```
GMHA/
├── api/proto/                    # Protobuf 定义
│   └── agent_heartbeat.proto     # 心跳 gRPC 服务定义
├── cmd/                          # 程序入口
│   ├── gmha/main.go              # 管理端入口
│   └── agent/main.go             # Agent 端入口
├── configs/                      # 配置文件
│   ├── profiles/mysql/           # MySQL 配置档案 (default/prod/oltp/test)
│   ├── profiles/sysctl/          # 系统内核参数档案
│   └── templates/mysql/          # MySQL 配置模板 (my.cnf/systemd/limits/sysctl)
├── internal/                     # 核心业务代码
│   ├── agent/                    # Agent 端实现
│   │   ├── core/                 # 核心组件 (dispatcher/heartbeat/register/reporter)
│   │   ├── handler/              # 任务处理器 (exec/collect/mysql_install/...)
│   │   ├── collect/              # 系统指标采集器 (cpu/disk/memory/network/os)
│   │   ├── dynamic/              # 动态主机指标采集
│   │   ├── mysqldynamic/         # 动态 MySQL 指标采集
│   │   ├── mysqlcheck/           # MySQL 心跳检查
│   │   └── selfcheck/            # Agent 自检
│   ├── app/                      # 应用服务层
│   ├── domain/                   # 领域模型层
│   │   ├── agent/                # Agent 实体
│   │   ├── machine/              # 机器实体
│   │   ├── cluster/              # 集群实体
│   │   ├── credential/           # SSH 凭据
│   │   ├── heartbeat/            # 心跳实体
│   │   ├── ha/                   # 高可用实体
│   │   ├── task/                 # 任务实体
│   │   ├── recovery/             # 恢复实体
│   │   └── dynamic/              # 动态采集实体
│   ├── infrastructure/           # 基础设施层
│   │   ├── persistence/sqlite/   # SQLite 仓储实现
│   │   ├── ssh/                  # SSH 基础设施
│   │   └── render/               # 模板渲染引擎
│   ├── interface/                # 接口适配层
│   │   ├── cli/command/          # CLI 子命令
│   │   ├── cli/menu/             # 交互式菜单
│   │   ├── http/                 # HTTP API
│   │   └── grpc/                 # gRPC 服务
│   ├── usecase/                  # 用例层
│   │   ├── machine/              # 机器用例
│   │   ├── agent/                # Agent 用例
│   │   └── task/                 # 任务用例
│   ├── mysql/                    # MySQL 工具 (计算器/包选择/账号/配置)
│   ├── collect/                  # 信息采集
│   ├── platform/                 # 平台层 (配置/HTTP/SQLite/SSH)
│   └── ports/                    # 端口接口
├── pkg/                          # 公共包
│   ├── api/v1/                   # 共享 API 类型
│   └── rpc/heartbeat/            # gRPC 心跳服务定义
├── scripts/                      # 脚本和模板
├── software/                     # MySQL/Keepalived 安装包
├── go.mod                        # Go 模块定义
└── go.sum                        # 依赖校验
```

---

## 9. 状态机

### 9.1 机器状态机

```mermaid
stateDiagram-v2
    [*] --> pending: 纳管请求
    pending --> ssh_connected: SSH 连接测试通过
    pending --> ssh_failed: SSH 连接失败
    ssh_connected --> ssh_trust_ready: SSH 免密配置成功
    ssh_trust_ready --> agent_installing: 开始安装 Agent
    agent_installing --> agent_online: Agent 心跳上线
    agent_installing --> agent_error: 安装失败
    agent_online --> agent_error: Agent 异常
    agent_error --> agent_installing: 重试安装
```

### 9.2 Agent 心跳状态机

```mermaid
stateDiagram-v2
    [*] --> INIT: Agent 注册
    INIT --> ONLINE: 首次心跳
    ONLINE --> SUSPECT: 心跳超时 (2个周期)
    SUSPECT --> ONLINE: 心跳恢复
    SUSPECT --> DEGRADED: 持续超时 (4个周期)
    DEGRADED --> ONLINE: 心跳恢复
    DEGRADED --> OFFLINE: 确认离线 (6个周期)
    OFFLINE --> ONLINE: 心跳恢复
```

### 9.3 任务状态机

```mermaid
stateDiagram-v2
    [*] --> pending: 任务创建
    pending --> sent: 分发给 Agent
    sent --> running: Agent 开始执行
    running --> success: 执行成功
    running --> failed: 执行失败
```

### 9.4 恢复任务状态机

```mermaid
stateDiagram-v2
    [*] --> pending: 检测到离线
    pending --> confirming: 确认离线
    confirming --> executing: SSH 检查
    executing --> waiting_heartbeat: 服务已启动
    waiting_heartbeat --> succeeded: 心跳恢复
    waiting_heartbeat --> failed: 超时未恢复
    executing --> failed: SSH 操作失败
    pending --> suppressed: 冷却抑制
```

---

## 10. 部署架构

```mermaid
graph TB
    subgraph "管理节点 (Manager)"
        GMHA["gmha 进程<br/>HTTP API :8080<br/>gRPC :9100"]
        SQLITE["SQLite<br/>data/manager.db"]
        GMHA --- SQLITE
    end

    subgraph "数据库节点 1"
        AGT1["agentd 进程"]
        MYSQL1["MySQL :3306"]
        AGT1 --- MYSQL1
    end

    subgraph "数据库节点 2"
        AGT2["agentd 进程"]
        MYSQL2["MySQL :3306"]
        AGT2 --- MYSQL2
    end

    subgraph "数据库节点 3"
        AGT3["agentd 进程"]
        MYSQL3["MySQL :3306"]
        AGT3 --- MYSQL3
    end

    GMHA <-.->|"gRPC + WebSocket + SSH"| AGT1
    GMHA <-.->|"gRPC + WebSocket + SSH"| AGT2
    GMHA <-.->|"gRPC + WebSocket + SSH"| AGT3

    USER["运维人员"] -->|"CLI / Web"| GMHA
```

---

## 11. 关键设计决策

1. **纯 Go SQLite**：使用 `modernc.org/sqlite` 避免 CGo 依赖，简化跨平台编译
2. **gRPC 双向流心跳**：相比 HTTP 轮询，延迟更低、支持服务端推送配置更新
3. **模板化 MySQL 安装**：通过 Go 模板 + Profile 配置档案实现 MySQL 配置的灵活定制
4. **自动恢复冷却抑制**：避免对反复失败的 Agent 进行无意义的恢复尝试
5. **候选评分算法**：故障转移时综合数据新鲜度、Relay 状态、健康分数、选举优先级等多维度评分
6. **动态指标采集**：Manager 可通过心跳流实时调整 Agent 的采集配置，无需重启
