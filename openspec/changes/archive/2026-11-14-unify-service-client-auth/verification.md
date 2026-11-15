# Verification

## 归档与本地集成（2026-11-14）

- 用户明确要求依次归档、提交并合并到集成分支。本次沿用本地 `develop`，保留独立 merge commit，不推送、不发布。
- 归档前 CLI 确认全部工件完成、22/22 任务完成；四份 delta 与已同步主规格一致，同步预览无变化。`develop` 为任务分支祖先，没有分叉冲突。
- `openspec archive unify-service-client-auth --skip-specs --yes --json` 已成功归档到当前目录。仅跳过已完成的规格写入，没有跳过验证；主规格保持原提交字节。
- 已修复归档内任务/验收记录指向实现计划的相对链接，以及两份实现计划的反向引用；`.openspec.yaml` 随目录保留。此前各阶段的“未归档”等表述保留为历史事实。
- 归档后 `openspec list --json` 返回无活动变更；10 份相关 Markdown 的 7 个本地链接均可解析，没有旧活动路径引用。全库 strict 现为 43 份主规格、36 通过、7 份仍因已知正文长度警告未通过；结果在 `.test-output/service-client-archive-validation.json`，未修改主规格或放宽验证。
- 归档提交为 `f573961`。本地 `develop` 执行 `git merge --no-ff --no-commit feat/unify-service-client-auth` 无冲突；准备好的合并索引与任务分支完全一致，验证后生成独立双父 merge commit。本节结果增补是合并提交相对任务分支唯一的额外修改。
- 合并工作树上重新运行 `GOWORK=off go vet ./...` 和 `go test -count=1 -json ./...`，两者退出 0；54 个测试包通过，849 个顶层测试通过、380 个顶层测试跳过，无失败。原始日志为 `.test-output/service-client-merge-vet.log` 与 `service-client-merge-tests.jsonl`。
- 上项为 Windows 本地检查，未提供数据库集成配置且存在平台条件跳过，不能宣称新的完整集成/race 验证。合并没有修改已验收实现的 Go/SQL/部署内容；下文保留此前真实 Linux/race/数据库/恢复结果及规格长度警告，并明确区分各执行时点。未推送或发布。

## 主规格同步（2026-11-14）

用户在 `c3ddfe6` 后要求将当前 delta 同步到主规格。本次只同步下列四份主规格并记录结果，保留活动 change、delta 工件与历史验收记录，未修改产品代码。同步完成后用户要求本地提交；提交前重跑主规格普通校验仍为 43/43 通过，未推送或归档。

| 主规格 | 修改要求 | 新增要求 |
|---|---:|---:|
| accountsvc-runtime | 4 | 3 |
| accountsvc-service-authentication | 4 | 7 |
| consumer-token-introspection | 1 | 0 |
| purged-user-feed | 1 | 0 |

- 按现行 specs 指令读取规则后合并，共修改 10 项、增加 10 项要求。保留各主规格原 Purpose、未涉及的要求及场景；原修改项的所有场景标题均在 delta 中，不存在被遗漏的原场景。主规格仅有普通 Requirements，不含 ADDED/MODIFIED 等 delta 操作标题。
- 核对差异及需求块：JWT 校验、提供方缓存/故障处理、数据库吊销、清理事务/分页、其他 runtime 功能等未涉及内容保持。旧逐接口配置只作为明确拒绝和迁移说明出现，不再描述为可用配置；同一客户端可按权限访问两项能力。
- 合并后的四份主规格与 delta 对应块一致；重复执行同步预览均显示无变化，确认幂等。git diff --check 通过。
- `openspec validate --specs --json`：退出 0，43/43 主规格通过普通校验。`openspec validate --all --strict --json`：退出 1，44 项中 37 通过、7 失败；当前 change 通过且无 issues，失败仍为同样七份主规格的 >500 字符正文警告，没有为校验拆分/删减已确认要求。原始结果为 `.test-output/service-client-sync-specs.json` 与 `service-client-sync-strict.json`。
- 本次为规格同步，未重跑 Go/数据库测试；实现验收证据仍见下文。本节记录同步之后的状态，下文“主规格未修改”等说明属于各自历史验收时点。

