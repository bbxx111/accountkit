# Design

## Context

动机与范围见 proposal.md。影响设计的现状：

- 迁移历史检查（`internal/migrationcheck`）当前固定四类对象：源快照两份 SQL 的哈希、v0.1.0/v0.2.0 的精确提交、v0.3.0 至 v0.6.0 各自的提交与 0001 哈希、当前 `initialFrozen`。它遍历 `git tag --merged HEAD` 中全部 `vX.Y.Z`，未 pin 的 tag 必须与当前基线逐字节相同。因此只要 v0 tag 仍可达且 pin 被删，检查就会失败；工具改动与 tag 删除必须解耦。
- 运行时预检 `checkBaseline` 已经是单一入口：dirty 优先，然后要求版本在当前源中存在，再依次调用流程/审计、会话上下文、资料、身份类型、purged_user 五组 catalog 反查。这五组存在的唯一原因是 0001 在版本号不变的情况下被多次改写。
- 现有 62 个迁移测试里，约 14 个文件复现旧结构；`scripts/required-tests.txt` 列有约 20 条对应条目。部分文件同时包含当前行为的用例。
- 文档层约 3900 行，其中 `docs/compatibility.md` 883 行、`docs/release-checklist.md` 199 行、`docs/migrations.md` 126 行主要是逐版本增量叙述。
- 公开 Go API 有 6 处 Deprecated 符号，均为从未正式使用过的旧入口的兼容壳。
- 用户已确认：历史版本与 ai-food 本地联调数据均不保留；不考虑 Go module proxy 缓存；v0 tag 与 Release 可删除，但作为单独确认的动作。

## Goals / Non-Goals

**Goals:**
- 让第一次接入 v1.0.0 的读者只需读当前状态，不需要理解 v0.x 演进。
- 迁移门禁从守护七代历史收缩为守护 v1.0.0 一代，同时保留面向未来的全部机制。
- 在 semver 承诺生效前完成破坏性清理，避免 v1.0.0 之后为此升 v2。
- 每一项删除都有明确的设计依据和覆盖核对，而不是为通过门禁删测试。

**Non-Goals:**
- 不重写 Git 提交历史，不 squash，不删除 `openspec/changes/archive/` 与 `docs/superpowers/plans/`。
- 不修改 `0001_init.up.sql` / `0001_init.down.sql` 字节、sqlc 查询、生成代码或数据库结构。
- 不实现任何候选功能；不为真实环境验证缺口补验证。
- 不在本变更内执行远端 tag/Release 删除或 v1.0.0 发布动作。

## Decisions

### D1 文档采用“当前状态”重写，不做逐篇归并

备选是把九份发布说明与各文档的历史章节合并、去重后保留。否决理由：增量叙述的问题不在重复，而在叙述方式本身；归并后读者仍要沿时间线读。重写后每份文档只回答“v1.0.0 是什么”。历史只保留一份 `docs/releases/pre-1.0-history.md`，内容限于每个 v0 tag 的提交、日期与一句话摘要，以及“这些版本未被正式使用、不提供从它们升级的路径、tag 已删除”的声明。`docs/release-checklist.md` 改为可重复使用的通用检查表，版本专属门禁只在发布准备变更里按版本追加。`docs/compatibility.md` 只保留功能对照表；开发版之间的升级章节和三节“历史提取索引”随源快照退役一并删除。

### D2 HTTP 调用流程文档：协议层状态机加 httptest 示例测试

新文档 `docs/http-client-flows.md` 用时序图和合成值的请求/响应片段覆盖四条流程：首次登录与 pending 补全；并发刷新协调（单一刷新者、等待者复用结果）、宽限期内重复刷新拿到同一 pair 的含义、重放失败后的退出；重新认证只替换 access、保留 refresh 的客户端状态；注销、`:revokeOthers`、解绑导致当前会话退出时的清理。文档只讲协议层，不替 iOS/Android/Web 决定存储方案，这与候选表原先“有目标客户端后”的前提一致。

为防止片段与实现漂移，新增 `examples/httpflows` 示例测试：用 httptest 挂载 `EndUserHandler()`，以合成发送器与 miniredis 走完上述流程，关键响应由测试断言。文档引用测试名而不是复制输出到 Output 注释，避免 token 等随机值导致示例测试不稳定。该测试进入 `scripts/required-tests.txt`。

备选是等目标客户端出现再写平台特定版本。否决理由见 proposal：平台中立版本现在就能写，且与文档重写同批完成避免二次对齐。

