# internal/store package

本目录包含 GMHA-MySQL 的数据持久化层代码。

## 职责
*   封装底层数据库操作。
*   提供对 Cluster, Host, Instance 等资源的高级 CRUD 接口。

## 技术栈
*   使用 **bbolt** (Go 原生嵌入式 KV 数据库) 进行存储。

## 文件说明
*   **store.go**: 定义数据模型 (Structs) 和所有的数据库操作方法。
