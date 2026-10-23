# AGENTS.md — accountkit

本文件是本仓库所有编码代理的统一项目指南。`CLAUDE.md` 只引用本文，不维护第二份规范。沟通与项目文档默认使用中文；代码标识符遵循 Go 和既有协议约定。

本文定义 accountkit 的 API、领域建模、独立集成及开发规范。用户在当前任务中的明确要求优先。

## 项目定位与边界

- accountkit 是可嵌入宿主服务的 C 端账号认证 Go 包，模块为 `github.com/bbxx111/accountkit`，根 Go package 为 `accountkit`。
- 首版包含验证码登录、多身份与微信/Apple 登录、JWT/刷新会话、重新认证、身份绑定、注销/恢复/匿名化、管理接口、审计和维护任务。功能对照见 [docs/compatibility.md](docs/compatibility.md)。
- 单一 Go module，构建必须可在 `GOWORK=off` 下完成，不依赖外部工作区、提交的本地 replace、宿主业务表或部署环境。
- 当前存储为 PostgreSQL + Redis。库不监听端口，不管理网关，不内置管理员账号系统，不强制 Keycloak，也不承担短信/邮件服务商装配。不要擅自扩展为前端项目或多数据库适配层。
- accountsvc 是本项目自带的可选服务实现，基于 accountkit 装配运行；库不依赖该服务，其他项目仍可直接嵌入库。入口 `cmd/accountsvc`，装配在 `internal/accountsvc`；现有 `examples/embedded` 保留为开发宿主示例。运行边界见 [docs/accountsvc.md](docs/accountsvc.md)。
- 宿主负责连接池、Redis 客户端、发送器、可信代理/IP 解析、请求 ID、管理员身份验证以及业务匿名化回调。配置与接口细节以 [README.md](README.md) 和公开 Go 类型为准。

## 技术栈与目录

当前工具版本：Go 1.26.5（以 go.mod 为准）、sqlc 1.31.1、PostgreSQL 17。使用 Chi、pgx/v5、golang-migrate、go-redis/v9；Redis 测试采用 miniredis。CI 为 GitHub Actions，入口是 `.github/workflows/ci.yml`。

| 路径 | 职责 |
|---|---|
| `accountkit.go`、`config.go` | 对外门面、依赖注入、配置及生命周期 |
| `user/` | 账号领域规则、事务、状态机与仓储 |
| `httpapi/enduser/`、`httpapi/admin/` | 两个独立 HTTP 面及各自 DTO |
| `httpapi/authn/`、`httpapi/apierror/` | 认证中间件、协议错误映射 |
| `user/code/`、`user/idp/`、`user/sender/` | 验证码、第三方身份验证、发送器契约 |
| `tokens/`、`pii/`、`ids/`、`enum/` | 令牌、隐私数据、资源 ID、枚举 |
| `session/grace/`、`session/revocation/` | 刷新宽限缓存与吊销集 |
| `audit/`、`maintenance/`、`anonymize/` | 审计、维护任务、宿主匿名化契约 |
| `migrations/` | 嵌入 SQL、迁移锁、开发测试清库入口 |
| `user/query.sql`、`audit/query.sql` | 手写查询；生成结果分别进入 `user/db/`、`audit/db/` |
| `internal/migrationcheck/`、`internal/testgate/` | 发布检查工具，不是宿主公共接口 |
| `examples/embedded/` | 可独立编译的开发宿主示例 |
| `cmd/accountsvc/`、`internal/accountsvc/` | 可选服务入口、配置、生命周期、远程内省及外部管理员认证装配 |
| `user/sender/smtp/`、`examples/remoteauth/` | 可选 SMTP 发送器、业务服务远程鉴权示例 |
| `deploy/accountsvc/` | 服务镜像、仅本地开发 Compose 与无秘密配置模板 |
| `tests/testdata/source-baseline/` | 固定源清单、原始 SQL 和合成数据格式 |
| `scripts/`、`docs/`、`openspec/` | 验证入口、操作手册、规格与变更 |

## 架构与宿主契约

