## Why

accountsvc 当前通过双监听器区分入口，并要求生产进程自行提供 TLS。使用网关管理访问范围或未来使用服务网格时，这会增加端口、证书和部署配置负担；需要将监听器简化为一个，同时明确 TLS 终止与入口隔离的责任，保留认证及领域安全规则。

## What Changes

- **BREAKING**：serve 仅使用 `ACCOUNTSVC_HTTP_ADDR` 一个监听地址，默认仍为 `127.0.0.1:8080`；消费者、管理员、内省和探针共用监听器。公开路由范围由部署侧网关与网络策略控制。
- **BREAKING**：内省路径改为 `POST /v1/introspect`；采用直接迁移，不保留 `/internal/v1/introspect` 别名或重定向。serve 遇到非空 `ACCOUNTSVC_INTERNAL_ADDR` 明确报错，避免静默忽略旧隔离配置。
- 新增 `ACCOUNTSVC_TLS_ENABLED`：未设置或为空时保持原模式行为；显式 true 强制配置证书；显式 false 使用 HTTP（包括 production），传输加密由部署层负责。false 与任一 HTTP TLS 证书配置同时存在时拒绝启动。
- 保留独立认证处理器、内省 Basic 凭据及轮换、消费者/管理员身份分离、HTTP 超时/体积限制、探针语义、迁移与有界关闭。
- 补充与具体网关产品无关的接入契约和部署检查项：来源 IP、路由暴露、TLS 回源/外部终止、请求频率与并发、凭据透传、重试和日志保护。通用入口限流由宿主网关承担。
- 同步远程鉴权示例、Compose、配置模板、测试与调用迁移说明；真实网关、服务网格和产品环境验证仍延期。

## Capabilities

### New Capabilities

- `gateway-integration`: 单监听器下网关、服务和嵌入式宿主的职责、传输部署约束及可验收的发布检查项。

### Modified Capabilities

- `accountsvc-runtime`: 单监听器、TLS 显式开关、旧配置拒绝、探针所在入口及关闭语义。
- `consumer-token-introspection`: 新内省路径和外部 TLS 终止部署方式，保留调用认证、响应格式、吊销故障与接入方授权契约。

## Impact

- 涉及 `internal/accountsvc` 配置、路由和生命周期装配及测试，`examples/remoteauth` 文档/示例和测试，`deploy/accountsvc` 模板，README、服务/兼容手册、发布检查表；新增 `docs/gateway-integration.md`。
- 不改变 accountkit 根 Config/Deps、库生命周期、领域 SQL、数据库结构、令牌格式或身份体系；不新增运行时依赖。
- 旧调用方需调整内省与探针地址；旧服务配置需删除 INTERNAL_ADDR，并在流量切换前部署网关路径策略。提案按尚未进行产品部署的现状选择直接迁移，具体步骤见 design.md。
- TLS 开关只作用于本服务的 HTTP 监听；SMTP、OIDC、PostgreSQL 和 Redis 连接策略分别保持原配置。

## Non-goals

- 不实现网关、服务网格、自动证书签发/续期、原生 mTLS 客户端证书认证或配置热加载。
- 不新增库或服务内通用请求限流、共享限流 Redis 状态、运维命令、OAuth 动态客户端注册或授权服务器发现端点。
- 不改变验证码额度、刷新宽限、Redis access 吊销 fail-open、审计异步失败、周期性密钥回填及业务匿名化责任。
