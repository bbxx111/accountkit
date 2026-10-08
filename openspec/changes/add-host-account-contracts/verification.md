## 规划工件检查

本 Change 创建于 accountkit 本地仓库，分支 docs/host-account-contracts；仅创建和整理规划工件，不表示库功能已经实施。

- proposal、design、六项 specs 增量及 tasks 已创建，全部实施任务保持未勾选。
- 详细实施计划位于 docs/superpowers/plans/2026-10-08-host-account-contracts.md，引用本 Change。
- openspec status 显示 4/4 规划工件齐备；openspec validate add-host-account-contracts --strict 通过；openspec validate --all --strict 为 13 passed、0 failed，原生退出码 0。

## 实施验收

本轮已开始 apply，任务分支为 `feat/host-account-contracts`，起点提交为 `7b09c5e93ab73651b36bf1eb5a88eacff8128605`。使用现有详细计划，顺序派遣实现代理并逐步独立审查。任务与验收以本记录和 tasks.md 为准，不把历史日志当作本轮验证。

### 初始检查与环境

- `GOWORK=off go test -count=1 ./...`：退出码 0。此时未配置真实依赖，只能作为基线快速测试；不表示集成用例执行。
- `bash scripts/check-migrations.sh`：退出码 0，冻结迁移及正式 tag 历史一致。
- `openspec validate --all --strict`：退出码 0，13 passed、0 failed。
- 创建本任务专用 PostgreSQL 17 和 Redis 容器、隔离网络及普通测试/恢复源/恢复目标三个数据库，不使用已有运行容器。Linux 验证镜像实际为 Go 1.26.5、PostgreSQL 客户端 17.11，具备 gcc；真实依赖测试和恢复演练将在实现后运行。
- 沙箱助手 `codex-windows-sandbox-setup.exe` 缺失，常规 shell/Node 执行无法启动；通过工具批准的 `require_escalated` 执行本任务命令，文件修改仍限于本仓库，临时材料在被忽略的 `.test-output/`。
- 尚未推送、合并、归档或创建新 tag。6.5 的固定版本发布和远端独立消费须单独取得发布授权。

### 验证码存储（tasks 1.1–1.5）

- 新增 Binding/Issued/Credential 与 IssueChallenge/VerifyChallenge/DiscardChallenge。随机 128 bit 标识、用途/目标/主体隔离、第三次正确/错误边界、并发一次消费、跨用途固定失败窗口、第十次即时 TARGET_VERIFY_LIMIT、按标识清理及多版本/别名预算均有行为测试。
- 初次 `go test -count=1 ./user/code ./ -run 'TestChallenge|TestConfig'` RED 退出 1（缺接口/配置）；补齐实现后 GREEN 退出 0。第十次即时限流和畸形脚本响应分别有独立 RED/GREEN 证据。
- 最终 `go test -v -count=1 ./user/code` 退出 0，TestCodeRotationRedisIntegration、TestChallengeRedisIntegration 和 TestChallengeQuotaCorruptionRedisIntegration 均使用本任务真实 Redis 执行，无 SKIP。`go test -v -count=1 ./user/code ./ -run 'TestChallenge|TestConfig|TestValidate'` 退出 0；`go build ./...`、`go vet ./user/code ./`、范围 `git diff --check` 退出 0。
- 独立审查发现非规范/越界额度字符串通过 Lua tonumber 但不能 Redis INCR，可能报错前部分占额。真实 Redis 回归先 RED 退出 1，修复后 GREEN 退出 0：在首次写入前验证全部可写额度的规范十进制与递增范围。用 DUMP/PExpireTime 比较证明拒绝后键、值及原期限不变。独立范围复核判定 ADDRESSED，未留 Critical/Important，规格 PASS、质量 Approved。
- 真实 Redis 核验双 active、绝对期限保留、成功不清预算和并发；默认 15 分钟完整边界通过 miniredis 时间推进测试，真实 Redis 自然到期以本任务键缩短 TTL 验证，不声称实际等待 15 分钟。
- 既有 TestRedisDownIsUnavailable 主动断开连接产生预期拨号诊断日志，测试 PASS；作为 Minor 记录，没有凭证泄露。旧 Issue/Verify 仅在 Task2 迁移前暂留，完成实施前须移除。
- 原始证据：被忽略的 `.test-output/sdd/host-account-contracts/task-1-*` 日志、实现报告与两轮审查报告。完整库/服务 build/vet/race、真实 PostgreSQL、恢复与发布仍待后续步骤。

