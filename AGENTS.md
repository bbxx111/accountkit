# AGENTS.md — accountkit

本文件是本仓库所有编码代理的统一项目指南。`CLAUDE.md` 只引用本文，不维护第二份规范。沟通与项目文档默认使用中文；代码标识符遵循 Go 和既有协议约定。

本文定义 accountkit 的 API、领域建模、独立集成及开发规范。用户在当前任务中的明确要求优先。

## 项目定位与边界

- accountkit 是可嵌入宿主服务的 C 端账号认证 Go 包，模块为 `github.com/bbxx111/accountkit`，根 Go package 为 `accountkit`。
- 首版包含验证码登录、多身份与微信/Apple 登录、JWT/刷新会话、重新认证、身份绑定、注销/恢复/匿名化、管理接口、审计和维护任务。功能对照见 [docs/compatibility.md](docs/compatibility.md)。
- 单一 Go module，构建必须可在 `GOWORK=off` 下完成，不依赖外部工作区、提交的本地 replace、宿主业务表或部署环境。
- 当前存储为 PostgreSQL + Redis。库不监听端口，不管理网关，不内置管理员账号系统，不强制 Keycloak，也不承担短信/邮件服务商装配。不要擅自扩展为前端项目或多数据库适配层。
- accountsvc 定位为本项目自带的可选服务实现，基于 accountkit 装配运行；库不依赖该服务，其他项目仍可直接嵌入库。服务运行时尚待独立变更实现，现有 `examples/embedded` 是开发宿主示例。
- 宿主负责连接池、Redis 客户端、发送器、可信代理/IP 解析、请求 ID、管理员身份验证以及业务匿名化回调。配置与接口细节以 [README.md](README.md) 和公开 Go 类型为准。

## 技术栈与目录

当前工具版本：Go 1.26.5（以 go.mod 为准）、sqlc 1.31.1、PostgreSQL 17。使用 Chi、pgx/v5、golang-migrate、go-redis/v9；Redis 测试采用 miniredis。CI 为 GitHub Actions，入口是 `.github/workflows/ci.yml`。

| 路径 | 职责 |
|---|---|
| `accountkit.go`、`config.go` | 对外门面、依赖注入、配置及生命周期 |
| `user/` | 账号领域规则、事务、状态机与仓储 |
| `httpapi/consumer/`、`httpapi/admin/` | 两个独立 HTTP 面及各自 DTO |
| `httpapi/authn/`、`httpapi/apierror/` | 认证中间件、协议错误映射 |
| `user/code/`、`user/idp/`、`user/sender/` | 验证码、第三方身份验证、发送器契约 |
| `tokens/`、`pii/`、`ids/`、`enum/` | 令牌、隐私数据、资源 ID、枚举 |
| `session/grace/`、`session/revocation/` | 刷新宽限缓存与吊销集 |
| `audit/`、`maintenance/`、`anonymize/` | 审计、维护任务、宿主匿名化契约 |
| `migrations/` | 嵌入 SQL、迁移锁、开发测试清库入口 |
| `user/query.sql`、`audit/query.sql` | 手写查询；生成结果分别进入 `user/db/`、`audit/db/` |
| `internal/migrationcheck/`、`internal/testgate/` | 发布检查工具，不是宿主公共接口 |
| `examples/embedded/` | 可独立编译的开发宿主示例 |
| `tests/testdata/source-baseline/` | 固定源清单、原始 SQL 和合成数据格式 |
| `scripts/`、`docs/`、`openspec/` | 验证入口、操作手册、规格与变更 |

## 架构与宿主契约

