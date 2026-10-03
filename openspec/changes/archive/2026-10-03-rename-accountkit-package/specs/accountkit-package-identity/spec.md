## Purpose

统一 accountkit 的公共 Go 包名称和独立项目文档，使宿主能够按模块名称直接导入账号认证能力，并明确可选官方服务不构成库集成的运行依赖，保留已有认证与存储兼容性。

## ADDED Requirements

### Requirement: 统一公共包名称
根包 SHALL 命名为 `accountkit`，module 路径 SHALL 保持 `github.com/bbxx111/accountkit`。文档 SHALL 提供旧包标识符的迁移方式。

#### Scenario: 默认导入
- **WHEN** 宿主不指定别名地导入根模块
- **THEN** 能通过 `accountkit.Config`、`accountkit.Deps` 和 `accountkit.New` 使用已有 API

### Requirement: 独立文档与可选官方服务
文档 SHALL 使用本项目术语描述功能和验证基线，不包含其他产品仓库的名称、路径和依赖叙述。accountsvc SHALL 定位为基于本库的可选官方服务；宿主 SHALL 能继续直接嵌入库，且无需运行 accountsvc。本变更 SHALL 不将尚未实现的服务描述为已交付。

#### Scenario: 嵌入式集成
- **WHEN** 宿主选择直接集成库
- **THEN** 保留依赖注入、相对路由与生命周期契约，不新增远程服务依赖

### Requirement: 重命名保持运行兼容
重命名 SHALL 保留认证 HTTP 协议、环境配置键、持久化数据格式、冻结 SQL 与原始校验清单，以及跨版本维护任务锁标识。

#### Scenario: 已有实例升级
- **WHEN** 宿主调整 Go 包标识符后使用同一配置与存储
- **THEN** 无需数据库重建或修改凭证格式，维护锁仍与旧版本互斥