## 单一配置模式收敛（2026-11-14，本轮最终验收）

用户在提交 `b25ec98` 后要求“去掉旧配置模式，整合配置文件”。本轮 tasks 5.1–5.4 已完成，原 18 项仅是初次实现历史；当前最终契约及实现均只有统一注册表。计划见[单一服务客户端配置](../../../../docs/superpowers/plans/2026-11-14-service-clients-only.md)。验收完成时保留当前任务分支与未提交工作区，未推送、发布、部署或归档；用户随后明确要求本地提交，见下方补记。

### 当前实现

- 移除旧四项 Config 映射/名单、模式标识、JSON 解析、弃用元数据/警告及旧装配/认证分支；删除失去生产调用的 `introspection/clients.go`、basicclients.Parse 和 SharedSecret。Matcher、固定长度秘密摘要、常数时间轮换匹配保留。
- SERVICE_CLIENTS 未设置、空或全空白均生成非 nil 空注册表；无客户端时服务可启动、两个能力返回 NOT_CONFIGURED 503。孤立字段仍拒绝；任一旧变量非空白（含 {}、[]，仅旧或混合配置）在监听前明确报错，不回显值，空白旧占位忽略。migrate 仍不解析新旧服务运行变量。
- serviceauth 只使用 Registry、RequiredPermission、BearerVerifier；nil Registry 只转换为空注册表且仍要求合法权限。Basic-only 便利构造也进入同一路径。Basic/Bearer、两个权限、JWT/缓存/挑战/503 与消费者数据库吊销、feed 事务/分页保持。
- 基础 `compose.yaml` 直接 `env_file: ./.env`，删除 `compose.service-clients.yaml` 和四项旧变量默认映射。模板默认空注册表，保留三种认证组合及轮换示例。主机真实展开验证动态别名与显式旧变量拒绝；文档要求 CLI --env-file 与服务 env_file 指向同一文件。
- 活跃接入文档仅描述统一配置和显式迁移；当前二进制不能恢复旧模式，回退需配套旧二进制与匹配配置。支付宝、QQ、微博三份指南只修正 env_file 导致过时的透传说明，没有登录行为变化。
- SDK HTTP 覆盖检查通过，35 个操作与 OpenAPI 一致。相对 b25ec98，SDK、数据库、消费者/管理员协议、模块与令牌领域文件无差异；旧字段/解析器名称搜索无残留，gofmt 与 diff --check 通过。

### 本轮实际验证

使用本轮专用 Linux runner、PostgreSQL **17.11**、Redis **7.4.11** 和两个独立空恢复数据库；Go **1.26.5**，GOWORK=off。下表未另列目录的日志均位于 `.test-output/service-clients-only/`。

