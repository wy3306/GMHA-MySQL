# cmd package

本目录包含 GMHA-MySQL 的交互层代码。

## 职责
*   处理用户交互（输入/输出）。
*   显示菜单导航。
*   调用 `internal/cli` 执行实际业务逻辑。

## 文件说明
*   **repl.go**: 实现了 Interactive Mode（交互模式）的主循环和子菜单逻辑。