### D3 迁移历史检查只认 v1.0.0 及以上，低于 v1.0.0 的 tag 静默忽略

删除 `frozen`、`legacyReleases`、`publishedInitialReleases` 三张表及 `sourceBaseline` 逻辑。`releasedHistory` 过滤出的正式 tag 中，主版本为 0 的直接跳过，不参与任何比对。保留三项机制：当前 0001 与 `initialFrozen` 一致；每个可达正式 tag 内的迁移文件与基线逐字节相同且不可在后续 tag 中改写或消失；新迁移版本号必须大于已发布最大版本。

静默忽略是用户的选择：它让工具改动与远端 tag 删除完全解耦，本变更合入后 CI 立即为绿，删除动作可以在任意时机单独执行。代价是误建或恢复的 v0 tag 不会被检查发现；这一点由 AGENTS.md 规则“低于 v1.0.0 的 tag 已退役、不再创建”和发布检查表的人工核对承担，不由工具兜底。备选“v0 tag 存在即报错”能防旧 tag 复活，但会在合入与删除之间留下 CI 红色窗口，已否决。

### D4 运行时预检只保留 dirty 阻断与版本超前拒绝

v1.0.0 打 tag 后 0001 永久冻结，“version=1 等价于当前 0001 结构”由迁移引擎自身保证，catalog 反查失去对象。保留的两条都面向未来：dirty 优先沿用引擎哨兵；数据库版本不在当前源中（例如被更新的二进制迁过的库被旧二进制接管）继续返回 `ErrIncompatibleBaseline`，错误文案改为描述“版本超出当前源”。删除 `checkSessionContextBaseline`、`checkProfileAttributesBaseline`、`checkIdentityKindBaseline`、`checkPurgedUserBaseline` 及流程/审计的内联 SQL，删除对应三个 `*_baseline.go` 文件。`UnsafeReset` 沿用同一预检，因此只剩 schema 确认值这一道门。

备选是保留一条“version=1 时结构等于当前 0001”的通用结构比对。否决理由：它要维护一份与 0001 平行的期望结构，0002 之后还要按版本累加，维护成本高于收益；0001 冻结后它防的只是手工改库，不属于迁移安全契约。

### D5 源快照与原项目格式测试退役，格式保障由单元测试承担

`tests/testdata/source-baseline/` 证明的是“能读抽取前原项目产生的字节”。原项目没有真实数据，这一证明没有对象。密文、HMAC 摘要、refresh 哈希与 JWT 的格式本身仍在用，由 `pii`、`tokens`、`user/code` 包内的格式测试保障；本变更核对这些包已覆盖版本化 AES-GCM、HMAC 摘要匹配与密钥轮换，缺口在 verification.md 列明。`compatibility_test.go` 中读取 fixture 的用例删除，不读取 fixture 的用例按其实际目的归入对应包或保留。

### D6 旧结构测试按用例三分，删除条目逐条记录

处置单位是测试函数而不是文件。三类：只构造旧结构并断言被拒绝的删除；证明当前安装、重复 Up、并发首次部署、锁等待与取消、dirty 阻断、latest+1 升级与失败 probe、UnsafeReset 的保留；混合用例拆出当前行为部分后改为针对单一基线的断言。`migrations/testdata/legacy_integrated_v1.sql` 随旧结构用例删除，`0002_success.up.sql` / `0002_failure.up.sql` 保留。

`scripts/required-tests.txt` 删除退役用例的条目，并在 verification.md 附一张表：每条删除的测试名、所属类别、替代覆盖（若有）。这是对 AGENTS.md“不为逃避门禁删测试”规则的正面回应：删除的依据是基线重置这一设计决策，覆盖核对可审计。

### D7 Deprecated 符号直接删除，不再留一版过渡

6 个别名符号都是旧 Go 入口的别名或转发，没有任何已知调用方。v1.0.0 之前删除成本为零，之后删除需要 v2。公开 API 盘点（D8）另取消导出 `user.KeyBackfillBatchSize` 与 `user.PurgeBatchSize`（维护任务内部批量参数，包外无引用，无替代名），因此从公开面移除的符号共 8 个：6 个别名加这 2 个常量。规格层面同步删除 `verification-delivery` 中保留别名的要求；`ids.IdentityFlow` 等三处在规格中没有保留要求，只需删代码、测试与文档迁移说明。

