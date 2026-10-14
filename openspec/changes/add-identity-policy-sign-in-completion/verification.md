## Scope

变更 `add-identity-policy-sign-in-completion` 已完成本地实现和完整验证。提案提交 `9b0cb03` 已以独立 merge commit `6d8f3ec` 合入本地 develop；实施位于 `feat/identity-policy-sign-in-completion`。最终代码验证版本为 `27d738b`，其后只整理设计状态、任务和本验收记录。

OpenSpec 共 35 项任务，34 项完成；8.5 真实微信/Apple 联调按用户明确选择保留未验证。该变更尚未归档，实施分支保留供后续集成。

## Implementation

- EnabledIdentityKinds / RequiredIdentityKinds 已接入库和服务；Required 仅 PHONE/EMAIL、any-of，保留 nil 与显式 [] 的区别、旧 Disabled 装配和无 I/O 构造。
- 未知身份不足时仅返回 pending；补充 PHONE/EMAIL 后自动关联已有活动账号或原子注册。专用随机票据、加密证明、两阶段事务、归属复核、受约束幂等、TTL、密钥检查和匿名化清理已实现。
- C 端和管理端账号路径与资源 name 使用 accounts，新增两个独立补全端点；JWT、user_id、u_、scope 和 Go 门面保持约定。管理列表 JSON users 字段保留，本次改名范围为路径、name 与资源引用；未来若更改该字段需另行客户端迁移。
- SIGN_IN_COMPLETE 与流程绑定沿用共享额度和失败预算；流程审计具备脱敏 FlowID 关联。审计批处理持有连接至所有关闭处理完成，保持异步失败不影响认证结果。
- accountsvc 按策略装配 SMTP，serve 明确拒绝不支持的 PHONE 或缺少应用配置的显式 IdP；migrate 使用独立必要装配，网关明确允许两个补全端点。
- README、兼容/服务/网关/迁移说明、模板及根集成、恢复夹具已同步。[详细实现计划](../../../docs/superpowers/plans/2026-10-14-identity-policy-sign-in-completion.md)。

## Final Verification

日期：2026-10-14。环境为 Go 1.26.5 Linux、CGO/race、PostgreSQL 17.11、Redis 7.4.3，使用本次创建的独立测试资源。

| 检查 | 实际结果与证据 |
|---|---|
| GOWORK=off 完整 scripts/verify.sh | 最终第三轮容器及脚本退出码均为 0；[主日志](../../../.test-output/identity-policy/final20261014r3/verify.log) |
| build、vet、完整 race 测试 | 30 个有测试的包通过，1361 个测试/子测试通过，0 fail；[JSON 结果](../../../.test-output/identity-policy/final20261014r3/tests.jsonl)、[统计摘要](../../../.test-output/identity-policy/final20261014r3/summary.json) |
| 必需测试门禁 | 441 条全部通过；包含新增真实数据库、Redis、服务进程及审计并发回归。旧微信测试仅一对一同步改名，没有删除门禁或放宽 skip |
| 迁移历史 | 完整 verify 内通过；冻结 0001 与 source-baseline 保持，未发布 0002 包含 identity_flow 和可空审计 flow_id |
| PostgreSQL 17 备份恢复 | 独立新空源/目标库 seed、verify、source-unchanged 均通过；[恢复证据](../../../.test-output/identity-policy/final20261014r3/recovery.h6B5wP/verify.jsonl)。覆盖新表、旧加密 key、完整 token pair 重放、Redis 证明缺失拒绝及源库保持 |
| sqlc 生成一致性 | scripts/check-generated.sh 退出 0；[日志](../../../.test-output/identity-policy/final-generated.log)，生成文件没有手工修改 |
| OpenSpec | openspec validate --all --strict --json：16/16 通过，0 fail；[最终日志](../../../.test-output/identity-policy/final-openspec.json) |
| 文档与差异 | 检查路径、引用、当前设计状态及 git diff --check；仅保留已确认的外部联调未验证项 |

