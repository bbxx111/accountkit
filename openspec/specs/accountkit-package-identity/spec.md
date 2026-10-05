# accountkit-package-identity Specification

## Purpose

统一 accountkit 的公共 Go 包名称和独立项目文档，使宿主能够按模块名称直接导入账号认证能力，并明确可选官方服务不构成库集成的运行依赖，保留已有认证与存储兼容性。

## Requirements

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

重命名 SHALL 保留认证 HTTP 协议、环境配置键、持久化数据格式、冻结 SQL 与原始校验清单，以及跨版本维护任务锁标识。enduser 的门面 SHALL 继续返回相对路由；消费者与管理员身份体系、DTO、错误、scope/资源归属检查、审计及依赖失败语义 SHALL 保持独立且不变。accountsvc 的 `/v1`、`/admin/v1`、`/v1/introspect`、单监听器和TLS行为 SHALL 不因Go命名迁移改变。

#### Scenario: 已有实例升级
- **WHEN** 宿主调整 Go 包标识符、导入路径和门面调用后使用同一配置与存储
- **THEN** 无需数据库重建或修改凭证格式，维护锁仍与旧版本互斥

#### Scenario: HTTP 调用方不变
- **WHEN** HTTP客户端继续使用原URL、请求体、设备头和凭据访问嵌入式宿主或accountsvc
- **THEN** 登录、身份、会话、生命周期和错误响应保持既有契约，不能将 `/users` 改成 `/endusers` 或改变JWT字段

### Requirement: 终端用户 HTTP 包与门面命名

终端用户 HTTP 适配包 SHALL 使用导入路径 `github.com/bbxx111/accountkit/httpapi/enduser` 和 package enduser。根门面 SHALL 提供 `Auth.EndUserHandler() http.Handler`，不再提供 ConsumerHandler 方法。原 `httpapi/consumer` 导入路径 SHALL 被移除，不提供别名、兼容包或转发方法。迁移后的 Handler、Deps、Service、IdentityReplacer及构造/中间件方法 SHALL 保持原类型内容与签名，除引用的新包路径和门面方法名外不增加调用要求。

#### Scenario: 宿主采用新入口
- **WHEN** 宿主导入 enduser 包或调用 Auth.EndUserHandler
- **THEN** 在 GOWORK=off 下能够编译并按原方式挂载相对路由，无需运行 accountsvc

#### Scenario: 旧入口直接移除
- **WHEN** 宿主仍导入 httpapi/consumer 或调用 Auth.ConsumerHandler
- **THEN** 无法通过新版本编译，迁移说明明确要求修改源码，不存在静默兼容入口

#### Scenario: 自定义服务实现迁移
- **WHEN** 宿主将原 Service 和可选 IdentityReplacer 引用改为 enduser 包
- **THEN** 原接口实现无需新增方法；未提供换绑能力时仍按既有规则返回503，不因命名迁移扩大接口

### Requirement: 当前接入文档与测试门禁迁移

当前接入指南、代理项目指南和可编译示例 SHALL 使用 enduser 与 EndUserHandler 命名，并提供旧新 Go 调用对照。历史归档及已完成实施计划 SHALL 保留原事实，旧名称可在明确的历史说明和迁移表中出现。必需测试清单 SHALL 一对一更新移动后包路径，保留全部原有测试约束；如测试名称调整，SHALL 同步对应门禁，不通过删除条目或允许skip掩盖遗漏。

#### Scenario: 新旧调用对照
- **WHEN** 使用方查阅当前嵌入示例或兼容说明
- **THEN** 能找到新导入和门面调用，并明确HTTP客户端、环境配置和存储无需随之迁移

#### Scenario: 移动测试包后执行门禁
- **WHEN** 执行项目必需测试检查
- **THEN** 原终端用户HTTP包的全部必需用例以新路径被识别并实际通过，旧路径不作为仍有效的测试选择器保留
