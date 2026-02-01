# internal/cli package

本目录包含 GMHA-MySQL 的核心业务逻辑和命令行处理代码。

## 职责
*   解析用户命令和参数。
*   执行具体的业务操作（如添加集群、纳管主机）。
*   生成和展示数据（如集群拓扑）。

## 文件说明
*   **commands.go**: 定义了所有支持的命令及其执行逻辑（Controller 层）。
*   **oneliner.go**: 实现了 One-Liner Mode（单行命令模式）的入口。
*   **parse.go**: 负责解析命令行参数、Flags 和交互式输入。
*   **topology.go**: 负责将集群数据格式化为可视化的拓扑结构并打印。
