# 验证码密钥轮换验收记录

日期：2026-10-04。对应 [harden-code-key-rotation](tasks.md)，行为与部署条件见[设计](design.md)及[规格](specs/code-key-rotation/spec.md)。三个实现步骤已有定向验证和独立审查，库完整门禁、严格服务门禁与恢复三阶段已通过；文档审查和最终整体审查均 Approved，本变更实施验收完成。归档、合并和推送另行执行。

## 实施与定向证据

| 步骤 | 提交与本次证据 |
|---|---|
| 多版本 Store/Lua | `a718885`：Task 1 在生产修改前复现跨 active 旧码失联、冷却/额度分裂、原次数与冲突/替换失效、并发不满足一胜及畸形结果未 fail-closed。修改后 `GOWORK=off go test -count=1 ./user/code ./pii`、相关 vet 通过，七个新增顶层行为测试覆盖快速 Redis 语义 |
| 库与真实 Redis | `48b8ee0`：Task 2 最终 `go test -json -count=1 . ./user/code -run 'TestCode(KeyRotationIntegration|Rotation)'` 退出0，七个 Store 顶层测试及两个新集成顶层测试均 PASS，无 fail/skip；九项必需测试经 testgate 核验，相关 vet 通过 |
| 实际服务进程 | `7351177`：Task 3 最终 Linux `go test -race -v -count=1 -timeout=5m ./internal/accountsvc -run '^TestServiceIntegrationCodeKeyRotation$'` 退出0，1个顶层测试 PASS、0 fail/skip；开启 `ACCOUNTSVC_TEST_RACE=1`，服务二进制也以 race 构建，相关 vet 通过 |
| 缺依赖负例 | 未提供真实 Redis 时，两个新增集成测试在顶层明确 SKIP，同一必需门禁退出1；`scripts/verify.sh` 缺 Redis 配置时退出1，未进入数据库/恢复动作 |

Task 2 使用真实 PostgreSQL/Redis 和公开 `New → Migrate → Start → Close`、消费者 HTTP 面，验证 PHONE/EMAIL 的 SIGN_IN、BIND（含换绑）和 REAUTH 双向旧码消费，重建后的原账号复用、身份变更、同账号/会话 auth_time 更新、原错误/审计、投递失败后另一 active 的冷却和目标额度拒绝、实例隔离。

真实 Redis 测试使用原始旧 `h`/`n` hash 夹具，核对原验证码 PTTL、错误次数和到期、旧冷却、跨 active 目标计数求和、IP 单次增加、20路并发发码一胜和同码消费一胜。旧日额度 TTL 测试检查其仍处于原约1小时范围；Task 2 审查指出该范围断言不足以单独排除 TTL 被重设为1小时，当前不将其表述为绝对截止时间的独立证明。

实际服务测试通过本地 TLS SMTP 收码并调用登录及内省，两个 active 的真实进程双向消费，仅切 active 重启后旧码复用同一账号/身份；目标计数 K1=2、K2=1 时两个版本均拒绝继续发送，唯一 IP 达到5次时两版本均拒绝新目标且不占目标额度。最终运行启动四个实际服务进程，顶层耗时28.11秒、包29.214秒；进程输出、拒绝响应和退出后持久审计均检查秘密未泄露。

## 完整门禁状态

