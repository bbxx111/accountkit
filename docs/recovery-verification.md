# 本地恢复与发布验证记录

验证日期：2026-10-03。对象为 accountkit 首版候选代码；尚未创建发布 tag、push 或执行产品部署。

## 环境与执行方式

- Go 1.26.5；Linux Docker 环境启用 CGO/race。
- PostgreSQL 服务端 17.11（postgres:17-alpine）；pg_dump/pg_restore 17.11（Debian 17.11-0+deb13u1）。
- sqlc 1.31.1；Redis 测试使用 miniredis。
- 固定来源：ai-food 的 7b4c4ecfdba4a05d54810aa2152b40d5c7da00c2，130 个源文件清单。0001 SQL 与 manifest 均基于 git cat-file blob 原始字节；已在临时提交及干净克隆后复验。
- 数据库全部位于临时容器 accountkit-test-20261003，账号及身份都是合成数据。最终演练使用 accountkit_recovery_source_clean 和 accountkit_recovery_target_clean 两个空库，普通集成测试使用 accountkit_test。

最终验证在临时 Git 干净克隆运行，正式仓库未提交。以下命令中的 DSN 由运行环境指向上述一次性测试库，真实凭证不写入文档：

```bash
bash scripts/check-generated.sh
# 设置 SERVER_TEST_DB_DSN、ACCOUNTKIT_RECOVERY_SOURCE_DSN、ACCOUNTKIT_RECOVERY_TARGET_DSN
bash scripts/verify.sh
```

verify.sh 实际执行历史校验、GOWORK=off 独立构建、go vet、go test -race -json -count=1 ./...、必需测试核验及 verify-recovery.sh。恢复脚本执行种子数据生成、pg_dump 自定义格式备份、pg_restore 到独立空库、恢复核验及源库不变核验。

## 实际结果

| 检查 | 结果 |
|---|---|
| 独立构建、vet、sqlc 生成一致性 | 通过；独立临时宿主 module 编译也通过 |
| 完整 race 测试 | 286 个常规顶层用例通过，无失败，所有必需用例实际执行 |
| 旧库接管 | 四类记录不变；旧 access/refresh 可用；身份可解密；原手机号再次登录仍命中同一账号 |
| 多实例及宿主契约 | schema/Redis/令牌域隔离；管理员未配置返回 503；匿名化回调失败回滚业务和认证数据 |
| 版本升级与失败 | 合成下一版本只执行一次且保留数据；失败留下 dirty 并阻断重试及后续版本 |
| 并发与锁 | 新/旧 schema 并发通过；外层锁和旧工具引擎锁等待可取消；版本表初始化失败释放锁，修复后可重试 |
| 破坏性入口 | Down 无 I/O 拒绝；UnsafeReset 需精确确认且不影响其他实例 |
| 历史不可变 | 改写、删除、重编号、非递增新增被拒绝；合并分支及 HEAD 发布 tag 受保护；合法追加通过 |
| 备份恢复 | version/dirty、用户/身份/会话/审计快照一致；重复迁移不变；身份解密、旧 access/refresh 通过；源库未改变 |
| 脚本保护 | 缺少配置、别名 DSN 指向同一实际库、非空源库、非空目标库均拒绝；guard 数据库 sentinel 仍为 preserve |

TestRecoveryFixture 是演练的跨进程工具，常规全套测试明确跳过；恢复脚本在 seed、verify、source-unchanged 三个阶段分别强制执行并核验通过，不将这个预期 skip 算作集成验证。

本地证据（被 Git 忽略，不属于发布包）：

- .test-output/clean-clone-evidence/clean-verification.log
- .test-output/clean-clone-evidence/tests.jsonl
- .test-output/clean-clone-evidence/recovery.stgShp/：备份、合成状态、三个阶段的 JSON 测试结果
- .test-output/missing-db.log、same-db.log、nonempty-source.log、nonempty-target.log：前置保护验证

## 数据损失窗口与适用边界

演练在写入合成种子后没有并发业务写入，因此只能证明该备份快照被正确恢复，不代表生产 RPO 为零。实际恢复会丢失备份时间点之后的写入，产品方须确定备份频率、停写/切换安排及可接受窗口。

数据库备份不包含 Redis 和密钥系统。产品恢复前需核验历史签名、HMAC、加密密钥及 session/吊销/刷新宽限状态；不能通过清空 Redis 宣称安全恢复。实际短信、邮件、微信/Apple、管理员身份与产品预发布环境仍由宿主验收。

本记录证明包级本地发布门禁通过；GitHub Actions 已配置，但未声称在线运行通过。两个产品尚未切换到 accountkit。
