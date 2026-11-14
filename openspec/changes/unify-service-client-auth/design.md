# Design

## Context

动机见 [proposal.md](proposal.md)。初始规划基线为 develop `ac3eff1f076f52d4b58b8e43269939342c4a0416`；本轮配置收敛以已提交的首次实现 `b25ec98` 为代码基线，在原任务分支继续修改。

初始基线 `internal/accountsvc/config.go` 保存两个 Basic 映射和两个 Bearer 名单，并拒绝跨接口复用秘密或 azp。初次实现 `b25ec98` 已加入统一注册表，但保留旧模式、模式选择与额外 Compose override。用户随后明确要求“去掉旧配置模式，整合配置文件”，本次在同一 change 内修订最终设计；此前保留旧模式的决策被本设计取代，不再作为当前兼容目标。原 18 项任务及 verification.md 只证明 `b25ec98` 阶段，不能视为本轮验收。

## Goals / Non-Goals

**Goals:** 同一服务身份可持有多项权限，Basic/Bearer 共享授权；部署只用统一客户端注册表与一份 Compose 文件；旧逐接口配置明确拒绝，迁移由部署方显式转换身份与权限。

**Non-Goals:** 不新增身份查询端点或家庭业务；不改消费者与管理员授权，不改内省的数据库吊销规则、清理 feed 数据或数据库；不引入多 issuer、热加载、运行时权限编辑或 JWT scope 授权。

## Decisions

### D1. 在已有能力中演进，只保留统一注册表

选择复用 `accountsvc-service-authentication`，而非新增近似能力。serve 始终构造非 nil 注册表；`ACCOUNTSVC_SERVICE_CLIENTS` 未设置、显式空或全空白均表示无服务客户端。不存在模式选择或旧路径回退。存在任意未声明的 `ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_*` 字段仍是错误，包括主列表缺失或为空的情况。

四项旧逐接口变量（INTROSPECTION_BASIC_CLIENTS、PURGE_FEED_BASIC_CLIENTS、INTROSPECTION_BEARER_CLIENTS、PURGE_FEED_BEARER_CLIENTS，均有 ACCOUNTSVC_ 前缀）只要非空白即在监听前拒绝，包含 `{}`、`[]`，无论是否设置新列表；空白占位忽略。错误只指出变量名及配置已移除的固定原因，不回显值，不输出弃用警告。SERVICE_ISSUER/AUDIENCE 是现行 Bearer 信任参数，无 Bearer 客户端时配置它们仍是半配置错误。migrate 不解析这些新旧服务运行变量。

不把旧映射自动合并成注册表：旧配置允许相同用户名在不同接口使用不同秘密，合并可能扩大权限。部署者迁移时须明确合并为同一身份并授予权限，或保留不同客户端 ID。

Config 删除旧 Basic 映射、Bearer 名单与模式标识；serviceauth 只使用 Registry、RequiredPermission 与 BearerVerifier，不保留 nil Registry 触发的旧认证分支。若保留 Basic-only 便利构造，须转换成 ClientConfig、构造 Registry 并进入相同授权路径。删除失去生产调用者的旧 JSON 解析及跨接口 SharedSecret 辅助；Matcher、轮换与固定长度摘要常数时间比较保持。

### D2. 分项环境变量及严格输入契约

所有字段前缀为 `ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_`：ID、AUTH_METHODS、PERMISSIONS、BASIC_SECRETS。主列表为别名的逗号列表；别名匹配 `[A-Z][A-Z0-9_]{0,31}`，ID 为 1–256 字节、无冒号/空白/控制字符。别名与 ID 各自唯一；生成每个完整预期变量名后逐一读取，并检查该前缀下未知字段或未声明别名，避免下划线切分歧义。

列表元素只裁剪两端空白，拒绝空元素和重复元素；主列表空值的特殊语义见 D1，已声明客户端的认证方式和权限列表不得为空。AUTH_METHODS 只接受小写 basic、bearer；本次权限只接受 tokens.introspect、purged_users.read，拒绝通配符及尚未实现的 identities.resolve。权限名大小写敏感。

启用 Basic 必须提供 BASIC_SECRETS：逗号分隔、每项为规范标准 Base64 且解码至少 32 字节，原 Base64 文本作为密码。拒绝重复秘密及跨客户端相同秘密；同一客户端的秘密自然适用于其全部权限。未启用 Basic 时非空 BASIC_SECRETS 是错误。Basic 秘密与 IdP client secret 是不同材料，后者仅由调用方与 IdP 持有。

有 Bearer 客户端才要求统一 SERVICE_ISSUER/AUDIENCE；无 Bearer 时非空 issuer/audience 为半配置错误。保留基线中的 URL、开发 HTTP 例外、issuer/audience 隔离规则，audience 不得等于任一已启用 Bearer 客户端的 ID。migrate 在读取服务配置前返回，不解析任何新增变量。

JSON 总对象与索引编号方案均不采用：前者难以独立注入秘密，后者调整顺序会改变变量名。别名模式在变量数量与可维护性之间取折中。