- 生命周期固定为 `Config/Deps → New → Migrate → Start → Close`。`New` 校验并装配，不做 I/O；启动前执行 `Migrate`，关闭后不再运行维护任务。宿主创建的 Pool/Redis 由宿主管理和关闭。
- HTTP handler 只做协议转换；业务规则、事务和状态约束集中在 `user.Service`，由 C 端、管理面和维护任务复用。不要为管理路径绕开领域约束，也不要添加无业务意义的透传 service 层。
- SQL 放在领域查询文件，经 sqlc 生成。禁止手改 `user/db/`、`audit/db/`；修改查询、schema 或类型映射后运行 `sqlc generate` 并检查生成差异。
- 配置通过 `Config` 或 `ConfigFromEnv(prefix)` 注入；环境变量前缀由宿主选择，示例使用 `ACCOUNTKIT_`。不要硬编码 `SERVER_`、产品域名、业务 schema 或产品密钥。
- `ConsumerHandler()` 与 `AdminHandler()` 返回相对路由，由宿主分别挂载，示例为 `/v1` 和 `/admin/v1`。包内不重复写死前缀，也不引入 `/api`。
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

## 数据库与实例隔离

- 包拥有 `user_account`、`identity`、`session`、`audit_event` 和 `schema_migrations`，位于 `Config.Schema` 指定的 schema，默认 `auth`。使用无业务前缀的单数 snake_case 表名；隔离靠 schema，不添加产品专用表前缀。
- 同一数据库可有多个独立实例。宿主使用 `PoolConfig` 设置 search_path，并独立配置 schema、Redis `KeyPrefix`（以 `:` 结尾）、JWT issuer/audience 和密钥。新增 Redis 键必须包含实例前缀。
- PostgreSQL 同库可以跨 schema 查询、join、事务及定义外键；本包不使用外键是领域设计选择，不是数据库不支持跨 schema。不要改变既有无外键契约；引用一致性由领域写路径和测试保证。
- 主键使用 `ids` 的类型前缀 TSID：`u_`、`i_`、`s_`、`e_` 加 13 位小写 Crockford base32；TEXT/COLLATE C 和格式 CHECK 保持一致。ID 只承诺相等查找；分页排序必须有时间字段和唯一的次级排序键。
- 凭证不是资源 ID。验证码、refresh token 等沿用专用随机生成与校验逻辑，不能使用 TSID 代替随机秘密。
- 枚举存 SMALLINT，Go 类型集中在叶子包 `enum`，sqlc 使用类型 override。值只追加，禁止重编号和 `state >= 2` 这类序数判断；不新增枚举值 CHECK。
- 数据库与 API 统一 `_time` 命名，`update_time` 由应用更新；索引/约束名称包含表名，非显然的索引写明服务于哪个查询或约束。
- 软删除查询、部分唯一索引和注销冷静期遵循现有生命周期。账号删除、身份解绑和到期匿名化是不同阶段，不得用硬删除或简单过滤规则替代。
- 手机/邮箱按既有 `pii` 格式保存版本化 AES-GCM 密文及 HMAC 摘要，不新增明文列。密钥轮换、摘要匹配和旧令牌接管必须兼容；配置变更同步更新 README 与测试。

## 迁移安全

操作步骤以 [docs/migrations.md](docs/migrations.md) 为准。以下规则不能因为独立包尚未发布而放宽：

- `0001_init` 已冻结，必须与 `tests/testdata/source-baseline/` 中固定的原始字节和校验值一致。其保留注释中“部署前可改写”不适用于 accountkit；不要改写 SQL 或 manifest 来让检查通过。
- 新业务结构使用递增 `NNNN_name.up.sql` / `.down.sql`，已发布版本不可修改、删除、重编号或 squash。测试专用下一版本放 `migrations/testdata/`，不混入生产嵌入目录。
- `schema_migrations` 记录当前 version/dirty，不是完整历史账本。重复 Up 只忽略无新迁移；dirty 必须阻断后续升级，禁止自动 Force、清除 dirty 或把失败标记为成功。
- 保留从 schema 初始化开始的外层锁，以及与旧工具兼容的引擎锁。锁等待必须支持取消和超时，所有错误路径必须释放连接及锁；不能假定底层 WithInstance 初始化失败会自动释放借出的连接。
- `Down` 保留签名但无 I/O 返回 `ErrDestructiveOperation`；`UnsafeReset` 仅用于一次性开发测试数据，必须精确确认目标 schema。生产恢复不调用全量清库入口。
- 历史检查覆盖固定源基线及全部可达正式 `vX.Y.Z` tag，包括合并分支和 HEAD；需要完整历史和 tags，不能只看第一父链或跳过浅克隆检查。
- 应用回退必须验证新结构兼容旧代码；数据库恢复先落到独立空库核验。备份不包含 Redis 和密钥系统，不能宣称零数据损失或通过清空 Redis 实现安全恢复。