### D8 公开 API 盘点是一次有产出的通读，不是重构许可

用 `GOWORK=off go doc -all` 导出根包、`user`、`user/sender`、`user/code`、`user/idp`、`httpapi/enduser`、`httpapi/admin`、`httpapi/authn`、`httpapi/apierror`、`tokens`、`audit`、`maintenance`、`anonymize`、`migrations`、`ids`、`enum`、`pii`、`session/*` 的公开面，逐包记录三类发现：命名与既有约定不一致、不应导出的符号、文档缺失。每条发现要么在本变更内处理，要么写入 design 的附录“接受的 API 现状”并说明理由。盘点不引入新能力，不改行为；处理项若涉及规格要求，走对应规格的 delta。

### D9 AGENTS.md 规则改写范围

「迁移安全」节替换为：当前 0001 自 v1.0.0 tag 起字节冻结；后续结构变更从 0002 递增，已发布版本不可修改、删除、重编号或 squash；低于 v1.0.0 的 tag 已退役、不再存在也不再承认；历史检查只覆盖 v1.0.0 及以上可达正式 tag 与当前 initialFrozen。删除“tests/testdata/source-baseline 冻结”“已发布 v0 tag 不可移动”及全部逐轮“直接改 0001”授权记录。其他节中引用源快照、旧 tag 或 `ErrIncompatibleBaseline` 旧语义的句子同步修正。「开发与验证命令」与「Git 与 OpenSpec 工作流」不变。

### D10 执行顺序与不可逆点

1. 本变更在任务分支完成全部代码、测试、文档与规则改动；由于 D3 静默忽略 v0 tag，本地与 CI 在 v0 tag 仍存在的检出上即可完整验证。
2. 合入 develop，远端 CI 正常通过。
3. 用户单独确认后，删除远端九个 v0 tag、对应 GitHub Release 与本地 tag。这是唯一不可逆点，与 CI 状态无关。
4. 启动 `prepare-v1-0-0-release` 变更：发布说明、候选、CI、tag、Release、远端消费验证；v1.0.0 tag 打上后 0001 正式冻结。

### D11 合成种子保留

原项目源快照（`tests/testdata/source-baseline/`）整体退役，但其中的合成 fixture 仍被恢复演练、宿主契约与实例隔离测试用作种子数据，因此没有随目录删除，而是移到 `tests/testdata/synthetic/fixture.json`，由根包 `synthetic_fixture_test.go` 读取（`seedSyntheticAccount`、`snapshotSource` 等辅助函数随之改用正式名称）。这与 D5 的“证明能读原项目字节”不同：它只是合成测试数据，不再承担历史格式证明。“历史格式”这一证明（原 `TestHistoricalFormatsOnInitialSchema`）被丢弃，格式覆盖改由包内单元测试固定：`pii.TestStoredFormatsSurviveKeyRotation`（AES-GCM 与 HMAC 摘要的版本化格式及轮换）、`user.TestRefreshTokenHashFormat`（refresh 令牌 SHA-256 存储格式）以及 `tokens` 包的 kid 测试（`TestSignParseRoundTrip`、`TestParseAcceptsOldKeyVersionDuringOverlap`）。这是对计划中“直接删除 fixture”做法的偏离，原因是仍有三类测试依赖该数据。

## Risks / Trade-offs

- [v0 tag 被误建或恢复后工具不报错] → D3 已接受此代价；AGENTS.md 规则禁止创建低于 v1.0.0 的 tag，发布检查表加入“远端不存在 v0 tag”的人工核对项。
- [删除旧结构测试后出现覆盖缺口] → D6 的逐条表格列出替代覆盖；保留用例必须仍覆盖新安装、重复 Up、并发、锁、dirty、latest+1 与 UnsafeReset。
- [删除源快照后格式回归无人察觉] → D5 核对 `pii`、`tokens`、`user/code` 的格式测试覆盖，缺口在本变更内补单元测试。
- [v1.0.0 之后发现公开 API 命名问题] → D8 盘点在 tag 前完成；接受项显式记录，不留“以后再改”。
- [ai-food 构建失败] → 删除 v0 tag 后其 go.mod 必须升到 v1.0.0；在删除动作前通知接入方，并在 v1.0.0 发布说明列出 6 个被删符号的替换名。
- [文档重写遗漏交叉链接或与规格矛盾] → `openspec validate --all --strict`、链接检查与一次独立审查作为验收项；功能对照表与主规格逐项核对。
- [HTTP 流程示例测试因随机凭证不稳定] → 测试断言结构与状态机而非具体 token 值；文档不复制随机值。

