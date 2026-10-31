# consolidate-pre-1-0-history 验收记录

## 2026-10-31 追加实施：当前状态

本节记录用户授权“把 13 项逐项评估后的必要 API 调整也纳入本变更”后的收尾。下方原验收作为历史证据保留，其中“13 项待决定”、遗留小项及非 root 权限测试未验证的描述以本节结果为准；历史日志不代表本次代码已验证。

- 工作基点：`bf2f0ff`，分支 `chore/consolidate-pre-1-0-history`；本次修改保持未提交。完整分支审查以本地 `develop`（`fb00c89`）到当前工作区为范围。
- 13 项已逐项裁定：12 项包含必要修改，1 项保留协议专用 IdP 构造器/Verify 差异。保留不透明 `user.Repo/NewRepo`、具体装配依赖和两种 `CheckKeyVersions`；隐藏原始查询与事务方法。完整理由见 [design D12](design.md#d12-2026-10-31-用户授权的-api-收尾)，调用替换见 [兼容指南](../../../docs/compatibility.md)。
- 原遗留测试及文档小项均已处理；三个实施步骤独立审查均通过。全分支最终审查发现的升级测试覆盖缺口及 README 请求示例错误均已修复并独立复核关闭，规格与代码质量均为 Approve。29/29 项任务完成。
- 已同步五个相关主规格；没有归档。冻结 `0001`、生产查询 SQL、认证及领域事务行为未改。

## 追加实施的验证证据

环境为 `tests/Dockerfile` 对应的 Linux 镜像 `accountkit-verify:c1h`，Go 1.26.5、`GOWORK=off`、PostgreSQL 17、Redis 7。完整演练使用新建且执行前核对为空的两个独立库 `c1h_followup_20261031_src` / `c1h_followup_20261031_dst`；原有恢复库未重用。

演练完成后核对两个新库均无连接，仅删除这两个本次创建的库；既有测试容器、普通测试库及此前恢复库均保留。备份和三阶段日志仍在被忽略的 `.test-output/recovery.ZhJ6Yu/`，后续重跑需要另建独立空库。

| 检查 | 实际结果 |
|---|---|
| 遗留测试收尾 | migrations/pii/httpflows、宿主回调、会话来源及恢复快照定向真实 PostgreSQL 测试通过，零 skip；迁移拒绝、HTTP 刷新及隔离相关定向 race 通过；相关 vet 通过。dirty/超前拒绝均重复核对 catalog、业务记录、version/dirty，并显式核验两层锁与迁移连接释放。 |
| API 符号行为 | 可变覆盖登记测试在旧 map 实现上真实失败，副本函数实现后通过；手工旧 `ae/re` 加密缓存及审计旧快照读/清理测试通过。Task 2 中间态全仓非 race 测试 1163 个顶层 pass、31 skip、0 fail，因未配置真实 Redis，不能用其代替完整门禁；最终结果见后。 |
| 存储与 HTTP 边界 | 独立消费者模块 12/12 编译断言通过：公开装配可编译，3 个旧 Repo 方法不可用，4 个新 internal 包拒绝导入，4 个旧路径不存在。每项核对准确编译器诊断，没有把下载失败作为拒绝证明。HTTP/audit 179 与领域/迁移/accountsvc 40 个顶层测试通过，零 fail/skip。 |
| 生成内容与构建 | sqlc 1.31.1 重新生成，六个生成文件与移动前逐字节相同；两个 HTTP 实现文件逐字节相同。`scripts/check-generated.sh`、全仓 build/vet 通过。首次 vet 的测试 import 遗漏已修复，失败日志保留，不计为通过。 |
| 规格及协议 | `openspec validate --all --strict`：41 pass、0 fail；`node scripts/check-openapi.cjs`：enduser 24、admin 11 操作及会话上下文边界通过。 |
| 文档引用 | 检查 450 个 Markdown 文件、662 条本地链接：源文件/新增链接无断链；另有 197 条指向被忽略历史证据的链接在本机不可用，均在本次修改前已存在。不宣称历史原始材料全部可取。 |
| 完整 Linux 库门禁 | `bash scripts/verify.sh` 通过：迁移历史、build、vet、全仓 race、981 项必需测试及恢复演练。race 顶层 1192 pass、0 fail、2 skip；含子测试 3552 pass。日志 `c1h-followup-verify.log` 与 `tests.jsonl`。 |
| accountsvc 进程门禁 | `bash scripts/verify-accountsvc.sh` 通过：build、vet、进程/race 及 62 项必需门禁；顶层 203 pass、0 fail、1 skip，含子测试 820 pass。日志 `c1h-followup-accountsvc.log` 与 `accountsvc-tests.jsonl`；两个完整脚本的串行 runner 最终退出码 0。 |
| 新恢复演练 | pg_dump/pg_restore 17.11；`recovery.ZhJ6Yu/` 下 seed、verify、source-unchanged 的 `TestRecoveryFixture` 各 1 pass、0 skip。校验恢复后来源、客户端 JSON、流程密文、资料精确值、账号时间、access/refresh、匿名化及源库不变。 |
| 非 root 权限补验 | 编译 accountsvc 测试二进制，以 UID/GID 65534:65534 在 Linux 容器运行 `TestProfileAttributesUnreadableFile`：1 pass、0 skip，退出 0。日志 `c1h-followup-permissions.log`。首次未引用的 PowerShell 参数被拆分，未执行测试；修正引用后通过。 |
| 审查后的升级测试补验 | `go test -race -json -count=1 ./migrations`：41 个顶层 pass、129 个含子测试 pass、0 fail/skip；`go vet ./migrations` 通过，串行 runner 退出 0。日志 `c1h-followup-upgrade-race.jsonl`、`c1h-followup-upgrade-vet.log`。全仓现存 Go 文件 gofmt 检查无输出，`git diff --check` 通过。 |

完整脚本里的 skip 仅为普通套件预期跳过的 `TestRecoveryFixture` 和 root 可读取权限受限文件时的 `TestProfileAttributesUnreadableFile`；分别由上述恢复三阶段和非 root 用例补验，未修改测试的 skip 条件或必需清单。统计保存于 `.test-output/c1h-followup-summary.json`。完整脚本运行后新增的升级用例只修改测试文件；其最终单独迁移包 race/vet 结果另记，不能把先前全仓运行冒称为已执行后来补充的断言。

最终审查修复：`TestUpgradePreservesDataAndIsRepeatable` 补齐八表非空基线，在当前重复 Up、首次及重复 latest+1 后逐次验证原数据、列/约束/索引、资料精确数字、账号与观察时间。新增 HMAC 检索/身份解密、原 JWT 与未吊销会话关联、refresh 哈希正负例、有效来源、真实客户端 codec 及流程 AAD 解密断言；流程 payload 明确为存储层合成数据，不冒充完整领域登录补全。catalog helper 默认排除规则不变，只有该用例额外排除新增 probe 表。README 的 completeSignIn 示例改为实际 `kind/credential`。两项均经最终审查复核，无未关闭发现。

原始材料集中在被忽略的 `.test-output/`：`c1h-followup-task2-{red,green,suite,vet,final-unit}.log`、`task3-boundary/{red,green}/`、`task3-validation/`、`c1h-followup-links.json`；逐步报告、差异与独立审查在 `.superpowers/sdd/2026-10-31-consolidate-pre-1-0-history-followup/`。这些为本地工作材料，验收结论以本文件为持久记录。

追加实施中的测试变更与替代覆盖：

- 删除 `TestAuthenticationLegacyRejectsIncompatibleOptionalFields`：仅针对已退役 `MapLegacy` 写转换器；在线映射的可选字段/actor 验证与旧快照读取覆盖保留。
- 原 `TestAuthenticationLegacyAdminForbiddenPreservesResources` 改为 `TestAuthenticationLegacyAdminForbiddenSnapshotReadable`，用手工持久化事件验证旧读取；保留在线 ADMIN_FORBIDDEN 不推测请求目标的断言。
- 两个 SMTP renderer 测试仅随正式名称改名；`ErrExhausted` 不可达错误映射行移除，真实错误尝试计数、最终 `ErrInvalid`/后续 `ErrExpired` 及限流测试保留。
- 新增必需测试 `TestPurgeCoveredTablesReturnsIndependentSnapshots`、`TestGetPreviouslyEncodedPair`；HTTP helper 必需测试路径随包移动。必需清单从 980 变为 981 项（新增 2、退役 1，其余改名/移包），未以放宽 skip 或删门禁处理失败。
- 原始 Repo 事务测试改经仅存在于 `export_test.go` 的桥接调用同一私有实现，保留默认隔离与领域 READ COMMITTED 测试，并补 panic 回滚/连接归还场景；没有复制另一套事务实现。

## 追加实施的边界与后续动作

- 未提交、推送、合并、运行在线 GitHub Actions、删除远端 v0 tag/Release、发布 v1.0.0 或归档；这些不是本次本地实施完成的隐含动作。
- `docs/features.md` 的真实外部环境验证缺口未补做；Windows 权限语义未验证。API 调整已有迁移说明，不据本次盘点声称所有公开 API 已正式冻结。
- 下方历史记录中的延期小项与 13 项待决限制已被本次授权和实施取代；原测试删除附表及其他历史裁定保留用于追溯。

## 历史验收（追加实施之前）

以下为原记录，所有“当前”“本次”及未验证项均指追加实施之前的工作状态。

### 原当前状态

- 日期：2026-10-31
- 分支：`chore/consolidate-pre-1-0-history`
- 验证时的 HEAD：`7331b44`（docs(agents): drop the retired source-snapshot clause）；本记录与 `design.md`、`tasks.md` 的更新随下一次提交落地。
- 结论：任务 1 至 8 已实施并逐项审查；本地门禁与 Linux 完整验证（race、必需测试门禁、真实 pg_dump/pg_restore 恢复演练、accountsvc 进程测试）均通过，无失败项。未验证项见文末，其中远端 v0 tag/Release 删除未执行，v1.0.0 发布准备另起变更。

## 实际运行的检查

下表为本次实际执行的命令与结果。“本机”指 Windows 宿主（Git Bash），“容器”指由 `tests/Dockerfile` 构建的 `accountkit-verify:c1h` 镜像，经 `--network accountkit-c1h-net` 连接一次性 PostgreSQL 与 Redis 容器（`SERVER_TEST_DB_DSN`、`ACCOUNTSVC_TEST_REDIS_URL`、`ACCOUNTKIT_RECOVERY_SOURCE_DSN`、`ACCOUNTKIT_RECOVERY_TARGET_DSN`，恢复源/目标为运行前确认表数为 0 的 `recovery_src`、`recovery_dst`）。

| 检查 | 命令 | 位置 | 结果 |
|---|---|---|---|
| OpenSpec 校验 | `openspec validate --all --strict` | 本机 | 退出码 0，41 项通过、0 失败 |
| 生成代码一致 | `bash scripts/check-generated.sh`（sqlc v1.31.1） | 本机 | 退出码 0 |
| 迁移历史 | `bash scripts/check-migrations.sh` | 本机 | 退出码 0，输出 `Migration history is immutable; new versions are strictly increasing`；本机仍带 v0 tag，验证 D3 静默忽略 |
| OpenAPI | `node scripts/check-openapi.cjs` | 本机 | 退出码 0（enduser 24 个操作、admin 11 个操作，会话上下文边界通过） |
| gofmt | `gofmt -l $(git ls-files '*.go')` | 本机 | 无输出（`.test-output/` 含大量模块缓存，故不用 `gofmt -l .`） |
| Markdown 相对链接（任务 15 脚本） | `python -I task15_linkcheck.py .` | 本机 | 222 条链接、15 个文件，0 问题 |
| Markdown 相对链接（全仓库扩展版） | `python -I linkcheck_all.py .`（遍历全仓库，跳过 `.test-output`、`node_modules`、`.superpowers`、`.git`，不检查围栏代码块内示例） | 本机 | 674 条链接、494 个文件，0 问题 |
| 完整库验证 | `bash scripts/verify.sh`（check-migrations、build、vet、`go test -race -json -count=1 ./...`、`internal/testgate scripts/required-tests.txt`、`verify-recovery.sh`） | 容器 | 退出码 0；日志 `.test-output/c1h-verify.log` |
| accountsvc 进程验证 | `bash scripts/verify-accountsvc.sh`（build、vet、race 测试、`internal/testgate scripts/accountsvc-required-tests.txt`） | 容器 | 退出码 0；日志 `.test-output/c1h-verify-accountsvc.log` |

补充证据：

- `verify.sh` 的 race 测试：`-json` 输出中共 3551 个 pass（含子测试），其中顶层测试 1191 个 pass，0 个 fail；2 个 skip，见下。`scripts/required-tests.txt` 当前 980 行，`testgate` 在脚本中以 `set -e` 运行，退出码 0 即必需测试全部存在且通过。为交叉核对，另在本机对同一日志重放 `go run ./internal/testgate scripts/required-tests.txt`，退出码 0。
- `verify-accountsvc.sh`：顶层测试 203 个 pass、0 个 fail、1 个 skip；`scripts/accountsvc-required-tests.txt`（62 行）的 `testgate` 退出码 0（本机重放同样为 0）。
- 恢复演练：脚本输出 `Recovery drill passed: …`；三阶段证据保留在 `.test-output/recovery.Pl0gZM/`，`seed.jsonl`、`verify.jsonl`、`source-unchanged.jsonl` 中 `TestRecoveryFixture` 各为 1 个 pass、0 个 skip。使用 pg_dump/pg_restore 17.11。普通套件中 `TestRecoveryFixture` skip 是预期（仅由恢复脚本强制执行）。
- skip 清单（`verify.sh` 共 2 个顶层，accountsvc 脚本 1 个）：`TestRecoveryFixture`（预期，见上）与 `internal/accountsvc` 的 `TestProfileAttributesUnreadableFile`。后者在测试内以 `current user can read permission-denied files` 跳过，因为容器内以 root 运行；它不在任何必需测试清单中，未改动清单或测试来掩盖。
- `recovery_src`/`recovery_dst` 已被本次演练使用；重跑需要新的空库。未创建新库，未停止或删除任何容器。

## 测试删除与改名附表

删除依据是基线重置这一设计决策（D6）：旧结构只剩被拒绝的断言，没有对应的真实数据；“替代覆盖”列出仍然存在的断言。类别：DELETE 为整体删除，RENAMED 为改为正式名称、断言保留，新增为本变更加入。来源为任务 1 至 6 的报告与 `scripts/required-tests.txt` 的差异（36 个必需条目移除，其中 3 个原地改名、33 个直接删除）。

| 测试 | 包 | 类别 | 替代覆盖 |
|---|---|---|---|
| TestFrozenBaselineCannotBeRewritten | internal/migrationcheck | DELETE | 当前 0001 哈希不一致失败用例（`initialFrozen`）与保留的改写/递增检查 |
| TestPinnedLegacyReleaseCannotMove | internal/migrationcheck | DELETE | `TestPreReleaseTagsAreIgnored`（D3：v0 tag 不再受保护） |
| TestLegacyReleaseSQLIsVerified | internal/migrationcheck | DELETE | 同上 |
| TestNewReleaseCannotUseLegacyInitialSQL | internal/migrationcheck | DELETE | 同上；v1.x tag 改写迁移失败用例保留 |
| TestPinnedPublishedInitialBaseline | internal/migrationcheck | DELETE | 当前 `initialFrozen` 校验 |
| TestPinnedLegacyReleaseCannotDisappearFromHistory | internal/migrationcheck | DELETE | 无（v0 tag 退役；v1+ tag 被移动或删除不再被检测，见“未验证项”） |
| TestPublishedV040Pinned | internal/migrationcheck | DELETE | 无（v0.4.0 pin 退役） |
| TestCurrentInitialDiffersFromPublishedV040 | internal/migrationcheck | DELETE | 无（同上） |
| TestPublishedV060Pinned | internal/migrationcheck | DELETE | 无（v0.6.0 pin 退役） |
| TestCurrentInitialDiffersFromPublishedV060 | internal/migrationcheck | DELETE | 无（同上） |
| TestPreReleaseTagsAreIgnored | internal/migrationcheck | 新增 | v0 tag 携带被改写的 0001 与多余 0002，若未被忽略则检查失败 |
| TestOldMigrationBaselineRejected | migrations | DELETE | `TestVersionBeyondSourceRejected`（版本超出当前源被无写拒绝） |
| TestInitialSchemaPreservesNonAuditCatalog | migrations | DELETE | 新安装与目录检查（`migrationCatalog` 并入 initial_schema_test.go） |
| TestGenericAuditLegacySameVersionRejected | migrations | DELETE | `TestVersionBeyondSourceRejected`；审计结构由新安装用例覆盖 |
| TestGenericAuditBaselineRejectsPartialLatest | migrations | DELETE | 同上 |
| TestAuditMigrationRejectsIncompleteInitialBaseline | migrations | DELETE | 同上 |
| TestSessionContextBaselineRejectsPartialVersion | migrations | DELETE | 同上 |
| TestSessionContextPrecheckIgnoresConcurrentOtherSchemaDDL | migrations | DELETE | 预检已删除，并发首次部署用例保留 |
| TestSessionContextConsolidatedCatalog | migrations | DELETE | 新安装用例；会话上下文行为由 `TestRecoverySessionContextSnapshot` 等覆盖 |
| TestSessionContextRetiredBaselinesRejected | migrations | DELETE | `TestVersionBeyondSourceRejected` |
| TestSimplifiedSessionRejectsPreviousSnapshotBaseline | migrations | DELETE | 同上 |
| TestProfileAttributesConsolidatedV1Repeat | migrations | DELETE | 重复 Up 用例保留；`TestProfileAttributesBaseline` 缩为锁释放用例 |
| TestProfileAttributesInitialCatalogEquivalent | migrations | DELETE | 新安装用例 |
| TestProfileAttributesWithdrawnV2Rejected | migrations | DELETE | `TestVersionBeyondSourceRejected` |
| TestAvatarBaselineRejected | migrations | DELETE | 同上 |
| TestAvatarPreviousProfileV1Rejected | migrations | DELETE | 同上；头像行为由 `TestRecoveryAvatarFixture` 覆盖 |
| TestIdentityKindSmallintV1Rejected | migrations | DELETE | 同上 |
| TestIdentityKindSmallintV1WithTextColumnAccepted | migrations | DELETE | 无（旧结构接纳路径已删除） |
| TestIdentityKindDriftRejected | migrations | DELETE | 无（列漂移预检已删除，按 D4 只保留 dirty 与版本超前） |
| TestPurgedUserMissingV060Rejected | migrations | DELETE | `TestVersionBeyondSourceRejected` |
| TestPurgedUserV060WithTableAccepted | migrations | DELETE | 无 |
| TestPurgedUserDriftRejected | migrations | DELETE | 无 |
| TestSignInCompletionFlowBaselineMismatch | migrations | DELETE | `TestSignInCompletionFlowInitialDefinition`（缩为新表存在、旧表不存在、version=1） |
| TestVersionBeyondSourceRejected | migrations | 新增 | 版本超出当前源被无写拒绝，保留 catalog 与版本；dirty 优先由保留用例覆盖 |
| TestHistoricalFormatsOnInitialSchema | 根包 | DELETE | 见下“凭证格式覆盖”；Refresh 与既有账号复用见 `accountkit_db_test.go`、`code_key_rotation_test.go`，`legacy_event_id` 投影见 `audit/authentication` 的 `TestAuthenticationStableIDAndLegacyAdministrator`，`ListByResource` 见 `audit/store_test.go`，RevealIdentity 审计见 `audit_lifecycle_test.go` 与 `httpapi/admin/identities_test.go` |
| TestCodeSenderCompatibility | 根包 | RENAMED | `TestCodeSenderContract`（保留 SMS/Email 透传同一目标与 `CodeMessage` 断言，并断言 SMS-only 不是 Email 发送器） |
| TestSignInCompletionContextCompatibility | user | RENAMED | `TestSignInCompletionContextStable`（仅保留正式名称路径，重放断言保留） |
| TestIdentityFlowID | ids | RENAMED | `TestSignInCompletionFlowID` |
| TestLegacyDeliveryPurposesAndFailureQuota | user | RENAMED | `TestDeliveryPurposesAndFailureQuota`（旧名不在旧必需清单，清单中为纯新增条目） |

另有只随旧用例使用而删除的辅助函数与数据（不是独立测试）：`historicalFlowSQL`、`databaseReviewIndexSQL`、`avatarInitialFixtureSQL`、`identityKindTextDDL`、`purgedUserDDL`、`publishedV060Initial`、`profileAttributesInitialDDL` 等，以及 `migrations/testdata/legacy_integrated_v1.sql`、`tests/testdata/source-baseline/`、根 `compatibility_test.go`。`migrations/session_context_consolidation_test.go` 与 `internal/migrationcheck/published_initial_test.go` 整文件删除。保留的合成 fixture 移至 `tests/testdata/synthetic/fixture.json`（design D11）。

## 凭证格式覆盖

核对结果的原始记录在被忽略的 `.test-output/format-coverage.md`，四项结论如下，全部由包内单元测试固定，不依赖源快照：

| 项 | 测试 | 证明 |
|---|---|---|
| 版本化 AES-GCM（nonce 加密文）与旧版本可解密 | `pii.TestStoredFormatsSurviveKeyRotation`；`user.TestReencryptSubjectsRewrapsCiphertextAndKeepsPlaintext` | 以标准库手工构造的 v1/v2 密文可被当前 cipher 解开；库内重加密后版本变为 2 且明文不变 |
| HMAC-SHA256 摘要按版本匹配，新旧摘要都能命中 | `pii.TestStoredFormatsSurviveKeyRotation`；`pii.TestDigesterIsKeyedStableAndVersioned`；`user.TestRekeyDigestsRecomputesOnlyActiveOldVersionRows`；`TestKeyRotationBackfillEndToEnd` | 摘要等于标准库十六进制 HMAC；轮换前后登录返回同一用户 |
| refresh 令牌以 SHA-256 存储 | `user.TestRefreshTokenHashFormat`（无数据库，任务 7 修复轮新增） | `hashRefresh` 等于标准库 SHA-256，明文为 32 字节 base64url |
| JWT kid 即密钥版本，旧 kid 在重叠期可验证 | `tokens.TestSignParseRoundTrip`；`tokens.TestParseAcceptsOldKeyVersionDuringOverlap` | 头部 kid 与 alg 正确，旧版本重叠期可解析、移出后返回 `ErrInvalidToken` |

核对发现的缺口（密文/摘要的版本化格式无独立单元测试、refresh 哈希只有数据库用例）已在任务 7 补测：新增 `pii/format_test.go` 与 `user/refresh_format_internal_test.go`，对应测试均已纳入 `scripts/required-tests.txt`。

## 公开 API 盘点结论

盘点范围为 `internal/`、`examples/`、`cmd/` 之外的全部公开包（`go doc -all` 原始输出在被忽略的 `.test-output/api-inventory/`），结论与理由见 [design.md 附录](design.md#附录接受的-api-现状)。附录共 34 行：7 行“修改”（本变更内处理，仅限注释、取消导出零引用符号，不加功能）、14 行“接受”（保持现状并给出理由）、13 行“待用户决定”（可能属破坏性变更，不在本变更内擅自处理）。已取消导出：`user.KeyBackfillBatchSize`、`user.PurgeBatchSize`。

13 行“待用户决定”（按符号）：

1. `audit/authentication` 的 `MapLegacy` 与认证快照 `legacy_event_id`
2. `ids.AuditEvent`（`e` 前缀）
3. `user/code.ErrExhausted`
4. `user/idp.NewWeChat`
5. `audit.Store.ListByUser`
6. `audit.RetentionBatchSize`、`user.IsUniqueViolation`
7. `user.PurgeCoveredTables`（导出的可变 map）
8. `user.Repo`、`NewRepo`、`Repo.Q/WithTx/WithTxRaw`、`CheckKeyVersions`，以及 `Deps` 中的 `*Repo`、`*code.Store`、`*grace.Cache`、`*revocation.Set`
9. `user/db`、`audit/db` 包（sqlc 生成包可被宿主直接导入，与 sqlc 输出形成 semver 耦合）
10. `httpapi/jsonbody`、`httpapi/reqid` 包
11. `user/sender/smtp.LegacyChineseRenderer`
12. `session/grace.Pair.AccessExpiresAt`、`Pair.RefreshExpiresAt`
13. `user/idp` 的构造器与校验方法命名不一致（`NewApple/NewGoogle/NewFacebook/NewOIDC` 不返回 error 而 `NewAlipay/NewQQ/NewWeibo/NewWeChatWithConfig` 返回；`AppleOptions` 与 `AlipayOptions` 结构不同；`VerifyApple/VerifyGoogle/VerifyWeChat/VerifyIDToken` 命名各异）

建议在打 v1.0.0 tag 之前由用户逐项决定；尤其是第 8、9 行使 sqlc 生成结果成为公开契约。

## 实施中的裁定

以下为实施过程中控制方的裁定（账本 progress.md 中的 `Ruling:` 行），一行一条：

1. 保留 `legacy_event_id`（`audit/authentication` 的 `LegacyEventID` 及其 UNKNOWN actor 接受路径）与行使它的种子：这是审计解码的生产行为，由 generic-audit-events 契约覆盖，不在本变更提案内；作为后续变更候选告知用户（盘点第 1 项）。
2. 任务 2 与 3 合并派发、合并审查，避免出现已审查的 migrations 包红灯中间态。
3. 将删除 `migrations/testdata/legacy_integrated_v1.sql` 从任务 3 移到任务 4，与最后一个使用者 `audit_activation_test.go` 同步，保证每次提交可通过。
4. 任务 6 的验证为静态核对（必需清单每项对应真实存在的测试函数、退役名称均不存在）加定向测试；完整 testgate 在 Linux 镜像中运行，因为 Windows 无法忠实运行 race/CGO 门禁。
5. `TestCodeSenderContract` 保留原有透传断言（根 `Deps.SMSSender/EmailSender` 经 `user.Deps` 投递同一目标与 `CodeMessage`）并加上 SMS-only 不是 Email 的断言，去掉计划文本中无意义的空断言。
6. 任务 8 只允许补充或修正文档注释，以及对包外和文档均零引用的符号做改名或取消导出；其余 API 发现仅记入附录并标为“接受”或“待用户决定”，因为破坏性公开 API 变更超出死代码清理，由用户决定。
7. 任务 8 的盘点覆盖全部公开包而非计划中的 19 个，因为冻结适用于整个公开面。
8. 任务 9 在同一目标连续发送验证码时，持有 miniredis 句柄并用 `FastForward` 越过 `CodeCooldown`，不提高生产冷却配置也不真实等待。
9. 为减少评审席位：任务 9 与 10、11 与 12、16 与 17 分别合并派发。
10. 控制方在每个对应计划任务完成后勾选 openspec tasks.md；本任务只勾选 2.3 与 9.x。
11. 两个取消导出的常量（`user.KeyBackfillBatchSize`、`user.PurgeBatchSize`）计入已移除符号清单，写入兼容指南的 v1.0.0 调用者须知和 design 的 D7 与迁移计划（移除符号共 8 个）。
12. 文档与 AGENTS.md 对 v0 tag/Release 的表述为“已退役，计划在 v1.0.0 之前删除”，而不是“已删除”，因为远端删除是需用户单独确认的步骤，尚未发生；删除后在 v1.0.0 发布准备变更中更新一处措辞。
13. `pending_completion` 流程断言真实的文档契约：在原始截止时间内重放同一 completeSignIn 返回同一令牌对，refresh 轮换后重放得到 400 `SIGN_IN_ALREADY_COMPLETED`，不采用计划中的“第二次完成必须失败”，因为 README 描述的是前者，文档应描述实际行为。
14. `examples/httpflows` 不演练 `/revoke` 与 429 冷却；文档将 `/revoke` 指向 `TestConsumerEndToEndAgainstRealDB` 并解释 `FastForward`，因为审查重点 1 至 5 不含二者。
15. 任务 13 评审的三个小项（功能对照中过期路径 `user/code/scripts.go`、撤销原因 `IDENTITY_UNBOUND`/`IDENTITY_REPLACED` 未在文档说明、`avatar_url` 不可在宿主 Schema 重复声明）并入任务 15 的简报处理。

另记一项实施事实：任务 16 与 17 的提交携带实现者自己的 Sonnet 5.5 trailer，保留原样，不改写历史。

仍有延期的小项（未处理，均不影响门禁）：`dirty_precedence` 子测试未核对 catalog/数据不变、锁泄漏检测依赖 1 秒超时、`seedRenameFlow` 保留 Rename 命名、合成种子仍用 `Source user`/`source-device` 显示名、`TestSignInCompletionContextStable` 单用例表驱动残留、数处 `legacy` 失败文案、`pii/format_test.go` 个别错误被丢弃、`ErrDestructiveOperation` 注释未提 `UnsafeReset` 的确认不一致、`docs/migrations.md` 的 Down 小节仍有迁移叙述措辞、`docs/http-client-flows.md` 未写解绑/绑定前提（测试设 `MaxIdentitiesPerKind=2`，默认值下会得到 `LAST_ANCHOR_IDENTITY` 或 `IDENTITY_KIND_LIMIT`）、`refresh_coordination` 无启动屏障。

## 未验证项

- 远端 v0 tag 与 GitHub Release 的删除未执行，需用户单独确认（design D10 第 3 步，不可逆）。本分支在 v0 tag 仍存在的检出上通过全部检查，依赖 D3 的静默忽略。
- v1.0.0 发布准备（发布说明、候选、CI、tag、Release、远端消费验证）另起 `prepare-v1-0-0-release` 变更，本变更不包含。v1.0.0 tag 打上后 0001 才正式冻结；v1+ tag 被移动或删除不再被历史检查发现，打 tag 后是否追加 v1.0.0 pin 待评估。
- `docs/features.md` 中的六项真实环境验证缺口随版本发布，本变更没有补做。
- 本分支的在线 CI（GitHub Actions）未运行；以上均为本地与容器内验证，不等于在线 CI 已通过。
- `TestProfileAttributesUnreadableFile` 在容器内（root）跳过，该测试不在必需清单中；其非 root Linux 行为与 Windows 版本本次均未验证。
- 第二次恢复演练未运行：`recovery_src`、`recovery_dst` 已被使用，重跑需要新的空库。
- 13 条“待用户决定”的 API 项尚未决策；公开 Go API 在决策前不应视为冻结。
- 本变更未归档，归档是单独步骤。
