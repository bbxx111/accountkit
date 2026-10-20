# 验证记录

日期：2026-10-20。基线：develop/b34810a567c2dd2b2da6bef9f7b226b4eed596a2；任务分支：chore/consolidate-session-context-schema。

## 授权与执行边界

用户明确要求“把 0002 直接并入 0001”，本轮仅整合当前工作树，历史 tag/SQL 和固定源保持。新安装 version=1，旧 v0.3.0 v1 与撤回会话 v2 均无写拒绝；没有运行现有实例迁移或清库，没有提交、推送、合并、发布或归档。

按用户明确指定的整合方案顺序实施，OpenSpec proposal/design/specs/tasks 及详细计划补齐后由 CLI 确认 apply ready（0/7）。当前目录创建任务分支，未新增 worktree；独立代理只用于最终只读审查。

## RED / GREEN

- 在一次性 PostgreSQL 17 上，先更新初始清单断言并增加完整 catalog、旧 v0.3.0 v1 和撤回会话 v2 的拒绝测试。整合前真实 RED：清单仍有生产 0002、初始 catalog 不完整、两种旧库的空/非空场景都被接受。
- 整合后 go test -count=1 ./migrations 通过（19.810s），覆盖新空安装、完整 catalog 等价、旧库无写拒绝/重复拒绝/dirty 优先、原有锁、并发及隔离。
- 新历史保护回归先因新接口缺失 RED；实现后 go test -count=1 ./internal/migrationcheck 通过（29.612s）。覆盖精确发布提交/哈希、移动标签、未来标签复用旧初始定义、当前定义改写及已发布增量改写。
- go run ./internal/migrationcheck 通过；sqlc generate 成功且生成代码无差异。

## 独立审查

review_schema_consolidation 完成只读审查，发现两项 Important 并已复核修复：

1. 原可达标签遍历会漏掉删除或移至 HEAD 不可达提交的固定 tag。先写回归并真实 RED（两个 legacy tag 的删除/不可达四场景均错误通过），再将三个固定 ref 的存在及提交校验移到遍历前；完整历史测试 GREEN（58.216s），补齐 required-tests。
2. 原拟文档声称旧 v0.3.0 会拒绝新同号 v1，实际读取精确 tag 的 migrations/baseline.go 可见只检查流程/审计，末尾直接 return nil。已更正三份操作/兼容文档和本次设计，明确旧代码可能成功启动，须由部署方人工阻止混跑和未经验证的回退。

最终审查：规格符合，无未解决 Critical/Important/Minor。审查未独立执行数据库/race/恢复，本轮实际门禁由下节证据负责；产品旧库转换及真实上线不纳入验收。

## 完整门禁

- 首轮完整 Linux verify.sh 的真实 RED：TestMigrateAuditConfigurationSingleConnection/fresh_repeat、TestHistoricalFormatsOnInitialSchema、TestServiceIntegrationIdentityPolicy 的两个 migration_lock 子场景及 TestServiceIntegrationStartupSafety 仍断言 version=2，实际为新基线预期 version=1/dirty=false。四处断言已改为 1，其余数据、dirty、锁等检查保留，cipher_version=2 和历史撤回 v2 未误改。首轮日志保留在 .test-output/consolidate-session/linux-initial/。
- 独立复核确认四处修改符合要求且无新增问题。随后从当前工作树重新复制完整代码，并重跑所有门禁，不沿用首轮失败结果。
- 测试代码快照清单 .test-output/consolidate-code-sha256.txt 的 SHA-256 为 e8daad4b240bb9d41c252453e12eaa6cea0523cc357697ad0e59f37bc70a3f6b；已逐文件核对 Go、SQL、go.mod/go.sum、sqlc.yaml 和 required-tests 与当前工作树一致。
- 精确 v0.3.0 tag 经 git archive 提取至容器临时目录，独立探针对任务专属库的新完整合成 v1 实际调用旧 migrations.Up，返回 nil。探针仅清理自己创建的 schema；输出保留 .test-output/consolidate-session/old-v030-startup-probe.log，佐证旧预检不能防止旧程序启动。
- check-generated.sh、check-migrations.sh 通过；openspec validate --all --strict：25 项通过、0 失败。本地文件链接、git diff --check 通过，冻结源、生成代码和 v0.3.0 发布说明均无差异，三个发布 tag 精确提交保持。