## Migration Plan

对库的使用者：没有数据库迁移；新空 schema 安装仍为 version=1/dirty=false。升级到 v1.0.0 的 Go 调用者：`user.KeyBackfillBatchSize` 与 `user.PurgeBatchSize` 已取消导出且无替代（内部批量参数，不应被读取）；把 `sender.SMSSender`/`EmailSender`/`Message` 改为 `SMSCodeSender`/`EmailCodeSender`/`CodeMessage`，`ids.IdentityFlow` 改为 `ids.SignInCompletionFlow`，`Deps.IdentityFlowContext` 改为 `SignInCompletionFlowContext`，`CleanupIdentityFlows` 改为 `CleanupSignInCompletionFlows`。

回退：tag 删除前，本变更可整体 revert 恢复旧门禁与文档。tag 删除后不可回退，这是用户确认的决定；若之后需要历史 SQL，只能从提交历史读取，不再作为受保护基线。

## Open Questions

- 已决定：`docs/releases/pre-1.0-history.md` 列出每个 v0 tag 的完整提交 SHA，便于从提交历史追溯。
- 已决定：`examples/httpflows` 只覆盖库的终端用户 HTTP 面，不覆盖 accountsvc 独有的内省与已清理用户拉取，它们不属于库的 HTTP 契约，由 accountsvc 文档说明。

## 附录：接受的 API 现状

盘点范围为模块内 `internal/`、`examples/`、`cmd/` 以外的全部 33 个包（`GOWORK=off go doc -all` 输出位于被忽略的 `.test-output/api-inventory/`）。只列实际发现；“修改”为本变更内已处理，“接受”为保持现状，“待用户决定”为可能值得在 v1.0.0 前做、但属于破坏性改名/取消导出或牵涉规格与包外引用、超出本次盘点许可的项。

