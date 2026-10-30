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

6 个符号都是旧 Go 入口的别名或转发，没有任何已知调用方。v1.0.0 之前删除成本为零，之后删除需要 v2。规格层面同步删除 `verification-delivery` 中保留别名的要求；`ids.IdentityFlow` 等三处在规格中没有保留要求，只需删代码、测试与文档迁移说明。

### D8 公开 API 盘点是一次有产出的通读，不是重构许可

用 `GOWORK=off go doc -all` 导出根包、`user`、`user/sender`、`user/code`、`user/idp`、`httpapi/enduser`、`httpapi/admin`、`httpapi/authn`、`httpapi/apierror`、`tokens`、`audit`、`maintenance`、`anonymize`、`migrations`、`ids`、`enum`、`pii`、`session/*` 的公开面，逐包记录三类发现：命名与既有约定不一致、不应导出的符号、文档缺失。每条发现要么在本变更内处理，要么写入 design 的附录“接受的 API 现状”并说明理由。盘点不引入新能力，不改行为；处理项若涉及规格要求，走对应规格的 delta。

### D9 AGENTS.md 规则改写范围

「迁移安全」节替换为：当前 0001 自 v1.0.0 tag 起字节冻结；后续结构变更从 0002 递增，已发布版本不可修改、删除、重编号或 squash；低于 v1.0.0 的 tag 已退役、不再存在也不再承认；历史检查只覆盖 v1.0.0 及以上可达正式 tag 与当前 initialFrozen。删除“tests/testdata/source-baseline 冻结”“已发布 v0 tag 不可移动”及全部逐轮“直接改 0001”授权记录。其他节中引用源快照、旧 tag 或 `ErrIncompatibleBaseline` 旧语义的句子同步修正。「开发与验证命令」与「Git 与 OpenSpec 工作流」不变。

### D10 执行顺序与不可逆点

1. 本变更在任务分支完成全部代码、测试、文档与规则改动；由于 D3 静默忽略 v0 tag，本地与 CI 在 v0 tag 仍存在的检出上即可完整验证。
2. 合入 develop，远端 CI 正常通过。
3. 用户单独确认后，删除远端九个 v0 tag、对应 GitHub Release 与本地 tag。这是唯一不可逆点，与 CI 状态无关。
4. 启动 `prepare-v1-0-0-release` 变更：发布说明、候选、CI、tag、Release、远端消费验证；v1.0.0 tag 打上后 0001 正式冻结。

## Risks / Trade-offs

- [v0 tag 被误建或恢复后工具不报错] → D3 已接受此代价；AGENTS.md 规则禁止创建低于 v1.0.0 的 tag，发布检查表加入“远端不存在 v0 tag”的人工核对项。
- [删除旧结构测试后出现覆盖缺口] → D6 的逐条表格列出替代覆盖；保留用例必须仍覆盖新安装、重复 Up、并发、锁、dirty、latest+1 与 UnsafeReset。
- [删除源快照后格式回归无人察觉] → D5 核对 `pii`、`tokens`、`user/code` 的格式测试覆盖，缺口在本变更内补单元测试。
- [v1.0.0 之后发现公开 API 命名问题] → D8 盘点在 tag 前完成；接受项显式记录，不留“以后再改”。
- [ai-food 构建失败] → 删除 v0 tag 后其 go.mod 必须升到 v1.0.0；在删除动作前通知接入方，并在 v1.0.0 发布说明列出 6 个被删符号的替换名。
- [文档重写遗漏交叉链接或与规格矛盾] → `openspec validate --all --strict`、链接检查与一次独立审查作为验收项；功能对照表与主规格逐项核对。
- [HTTP 流程示例测试因随机凭证不稳定] → 测试断言结构与状态机而非具体 token 值；文档不复制随机值。

## Migration Plan

对库的使用者：没有数据库迁移；新空 schema 安装仍为 version=1/dirty=false。升级到 v1.0.0 的 Go 调用者把 `sender.SMSSender`/`EmailSender`/`Message` 改为 `SMSCodeSender`/`EmailCodeSender`/`CodeMessage`，`ids.IdentityFlow` 改为 `ids.SignInCompletionFlow`，`Deps.IdentityFlowContext` 改为 `SignInCompletionFlowContext`，`CleanupIdentityFlows` 改为 `CleanupSignInCompletionFlows`。

回退：tag 删除前，本变更可整体 revert 恢复旧门禁与文档。tag 删除后不可回退，这是用户确认的决定；若之后需要历史 SQL，只能从提交历史读取，不再作为受保护基线。

## Open Questions

- `docs/releases/pre-1.0-history.md` 是否列出每个 v0 tag 的提交 SHA。倾向列出，便于从提交历史追溯；不影响规格与任务拆分，实施时决定。
- `examples/httpflows` 是否同时覆盖 accountsvc 独有的内省与已清理用户拉取。倾向不覆盖，它们不属于库的 HTTP 契约；若覆盖则放 accountsvc 文档，不改变本变更范围。
