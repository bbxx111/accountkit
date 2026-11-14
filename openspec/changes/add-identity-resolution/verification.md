# Verification

## Scope

2026-11-15，变更 add-identity-resolution，分支 feat/add-identity-resolution。实施计划见 [详细计划](../../../docs/superpowers/plans/2026-11-15-identity-resolution.md)。已完成领域、服务 HTTP、权限、审计、协议登记与文档，16/16 任务完成。最终全仓库 race、必需测试与恢复门禁通过；全局 OpenSpec 既有长度告警仍单列。

接口为 POST /v1/identities:resolve，独立权限 identities.resolve；输入单个 PHONE/EMAIL，返回 user_id、state、display_name、avatar_url。当前未解绑身份可解析 ACTIVE、FROZEN、PENDING_DELETION，未匹配404，数据与关联采用单语句快照。

## Environment

- Go 1.26.5；sqlc 1.31.1；OpenSpec 1.14.3。
- 普通测试及最终 race 使用 accountkit-verify:local Linux 镜像，开启 GOWORK=off；race 开启 CGO，真实服务二进制也启用 race。
- 本任务独立创建 PostgreSQL 17、Redis 7 容器与专用 Docker 网络；普通测试库和两个恢复空库均为合成数据资源，不访问产品数据库。
- Windows Go 缓存和 Docker 需要正常工具提权；Git Bash 使用 C:/Program Files/Git/bin/bash.exe，PATH 中 WSL bash 无法启动，未改系统 PATH。

## Checks

| 检查 | 实际结果 |
|---|---|
| 新领域测试 | 12 个顶层测试使用真实 PostgreSQL 通过；最终超时修复后 8.375s，无 skip |
| 领域 Linux race | 通过，10.065s；最终新增两个超时码已由最终全仓库 race 覆盖 |
| 服务处理器／认证／装配 | accountsvc 包测试及 vet 通过 |
| 真实服务进程 | Linux race TestServiceIntegrationIdentityResolution 通过，13.532s |
| 构建与 vet | GOWORK=off go build ./...、go vet ./... 通过；最终修复后的完整入口再次通过 |
| 普通全套 | GOWORK=off go test -json -count=1 ./...：55 个包通过，无失败；该次在最后两个超时码修复前执行，修复后定向测试及最终全套 race 均通过 |
| SDK／服务覆盖检查器 | node --test scripts/check-sdk-coverage.test.mjs：13/13 通过；node scripts/check-sdk-coverage.mjs：36 个操作匹配，真实源码引用通过 |
| OpenAPI | node scripts/check-openapi.cjs 通过（既有消费者24、管理11）；既有本地 swagger-parser 校验 enduser/admin/service 三份文档均 valid |
| sqlc | sqlc generate 及 bash scripts/check-generated.sh 通过，未手改生成代码 |
| 迁移历史 | bash scripts/check-migrations.sh 通过；生产迁移文件无差异 |
| 变更严格校验 | openspec validate add-identity-resolution --strict 通过 |
| 全仓库规格 | openspec validate --all --strict：37 项通过、7 项既有主规格因 >500 字符 warning 在 strict 下失败 |
| 最终完整入口 | scripts/verify.sh 及服务必需测试核验均 exit 0；55 个包、1245 个顶层测试 PASS，无 FAIL |

全局 strict 的既有失败项（本次未修改对应主规格）：

- accountsvc-runtime：requirements[27]。
- accountsvc-service-authentication：requirements[6]。
- consumer-token-introspection：requirements[0]。
- embedded-auth-package：requirements[9]、[10]、[13]。
- purged-user-feed：requirements[1]、[3]。
- verification-delivery：requirements[0]。
- versioned-auth-storage：requirements[2]。

## Failure Investigation and Skips

首次全套普通测试在既有 TestAlipayPendingSanitizesAndCompletesWithoutVerifyReplay 的迁移前置连接阶段失败：通过 host.docker.internal 的宿主映射端口连接 PostgreSQL 超时，未进入业务断言。保留首次日志，改为本任务 Docker 网络内直接连接后，完整普通套件通过。没有跳过或修改该测试。

Linux root 容器中的 TestConfigServiceClientComposeExamples（无 Docker CLI）与 TestProfileAttributesUnreadableFile（root 权限）跳过；已在 Windows 宿主单独补跑两项，均 PASS（6.091s）。TestRecoveryFixture 在普通／race套件按项目约定 skip，由恢复脚本单独执行 seed、verify、source-unchanged 三阶段且全部通过。pg_dump/pg_restore 为 PostgreSQL 17.11。恢复证据位于 .test-output/recovery.dsCK65/。

## Review and Regression Evidence

- 规划阶段 builder check：修正审计 Action 为 IDENTITY_RESOLVE、目标 Type 为 USER；明确通用 Reader／外部审计可读取，管理员认证时间线不扩展。
- 领域独立规格／质量审查：单语句快照、多版本匹配、状态、资料与异步审计边界符合规格；发现 PostgreSQL statement_timeout 的57014分类缺口。先真实数据库复现失败，再修正为 ErrUnavailable；补故障审计和 MaxConns=1 连接获取预算测试，复核 PASS。
- HTTP 独立审查：权限、认证顺序、严格 JSON、错误分类、四字段结果及路由装配 PASS。
- 协议覆盖独立审查：显式 service 豁免不会放松消费者／管理员门禁，真实Go测试引用与协议一致，PASS；一处 Markdown 表格空行已修复。
- 最终整体审查发现同类超时遗漏：lock_timeout=55P03、PostgreSQL17 transaction_timeout=25P04。真实数据库先红后绿，最终三个服务器超时矩阵返回 ErrUnavailable、failure/unavailable且无目标审计；真实SQL缺失关系仍为内部错误，未把所有数据库错误一律变成503。最终限定复核 PASS，无剩余 Critical／Important／Minor。
- TDD 证据边界：领域入口、权限目录、HTTP入口、覆盖检查器及两轮超时修复均有先红后绿记录；审计初版实现后才补审计专属断言，未独立记录该部分最初红灯，不能声称每个审计场景都完整执行了独立 TDD 周期。

## Artifacts and Boundaries

原始日志保存在 [本次测试目录](../../../.test-output/identity-resolution/)，完整入口日志和恢复材料还使用项目标准 .test-output/tests.jsonl 与 recovery.* 目录。必需测试清单登记12个领域与6个服务相关新顶层测试。

代码保留于当前任务分支，提交按用户后续授权执行；本次验收不包含推送、归档、创建 tag 或部署。SDK 的平台构建与远端 Git 消费未运行，本次未修改 SDK 实现。全仓库 OpenSpec 既有长度问题单列，不以本变更严格校验通过替代全仓库通过。

任务结束后已核对 accountkit.task 标签并移除本任务 PostgreSQL、Redis 容器及专用 Docker 网络；既有镜像和缓存卷未清理。最后 git diff --check 通过，迁移文件无差异。