### 批量公开资料（tasks 3.1–3.2）

- 新增 `PublicProfile`/`BatchPublicProfiles` 和 sqlc `ANY` 查询，只返回 ID/DisplayName/State；空输入零查询、非法 ID 拒绝、输入去重、未知略去、待注销/已删除显示名为空。未添加资料 HTTP 入口。
- 缺接口和行为各有 RED 退出 1。使用全部 user 生产文件与本任务测试的 file-list 执行 `go test -count=1 ... -run TestBatchPublicProfiles -v` GREEN 退出 0，四个子测试使用本任务真实 PostgreSQL，无 skip；pgx QueryTracer 确认一次查询、六个去重 ID。
- sqlc generate 退出 0，内容仅新增批量查询生成块。独立审查规格 PASS、质量 Approved，没有 Critical/Important；缺零输出 vet/生成检查日志的 Minor 已由根代理补证。
- 并行迁移曾以同文本重写非拥有 Go 文件，生成一致性补跑捕获 stale（退出 1）；重新生成后原脚本连续退出 0，生成 Go 均为 LF，实际查询新增 31 行。已约束迁移代理只写明确拥有文件；未手改生成内容或放宽检查。
- 根代理 file-list vet 首轮因 Windows 路径筛选错误形成空生产文件列表，退出 1；纠正后包含 18 个 user 生产文件及资料测试，退出 0。实际状态和失败证据保留在 `.test-output/sdd/host-account-contracts/task-3-*`。
- 本阶段全仓测试尝试退出 1，当时验证码迁移中的旧测试签名、未用 import 和尚未清除的旧 Store API 导致失败；这些由 Task2 处理。不能将本阶段当作完整全库验证，最终门禁仍待整合后执行。

### Go/HTTP 完整轮次协议（tasks 2.1–2.5）

- 发码返回 CodeChallenge，登录、绑定、重新认证和换绑传 CodeCredential。两渠道必需 code_id，服务器固定用途与 BIND 用户/REAUTH 用户会话，发码明确 DTO/no-store；投递失败按标识清理，普通发送错误固定 Error 文本并保留 Unwrap，不泄露日志/响应凭证。旧 Store Issue/Verify 与旧 Lua/HMAC 旁路已移除。
- 新行为 RED 退出 1（原 HTTP 响应缺字段/no-store、投递失败残留）；跨用户 REAUTH 分类 RED 退出 1，调整为先轮次绑定后 anchor 校验，事务锁后身份和期限复核保持。
- `go test -count=1 ./user ./httpapi/enduser -run TestCodeChallenge` GREEN 退出 0；三包 user/enduser/accountsvc 回归退出 0，其中 user/HTTP 无 skip，Windows 的十项服务进程测试明确 skip，留待 Linux。
- 七个根真实依赖场景（SourceDatabaseTakeover、IndependentInstances、Consumer、IdentityBinding、IdentityReplacement、SessionExpiry、CodeKeyRotationIntegration）全部 PASS、无 skip，命令退出 0。最终协议范围 file-list、完整 HTTP 包及 user/enduser/accountsvc vet 退出 0；导入 RED 期间的全 user 编译失败已留日志，没有当作通过。
- Store 迁移最终 JSON：完整包 152 PASS/0 SKIP，真实 Redis 定向 47 PASS/0 SKIP；vet、范围 diff 检查退出 0。保留原顶层门禁和隔离/额度/并发断言，旧无标识拒绝用行为测试证明。
- 独立审查规格 PASS、质量 Approved，无 Critical/Important。非阻断注释与换绑/跨渠道覆盖已补齐；两项受控 mutation 分别让换绑 CodeID 传递及真实 PHONE 跨 EMAIL 断言失败（退出 1），finally 恢复原字节后覆盖测试/vet 退出 0。预期 Redis 故障日志作为已有诊断噪声记录。
- 迁移脚本曾同文本重写非拥有 Go 文件，已停止并由根代理仅对归一化后完全相等的文件恢复任务前原字节，保留语义成果。格式化的自动审查误判通过证明 user/code_credential.go 属于 user 根目录并使用明确路径后完成，没有遗留权限阻断。
- 证据在 `.test-output/sdd/host-account-contracts/task-2-*`；Linux race、完整服务/恢复及最终组合代码门禁仍待实施全部完成后执行。

