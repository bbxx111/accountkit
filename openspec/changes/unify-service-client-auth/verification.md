# Verification

## Apply 最终验收（2026-11-14）

18 项实施任务已完成。实现计划见[统一服务客户端认证](../../../docs/superpowers/plans/2026-11-14-unify-service-client-auth.md)。验收完成时保留现有 `feat/unify-service-client-auth` 分支和未提交工件，未执行提交、推送、发布、部署或归档；后续本地提交授权见下方补记。

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