### D3. 认证与能力授权分开，共享一个不可变注册表

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

401 挑战列出整个注册表启用方法的并集，使用原接口 realm，不按不可信用户名或 azp 改变挑战；无有效 Bearer 尝试不附 invalid_token。有效 Basic 但缺权限的 403 无 WWW-Authenticate；Bearer 的 403 保持 Bearer insufficient_scope；503 不带挑战。所有结果 no-store。

### D5. 运行时、部署与扩展

服务 issuer 短暂故障不阻止监听或影响 readiness，Bearer 请求按缓存/故障规则判定，Basic 独立工作；管理员 verifier 的启动要求保持。客户端禁用方法、撤销权限或删除身份须更新配置并重启所有实例；提供方禁用客户端不保证已签发令牌即时失效，最多接受至原 token 有效期/偏差边界。每个请求读取当前进程快照，不把权限放入可长期缓存的认证结果。

新增内部能力时必须在代码权限目录注册、声明端点所需权限并增加协议测试；这次只注册两个已存在能力。JWKS、探针、消费者和管理员路由不加入服务权限目录。部署使用只开启 service accounts 的专用 OIDC 客户端，不把登录客户端 ID 登记为服务客户端。

部署只保留 `deploy/accountsvc/compose.yaml`。accountsvc 直接声明 `env_file: ./.env` 以注入任意已声明动态别名；删除 `compose.service-clients.yaml` 及基础 environment 的四项旧变量默认映射。文档标准命令同时使用 `--env-file deploy/accountsvc/.env`，与服务 env_file 指向同一文件：前者用于 Compose 插值，后者用于容器环境注入，不能把前者误写为后者的替代。模板默认空注册表，并提供 Basic-only、Bearer-only、双方式及轮换示例。

## Risks / Trade-offs

- [共享凭据被泄露时影响该身份所有权限] → 只授所需权限，允许部署者按职责拆分客户端；保留轮换与显式方法开关。
- [旧映射自动合并导致权限扩张] → 拒绝旧变量，迁移前逐客户端核对新身份与权限，不自动合并。
- [配置拼写错误静默禁用或授权] → 检查声明列表、孤立字段、重复 ID 和已注册权限；错误只输出变量名。
- [滚动重启时策略短暂不一致] → 权限撤销或合并客户端时，部署方排空旧实例后再启用新凭据；不承诺热更新。
- [旧部署升级失败] → 按用户要求明确做配置破坏性变更；监听前指出移除变量，提供显式迁移与旧二进制回退说明。

## Migration Plan

1. 在升级前盘点旧客户端、逐接口权限及调用方凭据，显式转换为统一别名/ID/方法/权限；相同旧用户名不自动等同为同一新身份。Basic 与 OIDC secret 分别注入。
2. 删除四项旧变量的非空配置，只使用新的注册表配置及一份 Compose 文件；无服务客户端时留空或省略 SERVICE_CLIENTS，不残留孤立字段或 issuer/audience。
3. 在隔离环境验证仅旧/混合旧配置均被拒绝、缺省与显式空注册表、双方法、跨能力允许/拒绝、JWKS 不可用、秘密轮换及无敏感日志，再切换生产配置并重启全部实例。与网络策略一起保持内部入口不对公网暴露。
4. 如需恢复旧配置模式，必须同时回退支持该模式的旧二进制、已保存的匹配配置与调用方凭据；当前二进制不能通过取消 SERVICE_CLIENTS 恢复旧模式。新统一身份若跨接口共用，回退前重新准备原独立凭据。无 DB 迁移或清库操作。

## Verification Approach

runtime 主规格的“已清理用户拉取凭据”和服务认证主规格的旧标题仍以精确标题列入 MODIFIED，由注册表和权限取代旧逐接口名单/凭据限制，避免将旧行为残留为当前要求。未修改的 Bearer 校验、缓存/故障分类、启动与可观测性要求和场景继续生效。

表驱动配置解析/认证矩阵测试覆盖所有枚举、缺省/空值、重复、孤立字段、仅旧/混合旧配置拒绝（含 []/{}）、空白旧占位、共享秘密及 migrate 忽略非法新旧运行配置。迁移全部活跃测试夹具与示例，不放宽安全断言；原跨接口 Basic 401 因授权模型变化改为有效凭据缺权限 403。删除旧契约测试时在两份必需清单中明确替代映射，并登记新拒绝/空注册表用例。使用现有 oidcjwttest 隔离提供方验证 issuer/audience/kid/时间、提供方故障与缓存。两个 handler 以相同客户端/凭据调用两个已授权端点，并验证撤去一个权限、关闭一个方法后的拒绝结果及零业务调用。真实 PostgreSQL/Redis 服务集成保留内省的数据库吊销与 feed 分页、清理事务覆盖；真实 Compose config 核对默认空、三种方法组合与动态下划线别名注入。纯工件阶段不运行产品测试；实施阶段执行仓库规定的 vet、测试和完整验收入口，新增证据与 `b25ec98` 历史记录分开。