### 受控离线导入（tasks 5.1–5.3）

- ImportAccounts 使用调用者 pgx.Tx 与 db.New(tx)，不另连/提交/回滚；ACTIVE 至少 PHONE/EMAIL 锚点且无删除时间，DELETED 无锚点/显示名，原 ID 与非零原时间保存，支持历史时钟回退。复用归一化、加密和完整版本摘要检索，重复和冲突失败，Error 脱敏而保留可分类的底层诊断。
- 缺接口及真实 PostgreSQL 行为 RED 退出 1，最小实现后最新 `go test -count=1 -v ./user -run TestImportAccounts` 退出 0，七项顶层测试实际执行、无 skip，覆盖合法/非法数据、旧摘要冲突、真实唯一索引并发等待、调用者业务 SQL 失败整批回滚、数据库 PII 错误脱敏、导入后新协议登录保留 ID。
- sqlc generate、Git Bash check-generated、user vet、全仓快速测试及范围 diff 检查退出 0。普通全仓非 verbose 输出不证明所有可选集成均执行，最终 Linux 门禁另行核验。
- 独立审查规格与质量 Approved，无实质 findings；没有导入 HTTP 路由，也不发码/建会话/签令牌/访问 IdP。实际旁路守卫与 Redis/session/log 断言有记录。
- 原始日志与报告在 `.test-output/sdd/host-account-contracts/task-5-*`；完整恢复、发布及远端固定版本消费仍未执行。

### 宿主生命周期（tasks 4.1–4.4）

- 根/领域 Deps 装配 BeforeDelete，消费者与管理员共用 softDelete：账号锁、ACTIVE 检查、同 pgx.Tx 回调、状态与会话修改。ErrDeletionBlocked 为固定 400/FAILED_PRECONDITION/DELETION_BLOCKED，其余回调错误为固定 500，防止底层旧领域哨兵误分类。
- WithActiveUsers 在借连接前验证非空集合、非 nil 回调和 ID，去重排序锁定全部 ACTIVE 账号后执行同事务回调；库提交/回滚，回调 error 保留，panic 回滚后重抛。宿主锁序为账号后业务资源。
- 接口缺失、两面错误映射和基础设施误分类均有真实 PostgreSQL/HTTP RED，退出 1；完整定向 GREEN 退出 0，全部数据库用例执行无 skip。覆盖拒绝后账号/会话/宿主数据不变、状态先于 hook、回调 error/panic 回滚并释放连接/锁、逆序账号并发、删除/新关联双向竞争及原匿名化失败回滚。
- QueryTracer 确认重复/逆序输入只执行两次升序账号锁，回调内独立事务 NOWAIT 证明全部账号已锁。移除去重、移除排序两个受控 mutation 分别使断言失败（退出 1），恢复原字节后 GREEN 退出 0。
- 范围 vet/diff 与全仓快速 JSON 测试退出 0；该 Windows 套件 1124 个含子测试 PASS、0 FAIL，11 个顶层 skip（普通恢复夹具和十项要求 Linux 的服务进程），没有据此宣称完整服务验证。最终 Linux 结果见下节。
- 独立规格 PASS、质量 Approved，无需修复的问题；原始报告和证据在 `.test-output/sdd/host-account-contracts/task-4-*`。

## 最终验收（2026-10-09，tasks 6.1–6.4）