| 检查 | 结果与证据 |
|---|---|
| TDD 与定向配置/认证 | task-2-red.log 复现旧 [] 被接受、缺省 nil 注册表及 nil 绕过权限；修改后 task-2-green.log、final-config/runtime 等日志通过。没有把中间编译/夹具错误冒充行为 RED |
| 定向真实进程与 race | 9 个相关进程用例实际通过，覆盖旧变量监听前拒绝、缺省能力、双方式权限/轮换、数据库吊销及清理 feed；task-2-race-process.log。认证/handler 四子包完整 race 通过 |
| 配置模板与 Compose | 主机模板 4 场景、真实 Docker Compose config 16 场景全部 PASS、无 skip；覆盖缺省/空/空白、旧空白占位、三种认证组合、动态下划线别名、四旧变量的仅旧/混合拒绝；task3-green.log |
| 局部 vet 与示例 | 新鲜 `go vet ./internal/accountsvc/...` 和 remoteauth 定向回归通过；task-2-vet.log、task3-remoteauth.log、task3-vet.log |
| 独立 race 构建 | `go build -race ./...` 通过；race-build.log |
| 完整 verify.sh | 修复测试清单遗漏后，从完整入口重跑退出 0：迁移历史、build、vet、全库 race、必需测试门禁通过；54 个测试包、1227 个顶层测试通过，零失败；tests.jsonl、full-result.txt |
| 恢复演练 | verify.sh 内真实 pg_dump/pg_restore 及 seed/verify/source-unchanged 三阶段全部通过；`.test-output/recovery.PgYjs9/` 保留合成数据证据 |
| accountsvc 专项 | `bash scripts/verify-accountsvc.sh` 退出 0；10 个测试包、233 个顶层测试通过，零失败；真实 TLS/OIDC/SMTP fixture、SIGTERM、带 race 的服务子进程及必需门禁通过；accountsvc-tests.jsonl、accountsvc-result.json |
| 规格 | 当前 change strict 通过，无错误/警告；全库 strict 仍为 44 项、37 通过、7 份未修改主规格因正文 >500 字符失败，openspec-all.json |

### 首次完整验证失败与修复

首次 verify.sh 仅 `TestServiceAndLibraryEnvNamesDoNotOverlap` 失败，保存在 full-attempt-1.jsonl；其余 53 个测试包通过，恢复阶段未执行。原因是该测试仍要求源码扫描出已退休的 INTROSPECTION_BASIC_CLIENTS，且动态拼接的服务客户端字段未纳入库命名空间探针。

仅修改 config_prefix_test.go：sentinel 改为 SERVICE_CLIENTS，加入含下划线别名 COLLISION_PROBE 的 ID/AUTH_METHODS/PERMISSIONS/BASIC_SECRETS 四项真实 ConfigFromEnv 隔离探针；保留原静态检查、命名空间限制和 AUTH_SCHEMA 正向控制。单项 RED 复现后，43 个子例 race 全部通过、无 skip（task-2-prefix-red.log、task-2-prefix-green.log），独立限定复审通过，再完成上述全套重跑。没有删除或跳过该门禁。

阶段报告曾误称空输出 PowerShell Tee 已生成 task-2-vet.log，复核发现文件不存在后已纠正，并真实重跑 vet、显式保存命令及 exit 0；最终证据不依赖那条不准确的路径声明。

### 测试迁移、审查和边界

- 两份必需清单只将 EmptyAndLegacyModes 重命名为 EmptyRegistry、UnifiedCapabilityAndLegacyWarnings 重命名为 ServiceCapabilityWarnings，并新增 RejectsRemovedSettings、ServiceAuthenticatorNilRegistryIsEmpty 和 ServiceIntegrationRejectsRemovedSettings。其余原条目保留。
- 未登记为必需的旧 JSON 格式专用 Parse 测试随解析器移除，声明/ID/秘密/重复项/脱敏由现行 Config 与 Registry 测试覆盖；MatcherAndSharedSecret 改为 MatcherAndRotation，保留匹配、多头和两秘密轮换断言，重复秘密由 Registry 拒绝测试承担。有效 Basic 缺权限改为 403；错误秘密的 401 断言仍保留。
- 核心和文档独立审查均通过；全量最终审查未发现 Critical/Important 问题。核心初审发现的配置前缀测试问题已修复并经限定复审确认。
- Linux 两套件的 ComposeExamples 因无 Docker CLI 跳过，已由主机 16 场景实跑覆盖；既有 ProfileAttributesUnreadableFile 因容器 root 可读受限文件而跳过，该权限分支本环境未验证。普通套件 RecoveryFixture 按契约跳过，但专用三阶段实际通过。完整发布门禁在 Linux 执行，不把非 Linux 进程用例的 skip 当作通过。
- 全库 OpenSpec 的 7 份既有失败仍是 accountsvc-runtime、accountsvc-service-authentication、consumer-token-introspection、embedded-auth-package、purged-user-feed、verification-delivery、versioned-auth-storage；主规格与基线无差异，没有放宽校验或报告全库通过。
- 下方历史验收表中的通用 tests.jsonl/accountsvc-tests.jsonl 已在本轮重写前保存为 `.test-output/unify-service-client-auth/b25ec98-tests.jsonl` 和 `b25ec98-accountsvc-tests.jsonl`，保留 b25ec98 原事实。本轮原始日志及合成恢复材料不提交。
- 执行选择：沿用同一未归档 change，保留已提交的历史阶段；旧变量用明确拒绝代替静默忽略；基础 Compose 要求按文档准备同目录 .env，不再维护额外配置文件。运行时与文档只在不重叠文件范围并行，最终统一验证。
- 验收结束后按容器 ID 核对并清理本轮三个测试容器及匿名数据卷、专用 network 和两个命名缓存卷；另经绝对路径/非 reparse point 核对后清理本轮生成的 Go 构建缓存。未操作其他容器、数据库或目录，代码和日志/合成恢复证据保留。

