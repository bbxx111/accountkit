# Proposal

## Why

v0.1.0 到 v0.7.2 共九个版本均未被正式使用：仓库自身无正式部署，唯一接入方 ai-food 只在本地环境联调且未发布产品。然而仓库里的文档、迁移门禁和测试仍按“每一代已发布结构都要被保护、每一种旧库都要被识别并拒绝”的方式累积：面向读者的文档是逐版本增量账本，迁移检查工具固定七代 tag 与 0001 哈希，运行时预检分五组反查旧结构，抽取前原项目的 130 文件源快照仍作为兼容基线。一个第一次接入的读者要先理解整段演进才能知道当前状态，而这些保护没有任何真实对象。用户决定以 v1.0.0 作为第一个正式版本，在此之前把历史记录整合为单一基线，并清理只为从未使用过的旧入口保留的兼容壳。

## What Changes

- **文档重写为 v1.0.0 基线（第 1 层）**：README、`docs/features.md`、`docs/migrations.md`、`docs/compatibility.md`、`docs/release-checklist.md`、`docs/openapi.md`、Go 集成与网关文档及两份 OpenAPI 的版本表述，全部改为只描述当前状态，不再按版本增量叙述。九份 `docs/releases/v0.*.md` 压缩为一份开发期历史摘要，`docs/releases/v1.0.0.md` 由后续发布准备变更产出。`docs/compatibility.md` 中针对开发版之间升级的章节与三节“历史提取索引”删除，只保留功能对照。
- **新增平台中立的 HTTP 调用流程文档**：覆盖首次登录到 pending 补全、刷新协调与宽限语义、重新认证后的客户端状态、注销/撤销/解绑导致当前会话退出的清理；配套基于 httptest 的示例测试，使关键请求/响应片段由测试产出。只讲协议层状态机，不替具体平台决定凭证存储方案。
- **过程记录原样保留（第 2 层）**：`openspec/changes/archive/` 与 `docs/superpowers/plans/` 不删除、不改写，只修复因文档重写而失效的相对链接。
- **BREAKING 迁移门禁收缩为单一基线（第 3 层）**：迁移历史检查工具删除源快照哈希、v0.1.0/v0.2.0 提交 pin 与 v0.3.0 至 v0.6.0 的 0001 pin，只认 v1.0.0 及以上正式 tag，低于 v1.0.0 的 tag 静默忽略；保留当前 0001 冻结哈希、可达正式 tag 内迁移不可改写、新版本号必须递增三项机制。`tests/testdata/source-baseline/` 整目录、读取它的原项目格式兼容测试与 `migrations/testdata/legacy_integrated_v1.sql` 退役。
- **BREAKING 运行时预检只保留面向未来的两条**：dirty 优先阻断，以及数据库版本超出当前源时的拒绝；删除流程表、审计十列、会话上下文、资料与头像、身份类型、purged_user 五组反查旧结构的预检及其基线文件。`ErrIncompatibleBaseline` 保留给版本超前场景。`UnsafeReset` 只保留 schema 确认。
- **旧结构测试按用例三分**：只证明“旧结构被拒绝”的用例删除；证明当前安装、重复 Up、并发、锁、dirty、latest+1 升级、UnsafeReset 的用例保留；少数改为针对 v1.0.0 单一基线的断言。`scripts/required-tests.txt` 同步删除对应条目，并在验收记录中核对删后清单仍覆盖全部存活用例。
- **BREAKING 删除 6 处 Deprecated 兼容入口**：`ids.IdentityFlow`、`user.Deps.IdentityFlowContext`、`(*user.Service).CleanupIdentityFlows`、`sender.SMSSender`、`sender.EmailSender`、`sender.Message`；对应兼容测试与文档迁移说明一并退役。
- **公开 Go API 冻结前盘点**：用 `go doc -all` 通读根包、`user`、`httpapi/*`、`tokens`、`audit` 等公开面，发现的命名不一致或不该导出的符号在 v1.0.0 前处理或明确记录为接受；不新增功能。
- **改写代理规则**：AGENTS.md「迁移安全」节改为只描述 v1.0.0 基线（0001 自 v1.0.0 tag 起冻结，后续结构从 0002 递增，低于 v1.0.0 的 tag 已退役、尚待单独删除且不参与历史保护）；删除“已发布 v0 tag 不可删除”与“不修改冻结源”两条已无保护对象的规则。
- **明确排除**：其余四项候选功能（HTTP 客户端 SDK、纯第三方账号自助恢复、更多认证方式、两个已有账号合并）不进入本变更；六项真实环境验证缺口作为已记录边界随版本发布，不阻塞。
- **后续独立动作**：删除远端 v0.1.0 至 v0.7.2 的 tag 与 GitHub Release 是对外且不可逆的动作，须在本变更合入后单独取得确认再执行；v1.0.0 发布准备另起变更。