最终源码所在任务分支 `feat/host-account-contracts`，基线为 `7b09c5e93ab73651b36bf1eb5a88eacff8128605`。以下检查针对本轮实际代码，不引用历史 CI 成功作为通过证据。测试使用本任务专用 PostgreSQL 17/Redis，恢复源/目标为不同的专用空数据库。

| 实际命令 | 原生退出码 | 结果 |
|---|---:|---|
| `GOWORK=off go build ./...` | 0 | 库、accountsvc 及示例独立编译 |
| `GOWORK=off go vet ./...` | 0 | 全仓无诊断 |
| `bash scripts/check-generated.sh` | 0 | sqlc 1.31.1 生成结果稳定，未手改生成内容 |
| `bash scripts/check-migrations.sh` | 0 | 固定源基线与完整可达正式 tag 历史保持不可变 |
| `bash scripts/verify.sh`（Linux Go 1.26.5，CGO/race） | 0 | build/vet/race、382 项库必需测试、真实恢复三阶段通过 |
| `bash scripts/verify-accountsvc.sh`（Linux，服务二进制启用 race） | 0 | 实际 SMTP/OIDC/TLS/HTTP/进程/SIGTERM 及 15 项服务必需门禁通过 |
| `openspec validate --all --strict` | 0 | 13 passed、0 failed |

### 完整执行证据

- 库完整 JSON：454 个顶层测试 PASS，1140 个含子测试 PASS，0 FAIL；仅 TestRecoveryFixture 在普通套件预期 skip。382 个库必需测试逐条都有真实 PASS，旧 342 条全部保留，新增 40 条。
- 服务严格 JSON：66 个顶层测试 PASS，338 个含子测试 PASS，0 FAIL/0 SKIP。15 条原服务必需测试全部真实 PASS，原清单未删减。
- `seed`、`verify`、`source-unchanged` 分别包含 TestRecoveryFixture 的 PASS，均 0 FAIL/0 SKIP；实际 pg_dump/pg_restore 17.11 演练验证版本、四表快照、解密、access/refresh 及源库保持。备份、状态和日志保留于 `.test-output/recovery.cndUZp/`。
- 完整验证容器退出 0；日志含 `LIBRARY_VERIFY_EXIT=0`、`ACCOUNTSVC_VERIFY_EXIT=0` 和 `Recovery drill passed`。原日志为 `.test-output/tests.jsonl`、`.test-output/accountsvc-tests.jsonl` 与 `.test-output/sdd/host-account-contracts/full-linux-verification.log`。
- 原生各项日志/退出码保留于同目录 `final-native-*`、`final-generated.log`、`final-migrations.log`、`final-openspec.log`。受测 Go/SQL/module/sqlc/验证脚本与门禁 SHA-256 清单为 `final-tested-source.sha256`，提交前核对当前源码仍一致。
- 最终全分支独立审查 Approved，无 Critical/Important。README 旧 CODE_ATTEMPTS_EXHAUSTED 描述已修正并复核；现有故障注入的预期 Redis 拨号日志仅为非阻断诊断噪声，未泄露凭证。完整审查记录为 `final-code-review.md`。

### 六项规格场景对应