### 本轮提交前复核

- 用户后续要求“提交”，本次只创建本地提交，不推送或归档。
- 提交前重新运行 `GOWORK=off go test -count=1 ./internal/accountsvc/... ./examples/remoteauth`，通过；这是 Windows 快速回归，平台及数据库条件不满足的用例按契约跳过，完整 Linux 验证仍以上述本轮实跑证据为准。
- 核对改动范围及差异格式，未包含实际 `.env`、测试输出、缓存或恢复备份。

## Apply 最终验收（2026-11-14，b25ec98 初次实现历史）

18 项实施任务已完成。实现计划见[统一服务客户端认证](../../../../docs/superpowers/plans/2026-11-14-unify-service-client-auth.md)。验收完成时保留现有 `feat/unify-service-client-auth` 分支和未提交工件，未执行提交、推送、发布、部署或归档；后续本地提交授权见下方补记。

### 实现与覆盖

- 配置：按 SERVICE_CLIENTS 存在性选择模式，显式空为统一空注册表；完整变量名匹配别名字段，拒绝孤立字段、新旧混用、非法/重复 ID、方法、权限或秘密。规范 Base64、至少 32 字节、重叠轮换和跨客户端秘密不复用均有测试。migrate 不读取服务认证变量；原 issuer/audience 隔离保持。
- 认证：不可变 Registry 复用 basicclients 摘要匹配和 oidcjwt，Basic/Bearer 共用权限。两个接口共享 Registry/verifier，分别要求 tokens.introspect、purged_users.read；保留旧模式独立名单路径。
- 协议：两个 handler 现有 Enabled → Authenticate → 协议解析 → 业务调用顺序可直接复用，未改写 handler 生产逻辑。新增矩阵核对 401/403/503、精确挑战、no-store、feed Retry-After，以及拒绝时零业务调用。Basic-only 无 OIDC I/O；提供方故障不影响监听、readiness 和 Basic。
- 真实进程：同一身份双方式双接口、权限拒绝、禁用方法、运行快照、重启撤权、删除身份、Basic 秘密轮换、恢复旧配置均已验证。Redis 故障后的已提交数据库吊销立即影响统一内省；真实维护清理与 Basic/Bearer 交替分页通过。
- JWKS 故障恢复和 kid 轮换复用实际通过的 TestProviderFailureAndRecovery、TestNewKidWithinWindowThenAccepted；统一装配测试确认并发请求共用同一 verifier/刷新。未新增第二套真实进程轮换 fixture。
- 完整数据库套件确认 TestPurgeWritesPurgedUserRecord、TestPurgedUserRecordRollsBackWithAnonymizer、TestPurgedUserFeedConcurrentPurgeNoGaps 均通过。领域、HTTP 公共包、令牌、迁移、go.mod/go.sum 无差异。
- 部署：基础 Compose 不默认注入空 SERVICE_CLIENTS；显式 `compose.service-clients.yaml` override 通过同目录 `.env` 透传任意别名。CLI --env-file 与 env_file 必须指向同一文件；自定义路径需两处一致。模板、配置决策表、迁移回退、TLS、两类 secret 和副本重启/排空说明同步。
- SDK：`node scripts/check-sdk-coverage.mjs` 通过，35 个消费者/管理员操作与 OpenAPI 一致。登记文件只覆盖 enduser/admin，内省/feed 在此协议集合之外，并非存在专门的排除条目；本次不修改 SDK。

