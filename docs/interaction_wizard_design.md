# 引导式交互 (Wizard) 逻辑设计说明

本文档旨在说明 `internal/interaction/wizard` 模块的设计思路与实现细节，方便开发者理解流程并进行定制化修改。

## 1. 模块定位

**Wizard (向导)** 模块是 GMHA-MySQL 三种交互模式之一（另外两种是 Web 和 CLI）。
它的核心目标是：**通过问答式的终端交互，引导用户完成复杂的配置任务（如创建集群），降低上手门槛。**

该模块**不包含业务逻辑**，只负责：
1.  **收集**用户输入（通过终端 stdin）。
2.  **组装**数据结构（DTO）。
3.  **调用**内核 Service 层执行实际操作。

## 2. 代码结构

相关代码主要分布在以下两个位置：

*   **交互逻辑层**: `internal/interaction/wizard/`
    *   [`handler.go`](../../internal/interaction/wizard/handler.go): 包含主循环 `Run()` 和具体流程函数（如 `addClusterFlow`）。这是您主要需要修改的地方。
*   **UI 工具库**: `internal/ui/`
    *   [`prompt.go`](../../internal/ui/prompt.go): 封装了底层的终端输入读取逻辑（`Ask`, `AskBool`），屏蔽了换行符处理等细节。

## 3. 核心流程设计

整个交互流程是一个死循环的状态机，直到用户选择退出。

### 3.1 主入口 `Run()`
位于 `internal/interaction/wizard/handler.go`。

```go
func Run(svc service.ClusterService) {
    // 1. 初始化输入读取器
    prompter := ui.NewPrompter()
    
    // 2. 进入主菜单循环
    for {
        // 打印菜单
        // 读取用户选择 (1, 2, q)
        // 根据选择进入子流程 (addClusterFlow / listClustersFlow)
    }
}
```

### 3.2 添加集群流程 `addClusterFlow()`

这是一个典型的线性引导流程，分为三个阶段：

1.  **基础信息收集 (Step 1)**
    *   调用 `p.Ask()` 获取集群名称、VIP 等。
    *   使用 `service.CreateClusterInput` 结构体暂存数据。
2.  **关联机器收集 (Step 2)**
    *   进入一个子循环 (`for`)。
    *   询问 "Add a machine?"。
    *   如果是，则收集机器 IP、端口、账号密码。
    *   将机器信息 `append` 到 `input.Machines` 切片中。
3.  **确认与提交 (Step 3)**
    *   打印所有已收集的信息概览。
    *   调用 `p.AskBool("Confirm create?")` 进行最终确认。
    *   **关键点**：确认无误后，调用 `svc.CreateCluster(ctx, input)` 将数据提交给内核层。

## 4. 如何进行修改

如果您需要调整交互逻辑，请参考以下场景：

### 场景 A：添加一个新的输入项（例如：数据库版本）

1.  **修改数据结构**：
    首先在 `internal/service/cluster.go` 的 `CreateClusterInput` 中添加字段：
    ```go
    type CreateClusterInput struct {
        // ...
        DBVersion string // 新增字段
    }
    ```

2.  **修改交互流程**：
    在 `internal/interaction/wizard/handler.go` 的 `addClusterFlow` 函数中添加询问逻辑：
    ```go
    // 在 Step 1 中添加
    input.DBVersion = p.Ask("MySQL Version", "8.0.26")
    ```

3.  **修改内核逻辑**：
    在 `internal/service/cluster.go` 的 `CreateCluster` 方法中处理这个新字段（例如存入数据库）。

### 场景 B：修改提示文案或默认值

直接修改 `internal/interaction/wizard/handler.go` 中的 `p.Ask` 调用参数即可。
例如将默认端口改为 2222：
```go
portStr := p.Ask("SSH Port", "2222")
```

### 场景 C：增加新的校验逻辑

在 `p.Ask` 获取输入后，立即添加 `if` 判断。如果校验失败，可以使用 `continue` 跳过后续步骤或重新询问（通常配合 `for` 循环实现重试机制）。

```go
for {
    ip := p.Ask("Machine IP", "")
    if isValidIP(ip) { // 假设您实现了一个校验函数
        m.IP = ip
        break
    }
    fmt.Println("Invalid IP format, please try again.")
}
```

## 5. UI 工具库扩展

如果您觉得现有的 `Ask` (文本) 和 `AskBool` (布尔) 不够用，可以在 `internal/ui/prompt.go` 中扩展新的方法，例如：
*   `AskPassword()`: 输入时隐藏字符（使用 `golang.org/x/term` 等库）。
*   `AskSelect(options []string)`: 提供列表供用户选择（如方向键选择）。

---
*文档生成时间：2026-02-02*