| 规格与场景组 | 本轮实际测试/检查 |
|---|---|
| verification-challenges：发码成功、完整凭证、缺失/格式、四类消费、缓存与错误协议 | TestCodeChallengeHTTPContract、换绑两渠道/错误矩阵、库/服务真实闭环；完整门禁 PASS |
| verification-challenges：旧页面、同数字、目标/渠道/用途/用户/会话隔离 | TestChallengeIsolation、TestCodeChallengePrincipalBinding、真实源 PHONE 跨 EMAIL 用例、TestIndependentInstances |
| verification-challenges：第三次边界、并发一次消费、重发竞争 | TestChallengeAttemptBoundary/AtomicConsume/ConcurrentReplaceAndVerify，真实 Redis 集成 |
| verification-challenges：跨轮次/用途预算、第十次、首次固定窗口、成功/重发不续期、失效流量与依赖 | TestChallengeFailureBudget/SuccessPreservesWindow/Unavailable/CorruptState/ExpiryAndProtocol、真实 Redis 期限/预算用例 |
| verification-challenges：旧协议拒绝、全部调用者升级、实例/存储隐私 | TestCodeProtocolRejectsUnidentifiedLegacyCredential、旧旁路搜索无匹配、HTTP/Go 编译、部署迁移说明 |
| verification-delivery：SMTP 接受、用途说明、失败不退额度、清理新旧轮次竞争 | TestCodeChallengeDeliveryCleanup、TestChallengeDiscardRace/ConcurrentReplaceAndDiscard、SMTP 与实际服务 EmailLifecycle/DependencyFailure |
| code-key-rotation：双 active 校验/原 TTL/次数、原子消费/替换、用途/渠道/实例、Redis 拒绝 | 原 TestCodeRotation* 门禁、TestCodeKeyRotationIntegration、TestChallengeRedisIntegration、服务 CodeKeyRotation |
| code-key-rotation：冷却/日额度/IP/UTC/别名、跨版本预算及截止时间、损坏额度无部分占额 | TestCodeRotationLimits/Boundaries/State、TestChallengeRotationBudget、QuotaCorruptionRedisIntegration；README 退役窗口包含失败预算 |
| host-account-contracts：一页一次、去重/非法/未知、注销资料清空、无身份字段/HTTP | TestBatchPublicProfiles 的真实 QueryTracer、三字段公开类型和路由审核 |
| host-account-contracts：两面共享拒绝、故障/业务写入回滚、状态先检查 | TestBeforeDeleteSharedTransaction/HTTPErrorClassification/LocksAndChecksState、两面 TestDeletionBlockedSafeResponse |
| host-account-contracts：新关联/注销竞争、逆序输入、去重稳定锁、回调 error/panic | TestHostDeleteCreateRace、TestWithActiveUsers/ReverseOrder/Rollback/DeduplicatesAndLocksBeforeCallback/ValidatesBeforeIO |
| legacy-account-import：同事务引用失败、ACTIVE 连续性、匿名墓碑、归一化/旧摘要冲突、部分失败/隐私 | 七个 TestImportAccounts* 顶层用例，含真实 SQL 失败回滚、唯一锁竞争、新协议登录和 PII trigger 错误 |
| embedded-auth-package：独立构建/示例、原认证会话/IdP/角色/匿名化与协议升级 | GOWORK=off build、原库/服务完整必需门禁、冻结迁移/生成检查、README/兼容指南/发布检查 |

### 已记录的设计取舍

- 独立的协议/资料任务并行，随后 Store 子任务也以明确文件范围拆分；共享写入或依赖一旦判断错误会导致重做。迁移脚本的同文本越界写入已纠正，原始换行恢复仅用于归一化后完全相等文件，未回滚语义成果。
- 导入保留历史时间回退，不额外强制 update_time 晚于 create_time；宿主仍负责原数据核验，可能需要处理历史时间不一致。
- WithActiveUsers 拒绝空 ID 集合/nil 回调，避免作为裸事务入口；宿主空批次自行跳过。
- BeforeDelete 仅业务 ErrDeletionBlocked 参与领域分类，其他 hook 原因不解包而固定 500；代价是宿主不能对任意 hook 底层原因做 errors.Is/As，WithActiveUsers 原错误链/panic 保持。

### 实施阶段未执行项（后续发布结果见下节）

- task 6.5：新固定版本、tag/发布以及无本地 replace 的远端宿主消费尚未执行，必须先取得单独发布授权；v0.1.0 未修改。当前检查不能声称新版本已可远端消费。
- 未推送、合并、归档或产品部署；本地结果不等于在线 CI 或宿主真实网关/服务商/IdP/产品环境验收。
- 临时测试容器、网络与缓存仅清理本任务创建资源，原始测试/恢复材料按项目要求保留在被忽略的 `.test-output/`。

## v0.2.0 发布（2026-10-09）