### 实际执行结果

环境为本任务专用 Docker network、PostgreSQL 17、Redis 7、Linux runner，以及两个不同的空恢复数据库；Go 1.26.5、GCC 14、pg_dump/pg_restore 17.11。全部 Go 命令设置 GOWORK=off。

| 检查 | 当前结果与证据 |
|---|---|
| 实施前 accountsvc 基线 | `go test ./internal/accountsvc/...` 通过；此基线运行不作为真实集成证据 |
| TDD 与阶段测试 | 注册表先写测试观察缺失 API 编译失败；配置、运行时与模板观察缺失行为失败，再实现并绿灯。未宣称每个补充边界测试单独观察过 RED |
| 定向配置/认证/协议与 vet | 通过；日志和阶段详情见 `.test-output/unify-service-client-auth/`，其中 task3-red.log 是实际装配行为失败 |
| 新增真实服务测试 | `go test -race -count=1 -timeout=4m ./internal/accountsvc -run TestServiceIntegrationUnified` 通过；父/子进程均 race，5 个新顶层集成用例，task3-integration.log |
| 模板与实际 Compose 展开 | 主机模板 4 个子例、Docker Compose v5.7.0 实际展开 5 个子例全部通过，无 skip；含默认旧模式及动态下划线别名。task-4-examples.log |
| 独立 Linux race 构建 | `go build -race ./...` 通过；unify-auth-race-build.log |
| 完整仓库验证 | `bash scripts/verify.sh` 退出 0；迁移历史、build、vet、`go test -race -json -count=1 ./...`、必需测试门禁通过。54 个有测试包通过，1226 个顶层测试通过，零失败；tests.jsonl、unify-auth-full-result.txt |
| 真实备份恢复 | verify.sh 内执行 seed、verify、source-unchanged 三阶段及真实 pg_dump/pg_restore，全部通过；`.test-output/recovery.jGGeBM/` 保留合成数据和原始证据 |
| accountsvc 专项 | `bash scripts/verify-accountsvc.sh` 退出 0；build、vet、race、真实 TLS/OIDC/SMTP fixture、实际 SIGTERM 与带 race 的子服务进程、必需用例门禁通过；accountsvc-tests.jsonl、unify-auth-service-result.txt |
| OpenSpec 当前变更 | `openspec validate unify-service-client-auth --strict --json` 通过，无错误或警告 |
| OpenSpec 全库 | `openspec validate --all --strict --json` 44 项中 37 通过、7 失败，见下述基线问题；unify-auth-openspec-all.json |
| 格式和范围 | Go 文件已 gofmt；git diff --check 无空白错误；主规格与 origin/develop 无差异，领域/迁移/模块文件无差异 |

上表未另列目录的原始日志均在被忽略的 `.test-output/`；不提交合成数据库备份或凭据。首次完整验证命令曾因自动权限审批超时未启动，正常重试后完成；未绕过权限控制。

验收结束后已核对容器 ID，并清理本任务创建的三个测试容器及其匿名数据卷、专用 network 和两个缓存卷；未操作其他容器或数据库。代码、测试输出和合成恢复证据保留。

### 跳过项、基线问题与审查

