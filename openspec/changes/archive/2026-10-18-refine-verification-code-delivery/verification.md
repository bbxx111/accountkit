# 验证记录：refine-verification-code-delivery

日期：2026-10-15。状态：实施完成，15/15 任务完成；尚未归档。

## 交付与提交

- 提案提交：8e7e5b4；以独立 merge commit 78182f5 合入 develop。
- 实现分支：feat/verification-code-delivery；隔离工作树由 Codex 原生工具创建。
- 类型兼容迁移：321a439；可注入 SMTP 渲染：3da40aa；宿主装配、本地化示例和文档：e68f288。
- 长标题编码与折行：c6f9801；编码标题物理行标准校准：1e19c72。
- 最后测试夹具修复：aa38b17，仅修改 user/service_challenge_test.go；生产代码与 1e19c72 相同。
- 实施计划见 [详细计划](../../../../docs/superpowers/plans/2026-10-15-verification-code-delivery.md)，需求见 [增量规格](specs/verification-delivery/spec.md)。

## 实现范围

SMSCodeSender、EmailCodeSender、CodeMessage 成为正式类型，旧名称为 Deprecated 别名；原方法、消息字段和 Deps 字段保持兼容。SMTP 新增 NewWithRenderer、Renderer、EmailContent，独立 LegacyChineseRenderer 保留旧 New 的四用途中文语义。accountsvc 显式装配兼容渲染器；examples/codeemail 展示宿主固定语言和请求级中英文选择、默认回退及安全模板。

渲染失败、无效内容与取消均沿用固定安全错误和原轮次、额度、审计语义。没有新增语言配置、通知/审计事件框架、依赖、SQL 或存储格式。

## 当前验证证据

Linux 验证使用 Go 1.26.5、CGO/race、PostgreSQL 17.5、Redis 7 和 PostgreSQL 17.11 客户端，均连接本任务独立合成测试资源。测试套件使用 GOWORK=off。

| 检查 | 对应代码与结果 |
|---|---|
| bash scripts/verify.sh | aa38b17：退出 0；包含迁移历史检查、go build ./...、go vet ./...、go test -race -json -count=1 ./...、全部必需测试检查及真实恢复演练 |
| 全库 race 事件 | 31 个测试包通过、1449 条测试通过事件（含子测试）、0 失败；唯一测试级 skip 是 TestRecoveryFixture；另有 4 个无测试文件的包级 skip |
| 恢复演练 | 独立空源库和目标库；seed、verify、source-unchanged 均通过；pg_dump/pg_restore 成功，认证与 identity-flow 数据、解密、令牌及源库未改动检查通过 |
| bash scripts/verify-accountsvc.sh | 1e19c72：退出 0；5 个测试包通过、393 条测试通过事件、0 失败、0 测试级 skip；保留脚本原 5 分钟时限，真实服务子进程同样启用 race |
| 服务验证适用性 | 1e19c72 到 aa38b17 仅 user/service_challenge_test.go 变化，服务及依赖生产代码、服务测试和门禁脚本完全相同，故不重复已通过的服务套件 |
| Windows 分步验证 | 各步相关测试、全套快速测试、build/vet 通过；其数据库及平台 skip 仅作快速反馈，完整验收以上述 Linux 真实依赖证据为准 |
| bash scripts/check-generated.sh | sqlc 1.31.1，退出 0；生成文件无差异，本变更无 SQL/schema/sqlc 配置修改 |
| OpenSpec | openspec validate --all --strict 通过，18 项、0 失败；最终任务状态再随交付检查核验 |
| Git 差异 | git diff --check 78182f5 HEAD 通过；migrations、tests/testdata/source-baseline、user/db、audit/db、go.mod、go.sum 无差异 |

当前原始证据在本工作树的忽略目录：

- [完整库日志](../../../../.test-output/linux-final/library-verification.log) 与 [测试事件](../../../../.test-output/linux-final/tests.jsonl)。
- [服务日志](../../../../.test-output/linux-final/service-verification.log) 与 [服务测试事件](../../../../.test-output/linux-final/accountsvc-tests.jsonl)。
- [最终恢复材料](../../../../.test-output/linux-final/recovery.yG8WC5/)。
- [分步与审查协作材料](../../../../.test-output/delivery-review/)。
- [控制器汇总状态](../../../../.test-output/linux-final/final-status.json)。该文件区分库与服务检查的提交；先前驱动脚本状态保留为历史文件，不代替本次汇总。
- 上述本地原始材料不提交 Git，其他检出环境只会获得此验收记录。

## 审查与修复

三个实施步骤分别完成独立规格符合性及代码质量审查，均通过；最终整分支审查和后续定向复核均由独立审查者执行。