普通 suite 仅 TestRecoveryFixture 按契约跳过，随后恢复脚本已强制执行三个阶段；无测试文件的包不计为测试跳过。Windows 专项检查中的 Linux 平台 skip 不作为验收证据，最终 Linux 服务真实用例均已执行，包括 migration_dependency_failure。

## Review And Corrections

所有实现工作包均完成独立规格/代码审查；整分支审查及其限定复审已完成，无未处理的实质问题。主要修复及证据如下：

- 策略访问器改为独立快照，避免外部整体赋值修改运行策略；23a420e。
- Redis 使用 TIME 与 PEXPIREAT 约束补全验证码绝对期限，排队不续期；c11545b。
- 换绑策略拒绝进入原脱敏失败审计；2acf7e1。补全设备名称保持原值；f629792。
- 活跃流程使用稳定逻辑实例上下文，允许恢复到不同物理数据库；31ec755。两处集成/恢复回放断言比较完整 TokenResult；af8e1d9。
- FlowID、阶段/拒绝审计、早期匿名审计清理及 Redis 查询后期限复核；a9be2a4。票据在 Redis 等待中到期的错误分类、fresh load 故障保留及缺失 IdP 配置提示；4a1a088。
- 完整 race 新发现的审计批处理连接复用缺陷修复于 27d738b：真实旧实现 RED，修复后 audit 全套 race 10 轮与原 user 故障用例 20 轮通过，0 fail/skip/race；[审计回归](../../../.test-output/identity-policy/audit-race-fix/green-audit.jsonl)、[用户回归](../../../.test-output/identity-policy/audit-race-fix/green-user.jsonl)。延迟提交错误测试及 overlay 变异确认没有通过丢弃最终 Close 错误规避问题。该修复也通过独立限定审查和最终完整 verify。

流程证明/完成成功审计对应各自已提交事务；最终事务失败不会产生业务完成成功事件。匿名化清理涵盖已落库的早期流程事件，晚到异步队列继续遵循既有审计契约，不声称跨队列零 PII 保证。

## Earlier Runs

- 固定基线 6d8f3ec 的完整 verify 已通过，仅作为变更前基线，证据在 .test-output/identity-policy/baseline-src/.test-output/。
- 单独恢复演练先发现物理数据库名进入 AAD 的缺陷，修复后重新使用新空库通过；旧材料保留在 task7-recovery-*，不替代上面的最新恢复结果。
- 首轮 final20261014 的全部 race 测试通过，但必需清单保留了已改名的旧微信测试，门禁失败，未执行恢复。e1305a0 一对一同步名称；[原日志](../../../.test-output/identity-policy/final20261014/verify.log)、[门禁复验](../../../.test-output/identity-policy/task8-gate-fix.log)。
- 第二轮 final20261014r2 捕获真实审计连接竞态，不能算通过；[原 JSON](../../../.test-output/identity-policy/final20261014r2/tests.jsonl)。第三轮在修复后完整通过。

## Not Verified

用户明确确认暂无真实微信/Apple 测试应用和配置，真实提供方既有登录、普通绑定及首次补全联调保留未验证（任务 8.5）。本地 TLS 提供方夹具只证明库与服务的协议集成，不替代外部服务商验收。

本记录证明本地代码与一次性依赖环境验收，不代表已发布版本、在线 CI 或目标生产部署已验收。

## Resources

仅使用本任务创建的 PostgreSQL、Redis、验证容器、网络和缓存卷；测试使用合成数据与隔离 schema。上述一次性资源已全部回收，共享验证镜像与工作区保留；[回收记录](../../../.test-output/identity-policy/cleanup.log)。原始日志、JSON、备份及资源记录保留在被忽略的 [.test-output/identity-policy/](../../../.test-output/identity-policy/) 与 [环境记录](../../../.test-output/identity-policy/environment.txt)。本计划临时工作目录已单独清理，[原始审查与执行材料归档](../../../.test-output/identity-policy/review-evidence-27d738b.zip) 保留供核对。