- Linux 两套件中的 TestConfigServiceClientComposeExamples 因镜像不含 Docker CLI 而跳过；该项已在 Windows 主机实际执行并全部通过，未列入必须具备 Docker CLI 的服务门禁。
- 两套件中的既有 TestProfileAttributesUnreadableFile 因容器 root 可读无权限文件而跳过；该权限分支在本环境未验证。必需的其他资料文件读取失败用例照常通过，未删除或放宽原门禁。
- TestRecoveryFixture 在普通套件按既定契约跳过，已由恢复脚本强制执行 seed/verify/source-unchanged 三阶段；不是缺少恢复验证。
- 全库严格校验失败仍全部来自未修改主规格的 >500 字符正文警告：accountsvc-runtime、accountsvc-service-authentication、consumer-token-introspection、embedded-auth-package、purged-user-feed、verification-delivery、versioned-auth-storage。未将全库严格校验报告为通过。
- 四个实现步骤分别由独立审查者核对规格及代码/文档质量，均通过；整体最终审查未发现 Critical/Important 问题。最终审查的小建议为移除 tasks 中过时的未实施说明，已修正。
- runtime delta 补齐旧“已清理用户拉取凭据”条目的适用模式，避免归档后与 D1/D3 冲突；只澄清原设计，不扩展产品行为。
- 执行选择：在现有任务分支原地实施，保留未提交工作；运行时和文档/部署在文件不重叠的范围并行，最后统一审查和验收。没有扩大为提交、合并、发布或归档。

## 提交前复核（2026-11-14）

- 用户在验收后要求“先提交”，本次仅创建本地提交；旧模式及可选 Compose override 保留，不推送或归档。
- 暂存区仅包含本变更 36 个文件；git diff --cached --check 通过，工作区与暂存区一致，没有夹带实际环境配置、测试输出或备份。
- 提交前重新执行 `GOWORK=off go test -count=1 ./internal/accountsvc/... ./examples/remoteauth`，通过。此为 Windows 快速回归，数据库/进程用例按平台与依赖条件跳过，不作为新的完整集成验证；完整 Linux 验收证据仍见上文。

## FF 工件核验（2026-11-14，实施前历史记录）

- 用户要求更新到最新远端 develop：首次 GitHub 连接重置，随后 HTTP/1.1 fetch 成功。原 develop 与任务分支均只落后 191 个提交，已快进到 ac3eff1f076f52d4b58b8e43269939342c4a0416；HEAD、develop、origin/develop 三者一致。提案完整保留，当前分支 feat/unify-service-client-auth。
- 已读取新 AGENTS.md、现有运行/服务认证/内省/清理规格及配置、认证、装配、协议测试。能力列表已修正为修改四项现有能力，不新增近似能力。
- proposal、design、四份 delta specs、tasks 均已生成；OpenSpec CLI 1.14.3 状态 isPlanningComplete=true，四类工件 done。18 个实施任务全部未勾选，未实施产品代码。
- 独立 builder-check 审阅 proposal/design：nothing important left。此为设计可实施性审阅，不代表代码审查或产品测试通过。
- openspec validate unify-service-client-auth --strict --json：1 项通过，0 错误、0 警告。
- openspec validate --all --strict --json：44 项，37 通过、7 失败；本 change 通过，失败全部来自未修改的主规格中的 >500 字符需求正文警告。分别为 accountsvc-runtime、accountsvc-service-authentication、consumer-token-introspection、embedded-auth-package、purged-user-feed、verification-delivery、versioned-auth-storage。git diff --quiet origin/develop -- openspec/specs 返回 0，确认这些文件与基线一致；未为校验擅自拆分主规格或删减既有需求。
- 文件检查：四份 capability delta 与 proposal 一致；引用的实现路径和能力名称已核对；文件无尾随空白；tasks 以 UTF-8 读取计数为 18 个未完成、0 个完成。
- 本轮没有运行 Go 构建、单元测试或数据库/race 验收；本轮仅创建规划文档。未提交、推送、发布、部署或归档。

## 初始提案阶段记录

初始提案基于本地 a561e54（v0.6.0 归档），当时仅 proposal 完成，严格校验因尚无 delta specs 未通过。本轮按用户要求更新远端基线并补齐工件，消除了该提案阶段错误；此前说明不作为本轮完整性或产品实现证据。