- 生命周期固定为 `Config/Deps → New → Migrate → Start → Close`。`New` 校验并装配，不做 I/O；启动前执行 `Migrate`，关闭后不再运行维护任务。宿主创建的 Pool/Redis 由宿主管理和关闭。
- HTTP handler 只做协议转换；业务规则、事务和状态约束集中在 `user.Service`，由 C 端、管理面和维护任务复用。不要为管理路径绕开领域约束，也不要添加无业务意义的透传 service 层。
- SQL 放在领域查询文件，经 sqlc 生成。禁止手改 `user/db/`、`audit/db/`；修改查询、schema 或类型映射后运行 `sqlc generate` 并检查生成差异。
- 配置通过 `Config` 或 `ConfigFromEnv(prefix)` 注入；环境变量前缀由宿主选择，示例使用 `ACCOUNTKIT_`。不要硬编码 `SERVER_`、产品域名、业务 schema 或产品密钥。
- `EndUserHandler()` 与 `AdminHandler()` 返回相对路由，由宿主分别挂载，示例为 `/v1` 和 `/admin/v1`。直接适配器使用 `httpapi/enduser`；旧 Go 入口迁移见 [兼容指南](docs/compatibility.md#终端用户-http-入口迁移)。包内不重复写死前缀，也不引入 `/api`。
- `AdminVerifier` 与 `AdminPrincipal` 必须成对提供；均未配置时管理面返回 `503 ADMIN_NOT_CONFIGURED`，普通消费者认证仍可用。管理员认证由宿主前置完成，包内继续执行每条管理路由的角色检查。
- 宿主通过 `Deps.ClientIP` 提供经过可信代理处理的地址，通过 `Deps.RequestID` 关联日志与审计。不能在库内无条件信任客户端的转发头。
- 匿名化回调使用传入的同一个 `pgx.Tx`；不得自行提交/回滚或另开连接写同批数据。认证表和业务表必须同库，任一回调失败使整个匿名化事务回滚；跨库事务不在契约内。宿主负责其 user_id 关联表的覆盖性检查。

## API 设计

### 标准优先级

适用于具体问题的行业标准优先，其次 Google API Design Guide/AIPs，最后才是本地偏好。令牌响应与错误遵循 RFC 6749，Bearer 认证遵循 RFC 6750，撤销遵循 RFC 7009，JWT claims 遵循 RFC 7519。不能改名 `access_token`、`refresh_token`、`token_type`、`expires_in` 等协议字段；也不要把完整授权码流程套到第一方验证码登录上。

- 资源导向：标准 Get/List/Create/Update/Delete 对应 GET/GET/POST/PATCH/DELETE；标准方法无法表达的操作使用 `:verb`，如 `POST /users/me/sessions:revokeOthers`。有副作用的自定义操作使用 POST。
- 集合名复数，资源名分层；时间字段统一为 `<verb>_time`；生命周期字段为 `state`，状态迁移走明确的领域方法。JWT 标准字段和第三方库字段不为本地命名偏好改名。
- 枚举在 API 中使用 UPPER_SNAKE_CASE 字符串，非法值返回 400；零值 UNSPECIFIED 不存储、不作为有效领域状态输出。
- 业务错误体沿用 `httpapi/apierror`：`{"error":{"code","message","status",...}}`，`code` 对应 HTTP 状态，`status` 为 google.rpc.Code 名称。`FAILED_PRECONDITION` 映射 400；token 端点保留 OAuth 错误格式；Bearer 401 带 `WWW-Authenticate`。
- 错误验证码为 400，权限/作用域不足为 403，依赖不可用为 503，不要一律转成 401。401 表示请求的认证凭据无效或缺失，可能触发客户端刷新/退出流程。现有领域错误及具体端点映射保持兼容。
- 500 对外返回固定消息，内部记录真实错误；禁止把 `err.Error()`、密钥或完整隐私数据写入响应。
- 设备元信息沿用 `X-Device-Id`、`X-Device-Name`，不改成业务请求体字段。

### 两个身份系统、两个 HTTP 面

- C 端账号与管理员身份分离，不能用一个共享 handler 加角色判断代替两套路由边界。管理员角色属于管理员系统内部授权，不是给消费者加一个 admin 标志。
- 消费者 JWT 的 `sub` 是 `u_…`，`sid` 是 `s_…`；不使用 `users/{id}` 资源名作为 sub。`aud` 表示令牌接收服务，不表示用户/管理员身份类型。`iss`、`aud`、密钥及作用域校验均须保留。
- C 端权限范围下沉到 SQL 的 user_id 条件；格式错误的 ID 返回 400，合法但不存在或属于其他用户的资源返回 404，不暴露资源存在性。
- 每个面使用明确 DTO，禁止直接序列化 sqlc 行，也不共享一个按角色删字段的 DTO。摘要、密文、密钥版本等内部字段不得泄露；管理面身份默认掩码，明文仅经授权且带审计的 `:reveal` 返回。
- 管理写操作仍经过领域方法；列表沿用既有 filter、show_deleted 和游标协议。角色拒绝审计通过宿主接入 `RecordAdminForbidden`，不要移除该契约。

## 数据库设计与实例隔离

### 命名与实例边界

- 包拥有 `user_account`、`identity`、`session`、`audit_event`、`sign_in_completion_flow` 和 `schema_migrations`，位于 `Config.Schema` 指定的 schema，默认 `account`。使用无业务前缀的单数 snake_case 表名；隔离靠 schema，不添加产品专用表前缀。
- 同一数据库可有多个独立实例。宿主使用 `PoolConfig` 设置 search_path，并独立配置 schema、Redis `KeyPrefix`（以 `:` 结尾）、JWT issuer/audience 和密钥。新增 Redis 键必须包含实例前缀。
- 索引与约束名称包含表名；重命名表时检查关联索引和约束名称，不能假定会自动同步。新增表与查询沿用现有领域归属和 sqlc 配置，不引入产品表前缀或第二套数据访问方式。
- 独立创建的普通索引命名为 `idx_<表名>_<字段或用途>`，`CREATE UNIQUE INDEX` 唯一索引命名为 `uk_<表名>_<字段或用途>`，不使用 `_idx`、`_uidx` 后缀；例如 `idx_identity_user_id`、`uk_identity_kind_subject_digest`。复合索引字段按索引列顺序命名；表达式、GIN 或部分索引可使用清晰的用途名，例如 `idx_audit_event_related_resources`，唯一性仍决定使用 `idx_` 还是 `uk_`。
- 主键索引沿用主键约束名（如 `<表名>_pkey`）；UNIQUE 约束及其所属索引使用 `uk_<表名>_<字段或用途>`，不额外创建重复的唯一索引。CHECK 保留明确的语义名称。名称不得超过 PostgreSQL 的 63 字节标识符上限，长名须明确缩写，不能依赖数据库自动截断。

### 主键、引用与枚举

- 账号、身份、会话与流程主键使用 `ids` 的类型前缀 TSID：`u_`、`i_`、`s_`、`f_` 加 13 位小写 Crockford base32；TEXT/COLLATE C 和格式 CHECK 保持一致。通用审计采用原生 UUIDv7，保留 variant/version CHECK；旧 e_ 仅用于历史格式追溯。ID 只承诺相等查找；分页排序必须有时间字段和唯一的次级排序键。
- 资源 ID 直接作为主键，不另加自增内部 ID 或 `public_id` 双轨映射。TSID 由时间戳与加密随机数组成，不泄露全局递增计数，但不保证无碰撞；主键唯一约束与调用方有界重试共同兜底。事务内发生唯一冲突后须回滚并整体重试，不能在已失败事务内继续写入；身份占用等业务唯一冲突仍按既有领域错误处理。
- 凭证不是资源 ID。验证码、refresh token 等沿用专用随机生成与校验逻辑，不能使用 TSID 代替随机秘密。
- PostgreSQL 同库可以跨 schema 查询、join、事务及定义外键；本包不使用外键是领域设计选择，不是数据库不支持跨 schema。不要添加 `REFERENCES` 或级联删除；引用一致性由领域写路径和测试保证。单表格式、结构 CHECK 与唯一约束不属于外键，仍须保留。
- 无外键不等于省略引用校验。创建或变更引用时，领域方法须检查目标存在性、所属用户及生命周期；涉及多行不变量的校验和写入放在同一事务内，并沿用相应行锁与锁序，防止并发绕过。管理面、导入和维护任务同样遵守；手工修数没有外键兜底，必须单独核验引用完整性。
- 枚举存 SMALLINT，Go 类型集中在 int16 底型的叶子包 `enum`，sqlc 使用类型 override，避免生成代码与领域包形成循环依赖。合法值由 Go 枚举唯一定义，不新增枚举值 CHECK 或 PostgreSQL ENUM；新增值不应仅因数据库枚举定义而要求迁移。
- 例外：`IdentityKind` 的类型集合可由部署配置扩展（OIDC 提供方），因此为 string 底型，`identity.kind` 存 `VARCHAR(32) COLLATE "C"` 加格式 CHECK（`^[A-Z][A-Z0-9_]{1,31}$`），不枚举合法值；Go 端格式校验为准。其他枚举不据此改为文本。
- 枚举值只追加，禁止重编号和 `state >= 2` 这类序数判断；0 保留为 UNSPECIFIED，不作为有效值写入或输出，可选枚举缺失使用 NULL。生命周期列命名为 `state`，由领域状态迁移方法更新，不能通过通用 PATCH 任意赋值。

### 字段、时间与隐私

- 时间戳使用 TIMESTAMPTZ，数据库与 API 统一 `<verb>_time` 命名，不另设 `_at` 转换。常规 `create_time` 使用 `DEFAULT now()`，`update_time` 在 INSERT 时初始化、UPDATE 时由应用显式更新，不用数据库触发器维护。
- 区分实际发生时间与入库时间：审计 `occur_time` 由生产者提供，`create_time` 沿用 `clock_timestamp()` 首次入库语义；不能将所有表的时间默认值机械统一。`now()` 在同一事务内固定，不能依赖时间戳单列区分行或替代分页决胜键。
- NULL 表示未提供或尚未发生，不能用空字符串、零时间或枚举 0 随意替换；字段的 NOT NULL、默认值和 sqlc 可空类型应与领域语义一致。JSONB 沿用已定义的结构与容量校验，不能成为绕过类型和隐私约束的任意数据入口。
- 手机/邮箱按既有 `pii` 格式保存版本化 AES-GCM 密文及 HMAC 摘要，不新增明文列。密钥轮换、摘要匹配和旧令牌接管必须兼容；配置变更同步更新 README 与测试。

### 查询、索引与删除语义

- 索引由实际查询、排序或唯一性要求驱动，非显然的索引写明用途；引用列是否建索引按关联查询和清理路径决定，不因无外键而遗漏。修改查询或索引时核对过滤条件、复合列顺序和部分索引谓词，性能敏感路径用代表性数据检查执行计划。
- 列表沿用 keyset 游标分页，排序与游标条件必须一致，通常为 `ORDER BY create_time, id`；审计使用 `occur_time, id`。不能仅按 ID 推断创建先后，也不能仅按非唯一时间列排序。
- 软删除查询、部分唯一索引和注销冷静期遵循现有生命周期。活跃身份查询与唯一索引使用 `delete_time IS NULL`；会话按吊销、过期条件判断。管理查询、恢复和匿名化可能需要历史行，不能给所有查询机械追加软删除过滤。
- 部分唯一索引只约束其谓词覆盖的行，应用查重不能替代数据库唯一性兜底；恢复、重新绑定及并发首次登录必须继续处理身份冲突。匿名化需覆盖已解绑身份，因此 `identity(user_id)` 保留完整索引，不能收窄为仅活跃行。
- 账号删除、身份解绑和到期匿名化是不同阶段，不得用硬删除或简单过滤规则替代。硬清理仅用于明确允许的过期会话、短期流程等维护路径，或精确确认的一次性开发测试资源；不能照搬宿主业务表的删除策略。

## 迁移安全

操作步骤以 [docs/migrations.md](docs/migrations.md) 为准。以下规则不能因版本尚处开发阶段而放宽：

- v0.5.0 已发布，tag 固定到 `102315c3069273f7a8c3ad253c1f4ae01255f8c6`；其中的 0001 已包含资料属性与头像，原始 SQL 字节不可改写。下述资料单列整合及头像直接修改 0001 的限定授权均已完成，不构成后续改写开关；后续真实结构变更从 0002 递增，已发布 tag/SQL 和冻结源保持。用户确认当前尚无正式部署，本次发布不提供旧数据库迁移工具；这不放宽迁移历史保护。证据见 [v0.5.0 发布说明](docs/releases/v0.5.0.md)。

- 本轮 `add-google-sign-in` 经用户明确要求“数据库变更直接修改 0001”，仅授权更新当前 `0001_init.up.sql` 里 `identity.kind` 与 `provider_meta` 的注释（提及 GOOGLE 及 `profile.name` / `profile.picture`），并只同步当前 `initialFrozen`。不新增列、索引、CHECK 或 0002；down、已发布 tag/SQL 和冻结源保持。新安装仍为 version=1/dirty=false。同号数据库不重放 0001，不会自动更新注释。不 Force、改版本或清库。此授权不构成日常改写初始 SQL 的开关。
- 本轮 `add-identity-provider-registry` 经用户明确要求“直接改 0001”，仅授权把当前 `0001_init.up.sql` 中 `identity.kind` 改为 `VARCHAR(32) COLLATE "C" NOT NULL` 并加格式 CHECK `identity_kind_format`，同步相关注释、sqlc、运行时预检和当前 `initialFrozen`。索引定义文本、其他列与约束、down、已发布 tag/SQL 和冻结源保持，不新增 0002；新安装仍为 version=1/dirty=false。`identity.kind` 仍为 SMALLINT 的 v1 及该列漂移结构无写拒绝，dirty 优先；不转换列、不重放同号 SQL、不 Force、改版本或清库，也不提供旧数据迁移。见 [OpenSpec 变更](openspec/changes/add-identity-provider-registry/proposal.md)。此授权不构成日常改写初始 SQL 的开关。
- 本轮 `add-user-avatar-url` 经用户明确要求“不新增 0002 迁移，改现有 0001”，仅授权在当前 `user_account` 增加 `avatar_url TEXT NULL`（无默认值、索引或业务 CHECK），同步 sqlc、运行时预检和当前 initialFrozen。新安装仍为 version=1/dirty=false，down 整表语义保持。前一含资料但无头像的 v1、全部既有历史拒绝结构及撤回 v2 均无写拒绝，dirty 优先，保留 catalog、数据、version/dirty；不补列、重放同号 SQL、Force、改版本、清库或提供旧数据迁移。已发布 tag/SQL、v0.4.0 精确 pin 和冻结源保持；后续增量仍从 0002，测试 probe 使用 latest+1。本条仅覆盖头像列，以下历史授权与验收保持原事实；见[头像计划](docs/superpowers/plans/2026-10-21-add-user-avatar-url.md)和[验收记录](openspec/changes/archive/2026-10-21-add-user-avatar-url/verification.md)。

- 本轮用户明确要求“0002 并入 0001，无需考虑旧数据迁移”，仅授权把 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb` 单列合入当前工作树 0001，撤回生产 0002；新空 schema 安装 version=1/dirty=false，其他列、索引和业务行为保持。当前 initialFrozen 新哈希与 v0.4.0 已发布旧定义分别固定；v0.4.0 精确 tag `62f0799b6c6ef1603b33f81834d39033dd0d0c4d`、其他发布 tag/SQL 和冻结源保持。旧 v0.4.0 v1 缺资料列、其他历史 v1 及各代撤回 v2（含单列、四列、五列及混合资料 v2）均无写拒绝，dirty 优先，保留 catalog、数据及 version/dirty；不转换旧数据、不重放同号 SQL、不 Force、改版本或清库。本轮不提供旧数据迁移工具或升级承诺；后续真实增量从 0002 递增，测试 probe 使用 latest+1（当前为 2）。 本轮见[整合计划](docs/superpowers/plans/2026-10-21-consolidate-profile-attributes-initial.md)和[验收记录](openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/verification.md)。此授权不扩大历史授权，不构成日常改写初始 SQL 的开关。

- v0.4.0 已发布，tag 固定到 62f0799b6c6ef1603b33f81834d39033dd0d0c4d，其中的已发布 0001 原始字节不可改写；当前工作树仅按上述本轮单列整合授权调整并独立固定。下列 `simplify-session-context`、数据库设计评审和 `consolidate-session-context-initial-schema` 等直接修改 0001 的有限授权仅适用于 v0.4.0 发布前，不再适用；后续结构从 0002 递增。发布证据见 [v0.4.0 说明](docs/releases/v0.4.0.md)。

- `simplify-session-context` 经用户本轮明确批准，在前一 `b414fef` 未发布会话初始定义上继续直接修改当前0001：移除known标记和INITIAL/LATEST双快照，来源为必有非空领域不变量，客户端描述以session_id为唯一主键保存单行JSONB及observe/create/update时间；同步down、baseline、当前固定哈希和sqlc，无生产0002，初始化仍为version=1。客户端JSON的对象、核心字段、扩展边界及敏感键均由业务读写验证，数据库只保留主键、ID格式和非空等基础约束，不增加JSON对象/业务CHECK。旧v0.3.0 v1、前一b414fef双快照v1及撤回会话v2均无写拒绝，保留catalog、数据、version/dirty且dirty优先，不自动转换或补来源、不Force、改版本或清库；保留数据需求另行设计迁移。所有历史tag、已发布SQL及冻结source-baseline不变，三代历史保护保持；旧v0.3.0预检可能接受同号新结构，部署须人工阻止混跑和未验证回退。此授权仅覆盖本次简化，不覆盖或改写下列历史授权，也不允许日常改写初始SQL。当前证据统一见[简化验收](openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)。

- 数据库设计评审经用户本轮明确要求“不建新 change、直接修改 0001”，限定为当前未发布 0001 的索引调整：密钥回填复合索引、同用户同设备未吊销会话部分唯一索引 `uk_session_user_id_device_id`（取代 `idx_session_user_id`），以及移除包内无查询使用的四个审计/流程索引；表、列、约束、down 与 version=1 保持，同步当前固定哈希。旧同号数据库不重放 0001，保留旧索引且无新唯一兜底；不 Force、改版本或自动清库。配套查询与锚点 subject 跨摘要版本 advisory lock 属代码改动。此授权不扩展为日常改写初始 SQL，细节见 [迁移手册](docs/migrations.md#当前初始基线)。

- v0.3.0 已发布，tag 固定到 ba2d0412764c659beabdbd0d3f0f0f9bb9007657，其中的 0001 是不可改写的已发布历史。用户本轮明确要求“把 0002 直接并入 0001”：`consolidate-session-context-initial-schema` 仅将未发布会话 0002 的字段、两表及索引整合进当前工作树 0001，同步 down 和当前固定哈希，初始化保持 version=1，生产 0002 撤回。当前 v1 须包含完整会话上下文；旧 v0.3.0 v1 及撤回会话 v2 均无写拒绝，不自动转换、改版本或清库；有保留数据需求时另行设计迁移。v0.1.0/v0.2.0/v0.3.0 标签、其中的已发布 SQL 和固定源快照保持，历史检查分三代独立固定。后续结构仍从 0002 递增，此授权不构成日常改写初始 SQL 或跳过门禁的开关。发布证据见 [v0.3.0 说明](docs/releases/v0.3.0.md)。

- 用户确认尚无实际产品接入后，`consolidate-initial-schema` 执行过一次明确的初始结构整合：`0001_init` 直接定义全部认证及登录补全结构，初始版本为 1。此前 `generalize-audit-events` 经用户明确授权，将十列 UUIDv7 审计定义纳入未发布的 0001、更新其固定校验，并撤回当时新增的 0002/staging 工具。旧完整十七列审计 v1 和旧增量结构 v2 均拒绝且保留数据与版本；固定源快照和已发布历史不变。当前初始 SQL 按独立固定哈希保护，不提供日常改写、squash 或跳过历史检查的开关。
- `rename-sign-in-completion-flow` 经用户本次明确要求“不新增 0002，直接改 0001 中的表定义”，限定为修改当前未发布 0001 的流程表、五个独立索引、主键与格式 CHECK 名称及配套 down，并同步当前固定哈希；列、约束表达式和索引定义保持。撤回本分支新增的 0002，初始化保持 version=1，后续真实增量从 0002 开始。旧表名 v1、双表并存及已撤回旧 v2（包括此前新表名 v2）均拒绝且保留数据、结构和版本，重复 Up 不自动改名或接管。采用新定义须人工核验后使用新空资源，需保留数据时另行制定并验证迁移方案，不 Force、不修改版本、不自动清库；公共 Down 仍拒绝。内部使用 `SignInCompletionFlow`，公开旧 `ids.IdentityFlow`、`user.Deps.IdentityFlowContext`、`CleanupIdentityFlows` 保留 deprecated 兼容入口，新上下文字段非空时优先、否则回退旧字段；密文 AAD、`f_`、审计 `IDENTITY_FLOW` 和 JSON `flow_id` 保持。本次授权不扩展为日常改写初始 SQL 或修改冻结源与已发布历史。
- 此前索引命名维护另经用户明确要求“不新增迁移”，限定为直接将未发布 0001 的 22 个普通索引改用 `idx_`、4 个独立唯一索引改用 `uk_` 前缀并同步固定校验；当次主键、索引定义、初始版本和历史源快照未变。旧同号数据库不会重放 0001，也不会自动得到该次索引命名调整；这些有限授权不扩展为后续任意改写初始 SQL。
- `tests/testdata/source-baseline/` 的原始 SQL 与 manifest 仍冻结；已发布 v0.1.0/v0.2.0/v0.3.0 的精确标签提交和历史 SQL 保持。历史检查分别核验源快照、v0.3.0、v0.4.0 已发布定义和当前整合定义的独立基线，不能改写源快照或移动旧 tag 来让检查通过。旧数据库不自动接管，使用新基线须经人工核验后使用新空资源；不得 Force、修改版本或自动清库。
- 新业务结构使用递增 `NNNN_name.up.sql` / `.down.sql`，已发布版本不可修改、删除、重编号或 squash。测试专用下一版本放 `migrations/testdata/`，不混入生产嵌入目录。
- `schema_migrations` 记录当前 version/dirty，不是完整历史账本。重复 Up 只忽略无新迁移；dirty 必须阻断后续升级，禁止自动 Force、清除 dirty 或把失败标记为成功。
- 保留从 schema 初始化开始的外层锁，以及与旧工具兼容的引擎锁。锁等待必须支持取消和超时，所有错误路径必须释放连接及锁；不能假定底层 WithInstance 初始化失败会自动释放借出的连接。
- `Down` 保留签名但无 I/O 返回 `ErrDestructiveOperation`；`UnsafeReset` 仅用于一次性开发测试数据，必须精确确认目标 schema。生产恢复不调用全量清库入口。
- 历史检查覆盖固定源基线及全部可达正式 `vX.Y.Z` tag，包括合并分支和 HEAD；需要完整历史和 tags，不能只看第一父链或跳过浅克隆检查。
- 应用回退必须验证新结构兼容旧代码；数据库恢复先落到独立空库核验。备份不包含 Redis 和密钥系统，不能宣称零数据损失或通过清空 Redis 实现安全恢复。

## 认证行为与兼容性

- 会话认证来源始终为事务校验过的非空具体身份集合；当前详情、重新认证和换绑使用来源时检查有效性，缺失或失效为内部不变量错误。解绑须检查同账号所有未吊销会话（含自然过期未清理者），任一缺少有效来源则整事务拒绝，不能按未命中放行或从身份列表/尽力审计补造；普通JWT/内省不增加数据库访问。
- 客户端描述为单份完整观察：非空规范化输入整体替换，nil/空输入不写且保持旧值和时间；孤立version/build合法，app/browser并存。pending最终本次非空描述优先，否则整体沿用加密首次描述及原observe_time；失败、回滚和重放不更新。Go保留平坦ClientInfo并新增DeviceManufacturer、AppBuild和Extensions，存储/HTTP采用nested JSON；扩展仅来自Go/宿主回调，无新任意JSON HTTP输入。业务读写负责完整校验与深拷贝，宿主负责扩展来源、语义和隐私白名单，固定敏感键检查不能识别任意值中的隐私。

- 保留刷新轮换、宽限内返回同一 token pair、宽限外重放处理及同设备会话规则；不要把所有重复刷新都改成失败。
- 保留验证码用途、尝试次数、发送额度、重新认证窗口及账号状态校验，失败路径不能绕过限流或回滚掉尝试计数。
- Redis 失败模式按 README 区分：验证码流程 fail-closed；刷新宽限查询失败拒绝且不误吊销；access 吊销查询当前为 fail-open 并记录警告。更改这些行为必须作为明确的兼容/安全设计变更。
- 审计默认有界异步写入，记录失败不改变认证动作结果；不要未经设计改成同步强依赖。新增敏感操作要补相应审计与脱敏测试。
- 审计生产事件为 `audit.Event`，存储记录为 `audit.RecordView`；注入写侧须显式配置稳定的 AuditSource/AuditNamespace。宿主可同时注入 Reader，只有写侧时已授权审计查询返回 503 AUDIT_QUERY_NOT_CONFIGURED，不能静默读取本地旧副本。库只管理自建 worker；宿主负责注入组件生命周期，以及外部保留、持久化清理流程和完成核验。USER_PURGED 是尽力审计通知，不能当作唯一可靠清理通知或完成证据；切换外部模式不免除旧本地副本责任。
- 改动公开 API、配置默认值、错误码、SQL、ID、令牌/密文格式或宿主回调时，先读取相关规格及兼容测试；破坏性变化写出调用迁移说明，不能静默“统一”成另一个产品的实现。

## 开发与验证命令

以下命令均从仓库根目录运行。Bash 可使用 Git Bash；PowerShell 设置环境变量使用 `$env:GOWORK = 'off'`。

```bash
export GOWORK=off
go build ./...
go vet ./...
go test -count=1 ./...

# 修改查询/schema/sqlc 配置后重新生成，提交生成结果
sqlc generate
# 验证已提交的生成结果与配置一致
bash scripts/check-generated.sh
# 校验历史迁移（需要完整 Git 历史与 tags）
bash scripts/check-migrations.sh

# 完整验证前提供三个一次性数据库的连接串
# SERVER_TEST_DB_DSN：普通数据库集成测试
# ACCOUNTKIT_RECOVERY_SOURCE_DSN：专用空源库
# ACCOUNTKIT_RECOVERY_TARGET_DSN：另一个专用空恢复库
bash scripts/verify.sh
```

- 快速测试缺少 `SERVER_TEST_DB_DSN` 会跳过数据库用例，只能报告单元测试结果；不能据此宣称集成验证通过。
- 完整发布验证包含独立构建、vet、race、必需测试检查和真实 pg_dump/pg_restore 演练。新增必需顶层测试时同步维护 `scripts/required-tests.txt`，不能通过删条目或允许 skip 掩盖失败。
- `TestRecoveryFixture` 是特例：普通套件预期 skip，恢复脚本分别强制执行 seed、verify、source-unchanged 三阶段。不要照搬其他仓库“发现任何 SKIP 即失败”的规则。
- 恢复脚本要求源库和目标库实际不同且均为空，有 PostgreSQL 17 客户端及读取集群标识的权限。脚本不覆盖既有库，重跑需新空库；数据、备份与日志留在被忽略的 `.test-output/`。
- race 需要 CGO 工具链。Windows 缺少编译器时使用 `tests/Dockerfile` 的 Linux 验证镜像，不通过禁用 race 伪造完整验证；CI 同时执行 sqlc 检查与完整验证脚本。
- 按改动范围验证：Go 变更运行相关测试和 vet，认证/事务/迁移变更补真实数据库与并发验证，发布前执行完整入口。纯文档改动检查内容、路径、引用和差异，无需为文档新增镜像实现的测试。
- 交付说明列出实际运行的检查及未验证项，结果写入对应变更的 `verification.md`。发布前置条件见 [docs/release-checklist.md](docs/release-checklist.md)，环境准备见 [docs/development.md](docs/development.md)。历史记录不等于当前代码或在线 CI 已通过。

## Git 与 OpenSpec 工作流

- 集成分支为 `develop`，稳定发布分支使用 `main`。默认从 develop 创建 `<type>/<short-kebab-description>` 短期分支，不直接在 develop/main 提交；如已有未提交改动，先带到任务分支，不能 reset 掉用户工作。
- Conventional Commits：`type(scope): message`，scope 可按需省略；类型包括 feat、fix、docs、test、refactor、chore、ci。scope 使用能力域，如 migrations、tokens、httpapi。
- 默认通过 GitHub Pull Request 合入 develop；用户明确要求本地合并时按其要求执行。提交、推送、tag、发布和产品部署是不同动作，不把其中一个授权扩展成全部动作。
- 合并统一保留独立 merge commit：任务分支合入 `develop`、`develop` 合入 `main` 时，本地使用 `git merge --no-ff <source-branch>`，即使可以快进也不省略合并提交；GitHub PR 使用 Create a merge commit，不使用 Squash and merge 或 Rebase and merge，除非用户明确另行指定。
- 功能、行为、公共接口或架构变更通过 OpenSpec；纯文档维护可直接修改。先读相关 `openspec/specs/`，再读涉及的变更工件；未归档变更的 specs 仍在 `openspec/changes/<change>/specs/`，不能因主规格目录为空就忽略它们。
- 遵循 `openspec/config.yaml`：工件正文中文，OpenSpec 结构标题和 SHALL/MUST 保留英文。使用已安装的 openspec CLI 和 `.agents/skills/` 中对应工作流，不假定其他版本的命令可用。
- 每次变更的测试结果、审查结论和未验证项统一保存在 `openspec/changes/<change>/verification.md`，归档时随变更一起移动。`docs/` 保留长期维护的验证方法、开发指南和发布检查表，只引用各次验收记录，不另存重复报告；原始日志、测试输出和恢复材料保留在被忽略的 `.test-output/`。归档或移动记录时同步修复记录内的相对链接和外部引用。
- 实施前读取 proposal、design、全部相关 specs 和 tasks；只在实际完成且验证后勾选任务。明确的设计偏离记录在该 change 的 design.md，并更新受影响规格/文档；不能以修复为由静默扩大范围或删减已确认要求。
- 实施后的核验使用 `openspec validate --all --strict`；归档作为单独明确步骤执行，不能因 tasks 全勾选就报告已归档。

### OpenSpec apply 默认执行规范

1. 读取完整工件并遵循 apply 指令；通过 `superpowers:writing-plans` 形成详细实现计划，默认放 `docs/superpowers/plans/`，引用对应 OpenSpec change，避免复制出互相冲突的需求。
2. 默认使用 `superpowers:subagent-driven-development` 执行可拆分任务；仅并行没有共享写入或前置依赖的工作。工具不可用或用户指定直接实施时改为顺序执行，并说明实际执行方式。
3. 功能/缺陷修复遵循 `superpowers:test-driven-development`，先复现或写行为测试，再做最小修改；纯文档和可逆低影响维护不强行套用 TDD。
4. 每个实现步骤按 `superpowers:requesting-code-review` 复核规格与代码，先处理实质问题，再进入依赖该步骤的工作。
5. 及时更新 tasks；交付前使用 `superpowers:verification-before-completion` 核对实际证据，不把构建成功、测试 skip 或旧日志当作当前完整验证。

## 代理操作约束

- 修改限定在本次任务范围；沿用现有结构，避免顺带重构与引入不必要依赖。
- 不提交真实 `.env`、密钥、服务商凭据、数据库备份或用户数据；测试使用合成数据，日志与审计也不得输出完整凭证。
- 不手改生成代码，不修改源基线，不为逃避门禁删除测试或放宽验证，不擅自变更既有公开契约。
- 用 `rg` 搜索，修改 Go 后 gofmt；遵守 `.gitattributes` 的 Go/SQL/shell LF，保留固定源快照的原始字节及当前迁移的固定校验值。
- 使用可识别的一次性测试资源，结束后只清理本任务创建的容器/数据库；不得操作产品数据库或未经核验的目录。
- 当前任务的用户授权优先于默认流程；遵守执行环境的文件与网络权限，不绕过权限检查。遇到实质信息缺失再询问，不重复索取已有授权。