| 符号 | 包 | 类别 | 处理 | 理由 |
|---|---|---|---|---|
| `KeyBackfillBatchSize`、`PurgeBatchSize` | `user` | 不应导出 | 修改 | 维护任务内部批量参数，Go 代码、示例、测试与文档均无包外引用，已取消导出；`PurgeDueUsers` 注释改写为“每批 100 个账号”。 |
| 别名类型与 `Actor*` 常量组、`Noop.Record`、`Memory.Record`、`Store`、`StoreOptions`、`NewStoreWithOptions`、`ListByResource`、`DeleteOlderThan`、`ErrConflict`、`ErrInvalid`、`RetentionBatchSize` | `audit` | 文档缺失 | 修改 | 补充注释，说明别名指向与存储语义（按 `occur_time` 分批清理、来源与命名空间范围）。 |
| `NewRecorder`、`Recorder.Record`、`Recorder.Stats`；`MaxEventBytes`、`MaxResources` | `audit/authentication`、`audit/event` | 文档缺失 | 修改 | 补充构造、失败不外抛语义和校验上限注释。 |
| `UserState`、`RevokeReason`、`CodePurpose`、`ActorKind`、`Result`、`EventType`、`FlowState` 及 `ParseRevokeReason`、`ParseEventType`、`ParseFlowState`、`ParseClientType`、`ParseDeviceType` | `enum` | 文档缺失 | 修改 | 补充类型用途（对应存储列）与 Parse 只接受精确 UPPER_SNAKE 名的注释。 |
| 支付宝/QQ/微信 `SubjectMode` 常量组、`Deps.AuditReader` | 根包 | 文档缺失 | 修改 | 常量组补注释；`AuditReader` 原英文注释未说明须与外部 `Audit` 同时注入，改为中文并补全约束。 |
| `ErrSignInCompletionInvalid`、`ErrSignInAlreadyCompleted`、`ErrProfileAttributesNotConfigured`、`ProfileAttributesValidationError.Error/Is`、`IdentityPolicy.Allows/Complete/EnabledKinds/RequiredKinds`、`Service.SignInWithCode` | `user` | 文档缺失 | 修改 | 补充错误映射与策略方法注释；`SignInWithCode` 的 godoc 混入了一段描述包内包装器的英文说明，已与方法注释分开。 |
| `Kind` 常量组、`ErrDestructiveOperation`、`ReasonTokenMissing/Invalid`、`Error.Error`、`RateLimitedError.Error`、`Disabled` 三个方法、`Alipay.Kind/Verify`、`QQ.Kind`、`Weibo.Kind`、支付宝/QQ 模式常量组 | `ids`、`migrations`、`httpapi/authn`、`httpapi/apierror`、`user/code`、`user/sender`、`user/idp` | 文档缺失 | 修改 | 补充注释；`ids` 常量组注明 `AuditEvent`（`e`）仅用于历史审计 ID 格式。 |
| `MapLegacy`、认证快照 `legacy_event_id` | `audit/authentication` | 开发期兼容残留 | 待用户决定 | 开发期审计兼容路径，按控制方裁定保留进入 v1.0.0；`docs/compatibility.md`、`docs/migrations.md` 与 `versioned-auth-storage` 规格仍引用，删除需规格 delta。 |
| `AuditEvent`（`e`） | `ids` | 开发期兼容残留 | 待用户决定 | 当前审计事件 ID 为 UUIDv7，`e` 前缀只剩历史格式追溯；仅 `ids` 外部测试包引用，删除属破坏性变更。 |
| `ErrExhausted` | `user/code` | 开发期兼容残留 | 待用户决定 | 注释写明“为源码兼容保留”，`code` 包已不再返回它；`user` 与 `httpapi/enduser` 仍有对应的映射分支，删除需同步清理。 |
| `NewWeChat` | `user/idp` | 开发期兼容残留 | 待用户决定 | 早期仅 OAuth、固定 UNIONID 的构造器，库内装配已改用 `NewWeChatWithConfig`；仅外部测试包引用。 |
| `Store.ListByUser` | `audit` | 不应导出 | 待用户决定 | 注释自称本地便捷方法、集成应使用 `Reader`；仅 `user` 外部测试包使用。 |
| `RetentionBatchSize`、`IsUniqueViolation` | `audit`、`user` | 不应导出 | 待用户决定 | 内部批量参数与 pgx 唯一冲突判定，但被同目录外部测试包（`audit_test`、`user_test`）引用；取消导出需配套测试导出垫片，超出本次许可。 |
| `PurgeCoveredTables` | `user` | 不应导出 | 待用户决定 | 导出的可变 map，宿主可改写库内覆盖登记；`docs/compatibility.md` 把它作为宿主覆盖检查入口，改为返回副本的函数属破坏性变更。 |
| `Repo`、`NewRepo`、`Repo.Q/WithTx/WithTxRaw`、函数 `CheckKeyVersions`，以及 `Deps` 中的 `*Repo`、`*code.Store`、`*grace.Cache`、`*revocation.Set` | `user` | 不应导出 | 待用户决定 | 使 sqlc 生成的 `*db.Queries` 成为公开签名的一部分，v1 后修改查询即可能构成破坏性变更；`internal/accountsvc` 依赖 `NewRepo` 与 `CheckKeyVersions`，收窄需另行设计装配入口。 |
| `user/db`、`audit/db` 包 | — | 包结构 | 待用户决定 | sqlc 生成包可被宿主直接导入，生成命名也不完全符合 Go 缩写约定（如 `FlowIds`、`ProofUserIds`）；移入 `internal/` 受上一行牵连。 |
| `httpapi/jsonbody`、`httpapi/reqid` 包 | `httpapi` | 包结构 | 待用户决定 | 两个 HTTP 面共用的内部工具包，`httpapi` 之外无引用，文档也未将其作为宿主接口；移入 `internal/` 属包路径级破坏性变更，不在符号级许可内。 |
| `LegacyChineseRenderer` | `user/sender/smtp` | 命名不一致 | 待用户决定 | 实为 `smtp.New` 的默认渲染器，名称中的 Legacy 是开发期措辞；README 与兼容文档引用，改名属破坏性变更。 |
| `Pair.AccessExpiresAt`、`Pair.RefreshExpiresAt` | `session/grace` | 命名不一致 | 待用户决定 | 时间字段约定为 `<verb>_time`（Go 名应为 `…ExpireTime`），JSON 标签是 Redis 内部短名不受影响；`user` 包引用。 |
| 构造器与校验方法：`NewApple/NewGoogle/NewFacebook/NewOIDC` 不返回 error 而 `NewAlipay/NewQQ/NewWeibo/NewWeChatWithConfig` 返回；`AppleOptions` 平铺字段而 `AlipayOptions` 内嵌 `Config`；`VerifyApple/VerifyGoogle/VerifyWeChat/VerifyIDToken` 各自命名 | `user/idp` | 命名不一致 | 待用户决定 | 各提供方分批引入形成的不同风格；统一须改签名，影响自行装配提供方的宿主。 |
| `SessionRetention` | `user` | 不应导出 | 接受 | 无包外引用，但它是 `CleanupSessions` 文档引用的固定保留期，宿主可据此说明会话记录可查期限。 |
| `Alphabet`、`TSIDLength`、`MaxResources`、`HTTPStatus`、`StatusInternal` | `ids`、`audit/event`、`httpapi/apierror` | 不应导出 | 接受 | 无包外引用，但分别描述 ID 格式、事件校验上限与错误体状态映射，属宿主可依赖的契约。 |
| `IdentityPolicy.RequiredKinds/ValidateCapabilities`、`NonceRegistry.RegisterScoped`、`OIDC.VerifyIDToken`、`Auth.RunMaintenanceOnce` | `user`、`user/idp`、根包 | 不应导出 | 接受 | 无包外调用，但所属类型面向宿主：策略经 `Service.IdentityPolicy` 返回，自定义提供方可复用 nonce 登记，`VerifyIDToken` 与 Apple/Google 同类方法对称，`RunMaintenanceOnce` 是宿主一次性作业入口。 |
| `Down` | `migrations` | 不应导出 | 接受 | AGENTS.md 要求保留签名、无 I/O 返回 `ErrDestructiveOperation`。 |
| 枚举值短前缀（`PurposeSignIn`、`UserActive`、`ClientWeb`）与全名零值（`…Unspecified`）；`ActorKind`、`Result`、`CodePurpose` 无 Parse 函数 | `enum` | 命名不一致 | 接受 | 规则本身一致且已被全仓与宿主广泛使用；缺少的 Parse 函数没有 API 边界需求，补充属新增导出。 |
| `enum.ActorUser` 等与 `audit.ActorUser`（`event.ActorType`）同名不同类型；`observability.Purpose/Result` 与 `enum` 同名词 | `enum`、`audit`、`observability` | 命名不一致 | 接受 | 分属认证领域、通用审计和观测标签三套词汇，包名限定后无歧义。 |
| 根 `Deps.SMSSender/EmailSender`、`Config.AccessTokenTTL` 与 `user.Deps.SMS/Email/AccessTTL`；`idp` 各 `Options.HTTP` 与 `aliyun.Config.HTTPClient` | 根包、`user`、`user/idp`、`user/sender/aliyun` | 命名不一致 | 接受 | 门面与领域层、各子包分别定义，已被宿主与文档使用；统一属破坏性变更且无语义收益。 |
| `Claims.IssuedAt/ExpiresAt/JTI` | `tokens` | 命名不一致 | 接受 | 对应 JWT 注册 claim（iat/exp/jti），JWT 标准字段不按本地 `<verb>_time` 偏好改名。 |
| `ProviderDeletionResult.PurgeTime`、`idp.Identity.ExpireTime` 以零值表示缺失，不同于 `AdminUser.PurgeTime *time.Time` | `user`、`user/idp` | 命名不一致 | 接受 | 注释已写明零值语义；改为指针属签名变更。 |
| `StoreOptions = authentication.Options` | `audit` | 命名不一致 | 接受 | Store 与认证映射共用同一对 Source/Namespace，别名保证二者一致；文档与 `examples/audit` 使用。 |
| `NewRunner`/`NewRunnerWithObserver`；`enduser` 的可选能力接口（`CurrentSessionService` 等） | `maintenance`、`httpapi/enduser` | 命名不一致 | 接受 | 用并列构造器和可选接口承载可选依赖，是 v1 后不破坏既有实现的扩展方式。 |
| `Task` 无 `cleanup_purged_users` 分类 | `observability` | 命名不一致 | 接受 | 有意归入 `TaskOther`（根包观测测试已断言）；新增分类属新增导出与指标标签变更。 |
| `observability`、`observability/otel`、`observability/prometheus`、`migrations` 部分及 `audit` 多处英文注释 | 多包 | 文档缺失 | 接受 | 内容完整，仅语言与其余包不同；统一语言不改变 API，可作后续纯文档维护。 |
| 枚举值常量、`String/Valid` 方法与多数导出结构字段无逐项注释 | 多包 | 文档缺失 | 接受 | 由类型注释、行尾注释和字段名自解释，逐项补注收益低。 |