## Capabilities

### 2026-10-31 扩展范围

用户明确要求将 13 项 API 盘点后的必要调整纳入本变更，具体决策见 design D12。新增范围包括：删除开发期审计写侧转换与无用符号、公开覆盖登记改为副本函数、微信配置构造器及 SMTP/grace 名称整理、Repo 的查询/事务方法私有化、sqlc 输出与 HTTP 工具包内部化；保留直接领域服务、不透明 Repo、独立密钥检查、审计读取兼容及已有 IdP 协议差异。相关调用说明与规格同步，补齐延期测试。允许调整 sqlc 输出路径并重新生成，不修改查询、数据库结构或冻结迁移字节。

### New Capabilities

无。HTTP 调用流程文档是对既有 HTTP 契约的说明，不引入新的运行时行为。

### Modified Capabilities

- `migration-safety`：「已发布迁移不可改写」由只固定 v1.0.0 及以上正式 tag 与当前 initialFrozen 的「正式发布迁移不可改写」取代，删除源快照、各代历史 pin、历史限定授权追溯及“旧结构无写拒绝”场景；「升级保留已有数据」删除旧 v1/撤回 v2 的拒绝描述；「授权调整初始审计校验职责」「身份类型文本化限定授权」「已清理用户记录表限定授权」三条历史授权要求删除，其结构事实并入当前初始定义描述。
- `versioned-auth-storage`：「当前初始定义」取代「包内数据库定义」，删除对 v0.3.0/v0.4.0 pin 与冻结源的核验场景；「基线预检边界」取代「整合基线的兼容边界」，并连同「旧头像结构无写拒绝」「整数身份类型结构无写拒绝」一起删除旧结构反查预检要求，预检只保留 dirty 优先与版本超前拒绝；「头像列初始定义限定授权」「Google 身份注释并入当前初始定义」「身份类型文本列并入当前初始定义」三条历史授权要求删除，结构事实并入当前定义。
- `embedded-auth-package`：「生命周期和资料保留」取代「生命周期和资料兼容」，删除“临时结构无写拒绝”场景；「独立消费与 v1.0.0 公开能力」取代「独立消费和完整功能保留」，移除六个开发期兼容入口并声明 semver 约束。
- `session-authentication-context`：「来源数据生命周期」取代「来源数据生命周期与兼容」，删除对旧 v0.3.0 v1 与前一整合 v1 的拒绝场景，保留当前结构不接受无来源会话的要求。
- `verification-delivery`：「验证码专用公共类型」取代「验证码专用公共类型与源码兼容」，删除对 `SMSSender`、`EmailSender`、`Message` 别名的保留要求，正式名称与 `Deps` 字段名保持。

## Impact

- 代码：`internal/migrationcheck/`、`migrations/`（预检、基线文件、测试、testdata）、`compatibility_test.go`、`ids/`、`user/`、`user/sender/` 的 Deprecated 符号及其测试；新增 HTTP 流程示例测试。不修改 `0001_init.up.sql` / `0001_init.down.sql` 的字节、sqlc 查询或数据库结构；按 2026-10-31 扩展范围调整生成包位置并重新生成；新空 schema 安装仍为 version=1/dirty=false。
- 公开 Go API：删除 6 个 Deprecated 符号，属破坏性变化；没有任何已知调用方使用，接入方 ai-food 升级到 v1.0.0 时须同步改用正式名称。v1.0.0 起公开 API 受 semver 约束，后续破坏性变化须升 v2 模块路径。
- 门禁：`scripts/required-tests.txt` 删除退役用例；`scripts/check-migrations.sh`、`verify.sh`、恢复演练脚本继续执行，CI 工作流不变。检查工具静默忽略低于 v1.0.0 的 tag，因此工具改动与远端 tag 删除的先后顺序互不影响 CI。
- 文档与规则：`docs/` 全量重写，AGENTS.md 规则改写，`.test-output/` 下的旧版本消费缓存不受影响（已被 Git 忽略）。
- 接入方：删除远端 v0 tag 后，ai-food 的 go.mod 必须升到 v1.0.0；本变更不考虑 Go module proxy 缓存。
