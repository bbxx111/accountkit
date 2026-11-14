# Design

## Context

动机见 [proposal.md](proposal.md)。基线为远端 develop `ac3eff1f076f52d4b58b8e43269939342c4a0416`，本地 develop 和任务分支已对齐。

当前 `internal/accountsvc/config.go` 保存两个 Basic 映射和两个 Bearer 名单，并拒绝跨接口复用秘密或 azp。`serviceauth/serviceauth.go` 已统一认证结果，`basicclients` 负责秘密匹配，`oidcjwt` 负责 Discovery/JWKS；`run.go` 的 serviceAuthenticators 为内省、purgefeed 分别装配。两个 handler 自行映射 OAuth/AIP 错误。已有 `config_service_token_test.go`、`run_service_token_test.go`、`service_token_integration_test.go` 和两个 handler 的 bearer_test 覆盖旧契约。

## Goals / Non-Goals

**Goals:** 同一服务身份可持有多项权限，两种认证方式共享授权；部署使用可独立注入的环境变量；已发布逐接口配置保持可运行。

**Non-Goals:** 不新增身份查询端点或家庭业务；不改消费者与管理员授权，不改内省的数据库吊销规则、清理 feed 数据或数据库；不引入多 issuer、热加载、运行时权限编辑或 JWT scope 授权。

## Decisions

### D1. 在已有能力中演进，保留两种互斥配置模式

选择复用 `accountsvc-service-authentication`，而非新增近似能力。统一模式由 `ACCOUNTSVC_SERVICE_CLIENTS` 的存在性（LookupEnv）选择，显式空/全空白值表示无服务客户端；未设置则使用旧模式。存在任意 `ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_*` 而未设置主列表是错误。

统一模式下，四项旧逐接口变量（INTROSPECTION_BASIC_CLIENTS、PURGE_FEED_BASIC_CLIENTS、INTROSPECTION_BEARER_CLIENTS、PURGE_FEED_BEARER_CLIENTS，均有 ACCOUNTSVC_ 前缀）只要非空白即拒绝，包含 `{}`、`[]`；空白占位视为未配置。SERVICE_ISSUER/AUDIENCE 是共享参数，不算旧模式冲突。旧模式继续使用原解析器及原逐接口认证路径，原有两名单不相交、秘密不复用、HTTP 错误全部保持。弃用警告只输出旧变量名，每进程启动一次；不设置旧模式移除期限。

不把旧映射自动合并成注册表：旧配置允许相同用户名在不同接口使用不同秘密，合并可能扩大权限。部署者迁移时须明确合并为同一身份并授予权限，或保留不同客户端 ID。

### D2. 分项环境变量及严格输入契约

所有字段前缀为 `ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_`：ID、AUTH_METHODS、PERMISSIONS、BASIC_SECRETS。主列表为别名的逗号列表；别名匹配 `[A-Z][A-Z0-9_]{0,31}`，ID 为 1–256 字节、无冒号/空白/控制字符。别名与 ID 各自唯一；生成每个完整预期变量名后逐一读取，并检查该前缀下未知字段或未声明别名，避免下划线切分歧义。

列表元素只裁剪两端空白，拒绝空元素和重复元素；主列表空值的特殊语义见 D1，已声明客户端的认证方式和权限列表不得为空。AUTH_METHODS 只接受小写 basic、bearer；本次权限只接受 tokens.introspect、purged_users.read，拒绝通配符及尚未实现的 identities.resolve。权限名大小写敏感。

启用 Basic 必须提供 BASIC_SECRETS：逗号分隔、每项为规范标准 Base64 且解码至少 32 字节，原 Base64 文本作为密码。拒绝重复秘密及跨客户端相同秘密；同一客户端的秘密自然适用于其全部权限。未启用 Basic 时非空 BASIC_SECRETS 是错误。Basic 秘密与 IdP client secret 是不同材料，后者仅由调用方与 IdP 持有。

有 Bearer 客户端才要求统一 SERVICE_ISSUER/AUDIENCE；无 Bearer 时非空 issuer/audience 为半配置错误。保留基线中的 URL、开发 HTTP 例外、issuer/audience 隔离规则，audience 不得等于任一已启用 Bearer 客户端的 ID。migrate 在读取服务配置前返回，不解析任何新增变量。

JSON 总对象与索引编号方案均不采用：前者难以独立注入秘密，后者调整顺序会改变变量名。别名模式在变量数量与可维护性之间取折中。

### D3. 认证与能力授权分开，统一模式共享一个不可变注册表

注册表按 ID 存储方法集合、权限集合及固定长度秘密摘要；启动时深拷贝，不保留可被外部修改的集合。复用 basicclients 的常数时间比较与轮换遍历方式、oidcjwt 的验证和缓存，不把明文秘密写入日志。

为两个接口分别声明 RequiredPermission：tokens.introspect 和 purged_users.read。统一认证顺序：接口是否配置 → 单一 Authorization → 凭据验证 → 客户端允许该认证方式 → 必需权限 → 原有请求解析和业务处理。接口配置定义为注册表中至少有一个客户端被授予该权限；无人获授权时先返回原接口的 NOT_CONFIGURED 503。

