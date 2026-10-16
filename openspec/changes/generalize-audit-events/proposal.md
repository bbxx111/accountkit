## Why

当前审计写入虽然允许宿主注入 Recorder，但事件模型、管理查询和匿名化仍围绕认证专属列与本地表耦合，宿主难以将认证和业务审计统一保存、检索及清理。采用稳定的通用事件模型并补齐外部读写边界，可让 accountkit 保留默认本地能力，同时成为宿主统一审计设施的事件生产方。

## What Changes

- 将 audit_event 收敛为 id、source、action、result、actor、targets、context、data、occur_time、create_time 十列；source/action 使用有业务长度限制的 VARCHAR(128)，JSONB 具有结构、容量和隐私校验。不设置 schema_version 或 ingest_time。
- **BREAKING**：审计事件 ID 改用生产方生成的 UUIDv7，数据库使用 UUID 主键，对外使用标准 UUID 字符串；其他 u_/i_/s_/f_ 资源格式不变。按用户最新指令直接修改原始 0001 数据库定义，不新增生产迁移、不提供旧审计表自动升级；旧审计游标明确失效。
- **BREAKING**：公开审计事件/读侧类型升级为通用结构，动作使用可扩展字符串；actor 通过 namespace + id 识别主体，管理员与消费者仍由独立身份系统认证。认证专属字段归入 data.accountkit。
- 保留默认本地有界异步模式；补充宿主查询适配器和显式的仅写入模式，外部写入不能静默搭配本地空结果。默认管理 HTTP 投影尽量保留旧字段，仅显式调整审计 ID/游标和未配置外部查询时的响应。
- 明确请求、流程、主体与目标的关联规则，保留账号审计查询、登录补全关联脱敏和本地保留期；外部审计清理由宿主管理，不宣称跨存储原子性或默认可靠投递。
- 将十列结构、约束和索引直接写入 0001，并同步更新当前固定校验；移除本分支先前新增的 0002 与 staging 工具。保留固定源快照及已发布标签原字节，继续验证认证、宿主集成、隐私、并发、重复投递和历史非审计格式。

## Capabilities

### New Capabilities

- `generic-audit-events`：十字段通用模型、UUIDv7、结构化主体/资源、长度及隐私边界、幂等和查询排序。
- `audit-host-integration`：本地及宿主接管模式、读侧装配、生命周期、失败观测和外部隐私清理责任。

### Modified Capabilities

- `embedded-auth-package`：明确公开审计模型、审计 ID 和游标的兼容迁移例外，保留管理授权、认证结果及非审计资源格式。
- `versioned-auth-storage`：原始 0001 直接定义通用审计结构，初始化版本保持 1；拒绝旧同号审计结构，历史合成数据验证不等于旧库原地升级。
- `migration-safety`：记录本次用户明确授权的初始定义调整及固定校验更新，保留完整历史和已发布标签检查、dirty 阻断、双锁与安全回滚契约。

## Impact

- 核心与领域：audit/、accountkit.go、config.go、enum/、user/ 的事件生成与清理查询；SQL 通过 sqlc 生成，不手改生成目录。
- HTTP 与装配：httpapi/admin 的读侧接口、DTO 投影及审计专用游标；internal/accountsvc 与 examples/embedded 的默认装配和宿主示例。
- 存储：migrations/0001_init 的审计定义、当前固定校验、baseline 结构核验、audit/query.sql 和 user/query.sql、相关索引；移除新增审计迁移和准备工具。
- 文档与验证：README、兼容/迁移/开发说明、必要的 AGENTS.md 审计 ID 规则修订，以及本变更 verification.md。不扩展为多数据库层、审计 SaaS、消息总线或内置远端网络服务商适配器。