## 认证行为与兼容性

- 保留刷新轮换、宽限内返回同一 token pair、宽限外重放处理及同设备会话规则；不要把所有重复刷新都改成失败。
- 保留验证码用途、尝试次数、发送额度、重新认证窗口及账号状态校验，失败路径不能绕过限流或回滚掉尝试计数。
- Redis 失败模式按 README 区分：验证码流程 fail-closed；刷新宽限查询失败拒绝且不误吊销；access 吊销查询当前为 fail-open 并记录警告。更改这些行为必须作为明确的兼容/安全设计变更。
- 审计默认有界异步写入，记录失败不改变认证动作结果；不要未经设计改成同步强依赖。新增敏感操作要补相应审计与脱敏测试。
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
- 交付说明列出实际运行的检查及未验证项。已有结果见 [docs/recovery-verification.md](docs/recovery-verification.md)，发布前置条件见 [docs/release-checklist.md](docs/release-checklist.md)，环境准备见 [docs/development.md](docs/development.md)。历史记录不等于当前代码或在线 CI 已通过。

## Git 与 OpenSpec 工作流

- 集成分支为 `develop`，稳定发布分支使用 `main`。默认从 develop 创建 `<type>/<short-kebab-description>` 短期分支，不直接在 develop/main 提交；如已有未提交改动，先带到任务分支，不能 reset 掉用户工作。
- Conventional Commits：`type(scope): message`，scope 可按需省略；类型包括 feat、fix、docs、test、refactor、chore、ci。scope 使用能力域，如 migrations、tokens、httpapi。
- 默认通过 GitHub Pull Request 合入 develop；用户明确要求本地合并时按其要求执行。提交、推送、tag、发布和产品部署是不同动作，不把其中一个授权扩展成全部动作。
- 合并统一保留独立 merge commit：任务分支合入 `develop`、`develop` 合入 `main` 时，本地使用 `git merge --no-ff <source-branch>`，即使可以快进也不省略合并提交；GitHub PR 使用 Create a merge commit，不使用 Squash and merge 或 Rebase and merge，除非用户明确另行指定。
- 功能、行为、公共接口或架构变更通过 OpenSpec；纯文档维护可直接修改。先读相关 `openspec/specs/`，再读涉及的变更工件；未归档变更的 specs 仍在 `openspec/changes/<change>/specs/`，不能因主规格目录为空就忽略它们。
- 遵循 `openspec/config.yaml`：工件正文中文，OpenSpec 结构标题和 SHALL/MUST 保留英文。使用已安装的 openspec CLI 和 `.agents/skills/` 中对应工作流，不假定其他版本的命令可用。
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
- 用 `rg` 搜索，修改 Go 后 gofmt；遵守 `.gitattributes` 的 Go/SQL/shell LF，尤其不要破坏冻结 SQL 的原始字节。
- 使用可识别的一次性测试资源，结束后只清理本任务创建的容器/数据库；不得操作产品数据库或未经核验的目录。
- 当前任务的用户授权优先于默认流程；遵守执行环境的文件与网络权限，不绕过权限检查。遇到实质信息缺失再询问，不重复索取已有授权。