1. 最终审查发现新自定义长 Subject 未折行：真实 SMTP 复现 Unicode 1103 字节、ASCII 1309 字节等过长物理行，并发现 ASCII 尾部空白丢失。修复以完整 UTF-8 字符分段、标准 Base64 编码词和安全续行保存原文。
2. 复核 [RFC 2047 §2](https://www.rfc-editor.org/rfc/rfc2047#section-2) 后，将分段收紧为最多 39 字节：编码词最多 64 字符、Subject 首行最多 73、续行最多 65。回归同时验证编码词合法、Subject 各物理行不超过 76 字符、完整解码及空白保真，原注入拒绝和短标题行为通过。
3. 最后全库并发验证暴露原 Redis 故障夹具的端口重用问题，见下节；测试修复独立审查确认未弱化错误、空轮次和零发送断言。

最终结论：所有审查发现已解决，无未处理 Critical/Important/Minor 或搁置事项。实现阶段的新 API 和边界行为均记录了 RED/GREEN 证据；既有行为回归未冒充新功能 RED。

## 验证过程中出现的问题

### Windows 挂载目录下服务套件超时

e68f288 的完整库门禁已通过，但服务专用门禁在 Windows bind mount 的 /src 路径达到原 300 秒上限。堆栈位于 fixtures_integration_test.go:296 等待 go build -race，超时时新用例才执行约 3 秒，前面的邮件闭环、身份策略、启动安全及 SIGTERM 等已通过。

在同一容器、同一代码和同一依赖下，只把检出目录换到 Linux 原生 /work：预热后的构建从 3.466 秒降为 1.159 秒，最长身份策略用例从约 108.68 秒降至约 31.217 秒包耗时。原始服务门禁随后在 c6f9801、1e19c72 均通过，未调整超时、关闭 race 或删减门禁。最初日志保存在 .test-output/linux-verify/.test-output/，原生中间通过证据保存在 .test-output/linux-native-c6f9801/。

### Redis 故障测试释放端口导致非确定性

1e19c72 的服务门禁通过，但全库 race 曾在 TestCodeChallengeDeliveryCleanup/redis_unavailable_returns_dependency_error 失败：关闭模拟 Redis 后反而收到成功轮次。该用例的 Redis 客户端为惰性连接，关闭服务释放端口，使另一并行夹具可能接管该地址。

保存原地址、关闭原服务并用健康 miniredis 监听同端口，可连续 3 次稳定复现原断言失败。aa38b17 把该子用例改为 mr.SetError 保持端口并拒绝所有命令；原 ErrUnavailable、空 CodeID、零发送断言均保留。它现在明确模拟 Redis 命令拒绝服务，不专门模拟 TCP 连接拒绝。定向 100 次、user 包和 vet 通过，最终 aa38b17 完整 Linux race 与恢复门禁再次通过。

失败原始证据保存在 .test-output/linux-native-1e19c72-failed/；定向复现证据在 .test-output/task-2-redis-outage/。未删除失败记录，也未把先前通过结果替代最后验证。

## 资源与未验证边界

本次只创建并使用独立测试网络、数据库/Redis/验证容器和合成数据；已清理这些容器及网络，原始日志、合成备份和实现工作树保留。既有 postgres、redis 容器未操作。

SMTP 验证使用隔离 TCP/TLS fixture，未验证外部商业短信/邮件服务商的实机送达或生产部署。宿主渲染器仍负责语义正确、本地化策略、HTML 转义和合作式取消。未推送、发布或归档；用户要求的本地提案合并已完成，实现保存在独立功能分支。

## 归档核验（2026-10-18）

用户明确要求归档已完成 change，本次在 chore/archive-completed-changes 分支完成归档，目录为 openspec/changes/archive/2026-10-18-refine-verification-code-delivery。本节补记后续归档动作，前文实施、提交和验证边界保留为当时记录。

归档前将 verification-delivery 的三个 ADDED 和两个 MODIFIED requirements 同步到主规格，包含验证码类型兼容、可注入渲染、语言上下文与投递失败语义；保留其他要求和全部既有场景。

本批三个变更共 24 项任务均已完成，归档保留 .openspec.yaml、提案、任务及已有设计、规格和验收记录，修复迁移后的相对链接及外部引用。实际归档检查：

- 使用仓库忽略目录中的 OpenSpec 1.11.0 CLI 执行 validate --specs --strict 与 validate --all --strict，均为 19 项通过、0 失败；list --json 返回空活动变更列表。
- 逐项复核两份增量的 7 个要求均已同步，原有场景和未涉及要求保持。
- 归档后的 13 份非验收工件与 HEAD 原路径内容一致（按 Git 工作树换行转换规范化 CRLF/LF 比较）；检查 19 份 Markdown 的 45 个本地链接，目标均存在，未遗留旧活动目录引用。
- git diff --check 通过。

本次仅同步规格、移动工件及维护引用，未修改运行时代码、SQL 或固定源快照；未重跑 Go、数据库、race 或恢复演练，也不将历史验证记录视为本次运行结果。归档阶段未提交、合并、推送或发布。

## 合并前复核（2026-10-19）

用户明确要求本地合并。本轮重新执行 OpenSpec 全量严格校验（19 项通过、0 失败）、活动变更查询（空）、归档内容与本地链接检查（13 份非验收工件保持、19 份 Markdown 的 45 个链接均存在）及 git diff --check，均通过。develop 与任务分支基点同为 7a829c1，没有额外待整合的代码差异。按仓库约定提交归档改动后使用 git merge --no-ff 合入 develop；本次授权不包含推送或发布。纯文档和规格维护未重跑运行时测试。

## 中文记录恢复（2026-10-19）

后续标准化指标提案检查发现，前两节通过默认 us-ascii 的 PowerShell 管道传给 Node，新增中文已被替换为问号。此处按原操作与验收证据恢复中文，之前的实施记录保持；不代表重新执行了历史运行时测试。