用户已明确授权“发布新版本”。选择尚未占用的 `v0.2.0`，包含本变更和已实施的默认 account schema，保留 v0.1.0 tag。按项目默认 PR/独立 merge commit 流程进入 develop 和 main，最终 main 候选 CI 通过后发布新 tag/Release，再验证无本地 replace 的远端宿主消费。OpenSpec 归档和产品部署不在此次发布动作内。

实施提交为 `61cfa0a6cfb5d857d81154bc221910e89004350c`，发布说明保存于 `docs/releases/v0.2.0.md`。当前正准备发布候选，最终版本 SHA、CI、PR、远端消费和原生退出码将在动作实际完成后补充，不预先勾选 task 6.5。

### 已完成发布与远端消费

| 项目 | 实际结果 |
|---|---|
| 功能 PR | [#1](https://github.com/bbxx111/accountkit/pull/1)，以独立 merge commit `42e125f3aa001c5ecd37fc331851f8dfb5a5c2a9` 合入 develop；两个父提交保留 |
| 发布 PR | [#2](https://github.com/bbxx111/accountkit/pull/2)，以独立 merge commit 合入 main |
| 最终发布 SHA | `e19af8542f099f14103627140c0fe23f14c99b7e`，main 与已验证 develop 文件树一致 |
| 最终候选在线 CI | [verify #37814180599](https://github.com/bbxx111/accountkit/actions/runs/37814180599)，head SHA 与发布 SHA 一致，所有步骤 success，含生成/完整库与恢复/严格服务门禁 |
| 新固定版本 | annotated `v0.2.0`；远端 tag 对象 `050a83d3522c389b72706e0f98ab12a8f693d8cd` 解引用为上述 SHA，git tag/push/ls-remote 均 exit 0 |
| GitHub Release | [v0.2.0](https://github.com/bbxx111/accountkit/releases/tag/v0.2.0)，已发布，非 draft、非 prerelease |
| 根门面独立宿主 | 公共远端 `go get ...@v0.2.0` exit 0、`GOWORK=off go build ./...` exit 0；编译 EndUserHandler、CodeCredential/CodeChallenge、三用途/换绑、BatchPublicProfiles、BeforeDelete、WithActiveUsers、ImportAccounts |
| 直接 enduser 宿主 | 远端 go get/build 均 exit 0；编译 enduser.New/Router 与 Service/IdentityReplacer 实际接口 |
| 消费版本核验 | 两个 `go list -m -json` 均 Version=v0.2.0、无 Replace；GOWORK=off，未依赖本地源码或 accountsvc 进程 |

功能候选 push/PR CI #37811165586/#37811285344、develop push/发布 PR CI #37812505118/#37812513191 也全部成功。发布前核验实施时 233 项受测源码/脚本未发生语义变化；分支 checkout 对门禁 .txt 的 CRLF 转换经原受测 LF hash 核对后恢复，不改内容。

发布与消费的原始 API 结果、版本JSON、go.mod/go.sum、源码和命令日志保留于 ignored `.test-output/v0.2-release/`。task 6.5 仅在新 tag/Release 和两宿主实际消费全部完成后勾选，现为 24/24；OpenSpec change 仍未归档。

已发布 v0.1.0 的 tag 对象/提交保持 `29ab9c22fcb8d2307bee75ea7c09287f0a517c87` / `48603d6d349c57910328d393d5e17decec934f11`，没有重建、移动或覆盖。GitHub Release 复用本版本说明；没有发布镜像/二进制或执行产品部署，产品实际环境/服务商/网关验收仍由宿主负责。

- 原库详细计划已迁入 accountkit，迁移前核对任务正文一致，再将宿主示例策略改为通用可配置表达；duopandian 移除重复库计划并引用外部 Change。

- 已读回并校验跨仓文档链接；6 项能力增量、24 项实施任务均未勾选。宿主接入计划的写入异常已按本轮计划恢复，并核对 7 项宿主任务及共享接口。
- git diff --exit-code 对 Go/SQL/go.mod/go.sum 无差异；本次没有源代码、依赖、数据库或发布变更。