| 检查 | 当前状态 |
|---|---|
| `scripts/verify.sh` | 协调者执行退出0；基于生产/测试 HEAD `7351177`，GOWORK=off 下独立 build、vet、Linux race、必需测试门禁、迁移历史检查及真实备份恢复通过 |
| 普通完整套件及 skip 分类 | 本轮 `tests.jsonl` 核验402个顶层 PASS、0 fail，唯一顶层 skip 为 `TestRecoveryFixture`；本轮实际服务用例也已 PASS，恢复 fixture 由三阶段分别证明 |
| `scripts/verify-accountsvc.sh` | 协调者执行退出0，57个顶层 PASS、0 fail/skip，服务包211.079秒；实际服务二进制同样启用 race，新增轮换用例30.17秒，必需服务门禁通过 |
| 恢复演练 | `recovery.42wVvM` 的 seed、verify、source-unchanged JSON 各确认 `TestRecoveryFixture` PASS；PostgreSQL17.11 的 pg_dump/pg_restore 成功，核对版本、四表快照、解密、access/refresh 和源库保留 |
| 生成/迁移历史检查 | 协调者已执行 `check-generated.sh` 与 `check-migrations.sh` 并报告通过；完整入口也通过历史迁移检查，本次无 SQL、生成代码或冻结基线变更 |
| 根库独立构建与依赖图 | 完整 build 通过；协调者 `GOWORK=off go list -deps .` 确认根库不依赖 `internal/accountsvc` 或 `cmd/accountsvc`；与 `055b542` 比较迁移、领域 query/db、go.mod/go.sum、config.go 和 pii 无变化 |
| 文档链接、差异与 OpenSpec strict | Task 4 的34个本地 Markdown 路径均存在，文档差异格式检查通过；协调者执行 `openspec validate --all --strict` 11/11通过、退出0 |
| Task 4 与最终整体审查 | 文档独立审查 Approved；最终整体审查覆盖 `055b542..76332eb`，结论 Approved / Ready to merge，无 Critical/Important；后续仅补录本记录及任务状态 |

环境为 Go 1.26.5、Linux CGO/race 验证镜像、PostgreSQL17和真实 Redis7；Task 2 定向验证使用 Windows Go，Task 3 使用 Linux 实际服务进程。仅本任务创建的一次性 PostgreSQL/Redis 容器和网络参与验证，集成测试各自使用随机 schema/Redis 前缀并精准清理，没有 FLUSHDB 或产品库操作。恢复源、目标为两个独立专用空库，由协调者统一执行恢复演练。

原始证据保存在被忽略的 `.test-output/`：Task 2 为 `rotation-task2-tests.jsonl` 与 `rotation-task2-skipped.jsonl`；Task 3 为 `task-3-rotation-race.log` 与 `task-3-rotation-vet.log`；完整库门禁为 `rotation-full-verification.log` 和 `tests.jsonl`，恢复三阶段、合成状态和 dump 为 `recovery.42wVvM/`。严格服务日志为 `rotation-service-verification.log` 和 `accountsvc-tests.jsonl`；凭据、备份与原始日志不提交到仓库。

## 审查与范围边界

四个实施计划步骤均经过非作者独立审查，最终整个分支审查结论 Approved，无 Critical/Important 问题。最终审查将预期断连用例的客户端日志噪声、上述日额度 TTL 断言精度、实际服务测试跨 UTC 午夜时的配额日稳定性三项保留为非阻断 Minor，列为后续测试维护建议，未声称已修复；它们不改变本次已验证的生产行为。

本次保留公开接口、配置/默认值、HTTP 错误、Redis 键及 hash 格式、SQL/数据库结构、迁移与依赖；未新增命令、端点、热加载、自动轮换或手动回填动作。周期性数据库回填及默认间隔保持，混合 active 期间方向可能变化。退役条件区分旧 HMAC 摘要引用及 Redis 业务窗口、旧 AES 密文引用和备份密钥保留，见[轮换说明](../../../README.md#密钥轮换)。根库仍按可嵌入契约设计，不要求部署 accountsvc。

产品环境、实际短信/邮件服务商、外部 OIDC 提供方、生产证书/网络、在线 CI、发布/tag/push 和部署按用户决定延期；本地真实协议 fixture 的通过不替代这些联调。本 change 的13项任务均已完成；本次不执行归档、合并或推送。

本任务创建的 code-rotation-20261004-pg、code-rotation-20261004-redis 容器及专用网络已清理；既有验证镜像保留，原始日志和合成恢复材料仍留在被忽略的 .test-output/。