Basic 的未知 ID/错误秘密返回 401。有效 Bearer 的未知 azp 返回 403，延续“有效服务令牌但未获准”的含义；已注册客户端使用未启用的方法返回 401。认证通过但缺权限，两种方式均为 403。JWT scope、角色及请求体中的 client_id 均不参与授予权限。

Bearer 校验保持 RS256、kid、禁止 jku/x5u、16 KiB 上限、精确 issuer、目标 audience、exp/iat、30 秒偏差及最多一小时寿命。统一 issuer 的 verifier 在实例内共享；原 10 秒刷新节流/并发合并、一小时缓存、认证等待最多 3 秒及失败分类保持。不得在 Bearer 失败时自动回退 Basic。

### D4. HTTP 错误和挑战

| 情形 | 内省 OAuth error | 清理拉取 AIP reason |
|---|---|---|
| 未配置该能力 | 503 introspection_not_configured | 503 PURGE_FEED_NOT_CONFIGURED |
| 无凭据、多头、未知方案、Basic 错误、客户端方法未启用 | 401 invalid_client | 401 INVALID_CLIENT |
| Bearer 签名/claims 无效 | 401 invalid_token | 401 TOKEN_INVALID |
| 有效 Bearer 未登记，或已认证客户端缺权限 | 403 insufficient_scope | 403 CLIENT_NOT_AUTHORIZED |
| Discovery/JWKS 不可用 | 503 temporarily_unavailable | 503 DEPENDENCY_UNAVAILABLE + Retry-After: 1 |

统一模式的 401 挑战列出整个注册表启用方法的并集，使用原接口 realm，不按不可信用户名或 azp 改变挑战；无有效 Bearer 尝试不附 invalid_token。有效 Basic 但缺权限的 403 无 WWW-Authenticate；Bearer 的 403 保持 Bearer insufficient_scope；503 不带挑战。所有结果 no-store。旧模式挑战完全保持现状。

### D5. 运行时、部署与扩展

服务 issuer 短暂故障不阻止监听或影响 readiness，Bearer 请求按缓存/故障规则判定，Basic 独立工作；管理员 verifier 的启动要求保持。客户端禁用方法、撤销权限或删除身份须更新配置并重启所有实例；提供方禁用客户端不保证已签发令牌即时失效，最多接受至原 token 有效期/偏差边界。每个请求读取当前进程快照，不把权限放入可长期缓存的认证结果。

新增内部能力时必须在代码权限目录注册、声明端点所需权限并增加协议测试；这次只注册两个已存在能力。JWKS、探针、消费者和管理员路由不加入服务权限目录。部署使用只开启 service accounts 的专用 OIDC 客户端，不把登录客户端 ID 登记为服务客户端。

## Risks / Trade-offs

- [共享凭据被泄露时影响该身份所有权限] → 只授所需权限，允许部署者按职责拆分客户端；保留轮换与显式方法开关。
- [新旧映射自动合并导致权限扩张] → 模式互斥、旧模式原样保留、迁移前逐客户端核对授权。
- [配置拼写错误静默禁用或授权] → 检查声明列表、孤立字段、重复 ID 和已注册权限；错误只输出变量名。
- [滚动重启时策略短暂不一致] → 权限撤销或合并客户端时，部署方排空旧实例后再启用新凭据；不承诺热更新。
- [兼容路径增加维护量] → 明确覆盖旧模式回归；未来移除另开破坏性变更。

## Migration Plan

1. 部署包含兼容路径的新版本，保持仅旧配置；验证内省和 feed 的既有权限、错误和故障语义。
2. 准备统一环境变量，按真实职责分配权限，Basic 与 OIDC secret 分别注入。删除四项旧变量的非空配置，保持 issuer/audience 一致。
3. 在隔离环境验证双方法、跨能力允许/拒绝、JWKS 不可用、秘密轮换、未配置接口及无敏感日志，再切换生产配置并重启全部实例。与网络策略一起保持内部入口不对公网暴露。
4. 回退时恢复已保存的旧配置与旧客户端身份，再回退二进制；新统一身份若原来跨接口共用，回退前重新准备原独立凭据和调用方配置。无 DB 迁移或清库操作。

## Verification Approach

实施核对补充：runtime 主规格另有“已清理用户拉取凭据”条目独立描述旧名单互斥；对应 delta 同步明确该条只适用于旧模式，以保持 D1/D3 在归档合并后的规格一致。此补充不改变已确认的模式选择、权限或协议设计。

表驱动配置解析/认证矩阵测试覆盖所有枚举、空值、重复、孤立字段、模式混用、共享秘密及 migrate 隔离。使用现有 oidcjwttest 隔离提供方验证 issuer/audience/kid/时间、提供方故障与缓存。两个 handler 以相同客户端/凭据调用两个已授权端点，并验证撤去一个权限、关闭一个方法后的拒绝结果及零业务调用。真实 PostgreSQL/Redis 服务集成保留内省的数据库吊销与 feed 分页、清理事务覆盖。纯工件阶段不运行这些产品测试；实施阶段执行仓库规定的 vet、测试和完整验收入口。