- 最终完整 scripts/verify.sh 已退出 0：独立 build/vet、迁移历史、完整库 race、691 项必需清单门禁及真实恢复成功。42 个测试包通过、2282 个测试通过事件、0 FAIL；8 个无测试包，唯一测试 SKIP 为普通套件预期的 TestRecoveryFixture。
- 恢复使用两个实际独立空库 recovery_source/recovery_target；seed、pg_dump、pg_restore、verify、source-unchanged 全部成功，PostgreSQL 客户端 17.11。检查 version=1/dirty=false、七表数据、已知/未知来源、INITIAL/LATEST、流程密文及引用、解密、access/refresh 和源库未改变，不靠清 Redis、改版本或延长票据期限通过。原始材料：.test-output/consolidate-session/linux-final/recovery.Ssa4Wc/。

- 最终 scripts/verify-accountsvc.sh 已退出 0，设置 ACCOUNTSVC_TEST_RACE=1：服务构建、范围 vet、服务/外围包 race 和全部服务必需清单通过；5 包 PASS、419 个测试通过事件、0 FAIL/0 SKIP，服务主包 144.582s。覆盖实际进程、SIGTERM、TLS、本地代理和会话上下文链路。
- 最终门禁原始日志集中 .test-output/consolidate-session/linux-final/：consolidate-verify.log、tests.jsonl、consolidate-accountsvc.log、accountsvc-tests.jsonl；源码逐文件对比再次通过，快照清单哈希保持 e8daad4b240bb9d41c252453e12eaa6cea0523cc357697ad0e59f37bc70a3f6b。
- 当前初始 up SHA-256：3b5fe06a68aa9761392fa527e64dd49c28e176bf3819c326eae1ea54e1f697de；down：2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b。历史 v0.3.0 哈希独立保留，没有改写已发布对象。
- 已核对并清理本任务三个专属容器及其匿名数据卷、源码/构建缓存卷和专属网络，原有 postgres/redis 容器保持；合成备份和原始日志保留在忽略目录。任务 7/7 完成，未提交、推送、合并、发布或归档。

## 最后复核的工具变化

此前相同的 openspec validate --all --strict 已两次 25/25 通过。收尾复核时本机 CLI 为 1.14.3，其 npm 包文件修改时间为本轮执行期间 2026-10-20 09:38:26；本轮没有安装、升级或更改 CLI。该工具现在将既有 requirement 段落超过 500 字符的 WARNING 视为严格失败：全量结果 19 通过、6 失败，涉及 accountsvc-runtime、embedded-auth-package、gateway-integration、metrics-adapters、session-authentication-context、session-client-information。上述主规格均未修改，git diff --exit-code -- openspec/specs/ 通过。

当前 CLI 下本次 change 的严格 JSON 校验 valid=true、issues=[]。因此本次实现/回归/独立审查完成，但不能声称当前工具的全量严格检查通过。没有关闭 strict、改配置、删除场景或扩展修改六份无关规格来消除警告；该全量工具兼容维护需单独处理。上文早期 25/25 是当时的实际结果，保留用于追溯。

## 2026-10-20 规格同步与后继对齐

用户随后明确要求按已提出的建议消除规格警告。本轮为纯文档维护，基线04b32a7、分支docs/sync-and-normalize-specs；按本变更在先、simplify-session-context在后的顺序同步主规格，没有归档。

同步后发现旧delta的兼容边界缺少后继新增的“前一整合基线拒绝”场景，导致全量校验报错，且单独重放旧delta可能回写旧存储定义。使用现有工件的后继对齐方式，将本变更versioned-auth-storage两条重叠delta重基到已实现的后继定义，并在proposal/design追加阶段说明。原b414fef提交、设计决定及上述实施证据保持可追溯，不将后继代码行为称为原始实现。

最终两个delta对重叠主规格逐条一致，重复同步内容等价；全量openspec validate --all --strict为26/26通过、0警告、0错误。上节CLI升级后的六份旧警告已由后继同步与职责拆分消除，原失败结果仅保留为当时历史。详细拆分和内容核验见[后继验收记录](../simplify-session-context/verification.md#2026-10-20-规格同步与警告优化)。本次未修改代码、SQL、生成结果、已发布tag或冻结源，也未提交、推送、合并或发布。

## 未验证项

产品真实旧库的数据转换、生产升级/回退、真实服务商与客户端联调不在本轮执行范围；不提供旧库自动迁移或保留凭据的部署保证。
