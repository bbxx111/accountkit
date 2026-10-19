# 验证记录

日期：2026-10-19。阶段：提案规划。基线：develop / 15309bf；工作分支：docs/session-authentication-context。

## 已确认范围

- 当前会话采用方案二：服务端详情接口返回具体认证身份及类型，不向 JWT 添加来源字段。
- 普通登录记录单身份，pending 成功记录参与认证的多个身份；重新认证成功替换来源，refresh 和普通绑定不改变来源。
- 身份解绑撤销命中来源及来源未知的同账号会话；用户已明确确认未知旧会话同时撤销，升级本身不强制退出。
- 客户端描述可选，登录、重新认证、新一轮刷新采集；普通业务鉴权不增加数据库热路径。

## 本轮实际检查

| 检查 | 结果 |
|---|---|
| OpenSpec CLI 版本及变更清单 | 已安装1.11.0；本地根为本仓库，创建前仅有prepare-v0-3-0-release |
| openspec status --change add-session-authentication-context --json | proposal/specs/design/tasks均done，isPlanningComplete=true；仅表示规划齐备 |
| openspec validate --all --strict | 24项通过，0失败，包含本次变更与全部主规格 |
| 新增工件静态检查 | 6个声明能力对应6份delta，32项实施任务均未勾选；正文编码、行尾空白及本地链接检查通过 |
| git diff --check / git diff --exit-code | 通过；已跟踪文件没有改动 |
| git status --short | 仅本change目录新增，运行时代码、主规格、已发布SQL与冻结源快照未修改 |

命令通过位于用户npm目录的已安装 openspec.cmd 执行；该目录在当前沙箱外，使用环境批准的执行权限，没有安装或替换CLI。PowerShell默认管道编码不适合中文生成脚本，生成时改用明确UTF-8；生成的delta随后由CLI及文件检查验证。

## 规划复核

本轮为顺序自查，未启动并行代理或声称已完成独立代码审查。已逐项核对协议、来源语义、历史兼容及数据生命周期：

1. 恢复迁移规格中被遗漏的历史场景；严格校验初次报告该遗漏，补回后全量通过。
2. 修正换绑后当前会话被撤销时的并发/重试描述：不再无条件要求404，已撤销会话按认证失效处理。
3. 明确直接Go解绑新增有效Principal前置条件，并标记行为/源码迁移影响。
4. 区分来源未知与客户端描述缺失，避免空数组被误读为解绑安全；确认已到刷新期限的会话仍属于显式撤销集合。
5. 明确查询一致性、Redis fail-open、宽限/补全重放、来源替换锁序、匿名化回滚及旧pending可选字段兼容。
6. 明确缺失元数据与显式非法输入的不同处理，以及/token继续使用OAuth错误格式。
7. 新生产迁移必须递增；应用回退和混跑不能绕过新身份撤销语义，旧撤回v2仍须拒绝。

## 未实施和未验证

本轮没有修改Go/SQL或执行数据库迁移，没有运行build、vet、Go测试、race、sqlc、迁移历史门禁或恢复演练；不能据此声称新功能、数据库并发或发布验证通过。任务清单中的这些验证将在实施时产生当前代码证据。

默认UA解析库的具体选型留给任务1.2，在既定输入/输出契约内完成许可、维护性、体积和样本评估；尚未增加依赖。真实浏览器Client Hints、原生SDK、产品网关/CORS、微信/Apple提供方和生产升级窗口未验收。

未提交、推送、归档或发布。后续审阅入口：[提案](proposal.md)、[设计](design.md)、[实施任务](tasks.md)。

## 实施阶段：公共类型与解析（Task 1）

用户后续明确授权commit、merge back、apply。提案提交ae71fdf以--no-ff合入develop为8190167；实施位于原生隔离工作区C:/Users/bbxx1/.codex/worktrees/80fd/accountkit，分支feat/session-authentication-context，计划检查点cd8d14b。以下为新增实施证据，前文“未实施”仅描述提案阶段。

- Task1检查点：3639424；新增公共enum、ClientInfo校验/合并、CurrentSessionInfo及本地HTTP解析器，尚未接入处理器或存储。
- 选型：mileusna/useragent v1.3.5（MIT）及dunglas/httpsfv v1.1.2（BSD-3-Clause），均无传递依赖；比较mssola/useragent v1.0.0后，前者的系统版本、设备及类别结构更适合此契约。Windows Chrome、iOS Safari、Android Chrome、原生头、GREASE/畸形/超限/重复头和BFF样本已测试，自动识别为尽力描述。
- RED：新增Normalize/ClientInfo、Parse未定义时各运行目标测试并确认失败。GREEN：go test ./enum ./user ./httpapi/clientinfo -count=1和对应go vet通过。
- 完整测试：GOWORK=off、一次性PG17/Redis，go test -json -count=1 ./...退出0；42包通过、0失败、8无测试包；19测试项skip（18项Windows不执行的Linux服务测试、正常跳过的TestRecoveryFixture）。原始日志.test-output/session-context/task-1-suite.json；不能据此声称Linux/race/恢复通过。
- 独立审查：review_client_model对cd8d14b..3639424判定规格符合、代码质量Approved，无Critical/Important/Minor。后续接线须以MergeClientInfo(nil, observation)初始化快照，孤立版本不得写观察时间。
- 总量2048字节按规范化文本计；单字段rune上限使合法数据最大1408字节，整体保护保留，超大输入测试不声称独立覆盖不可达总量分支。

## 实施阶段：迁移与sqlc（Task 2）

- 检查点c958ca9：新增0002、来源关联/客户端快照、版本结构预检、10个按账号限定查询及sqlc结果；旧会话默认来源未知，冻结0001/source-baseline未改。测试下一版本推进到0003，七项新增必需测试已登记。
- RED：先运行新升级/查询用例，确认新列/查询缺失；加入0002后历史v2_new_name曾被错误接受，增强结构识别后拒绝回归通过。
- 首次全仓回归发现跨schema目录反解析与其他测试删除schema的竞态（XX000 OID消失）；通过先MATERIALIZED目标目录再反解析修复。新增并发其他schema DDL回归与原并发迁移/单连接检查通过。独立新用例未稳定重现旧故障，真实RED证据为首次全套失败，不夸大确定性。
- 修复后完整go test -count=1 ./...退出0（根36.477s、migrations29.139s、user69.189s），go build/go vet、sqlc一致性、check-migrations及diff检查通过；Windows仍不执行Linux专用服务测试，恢复演练尚未执行。
- 10,000条合成来源关联的EXPLAIN ANALYZE命中反向identity_id/session_id索引；当前会话完整读取执行计划留待全局门禁。
- 独立review_storage对bac0040..c958ca9判定规格符合、质量Approved，无发现；运行时必须继续落实用户→会话锁序、上下文同事务及清理接线。
- 另外，在已审查的Task1快照3639424上运行Linux go test -race -json -count=1 ./...通过42包，0失败，仅TestRecoveryFixture按普通套件预期跳过；库和服务必需清单检查均通过。日志在.test-output/linux-source/.test-output/linux-baseline.jsonl。这是平台基线，不替代最终实现的Linux/race/恢复验证。

## 实施阶段：来源事务和当前会话领域查询（Task 3）

- 检查点144b96d及修复868c95e：普通登录精确身份、pending全部证明、重新认证同事务替换来源和auth_time、FOR SHARE当前会话一致读。未知false/[]、已知空集与非法持久化kind、归属/期限/撤销和依赖错误均有测试。客户端快照接线仍属Task5。
- 新增真实PG引用、回滚与锁屏障测试，pg_blocking_pids确认共享锁等待；来源写入失败不会部分创建账号/身份/会话或完成flow。原refresh和sid保持。
- go test ./...、build、vet通过；一次全仓误设SERVER_TEST_REDIS_URL，随后正确设置ACCOUNTSVC_TEST_REDIS_URL补跑./user/code ./internal/accountsvc及根TestCodeKeyRotationIntegration。实际Redis轮换通过；Windows服务与正常RecoveryFixture仍按原规则skip。原始日志.test-output/session-context-task3/。
- 独立review_sources发现连接中途EOF未归503；精确direct/wrapped EOF四项RED后修复，42703/42P01编程错误保持内部。首次/重试的pending回滚直接断言无结果、flow=VERIFIED、user_id/session_id=NULL。修复定向测试3.078s、vet/diff通过，复审两项均ADDRESSED、质量Approved。
- 144b96d固定快照的来源/重新认证/当前会话定向Linux race通过；EOF修复后的完整Linux/race仍由最终门禁覆盖。迁移快照c958ca9的Linux迁移及accountsvc四个包race通过，无测试skip。
- 计划补齐Task4的HTTP错误映射：领域锁后新鲜度不足应沿用400，scope不足沿用403，不能落入通用500；该接线维持已批准协议。

## 实施阶段：身份移除联动撤销（Task 4）

- 检查点34b5b2a：解绑的直接Go与HTTP前置条件一致，来源命中/未知及已过期未清理会话同事务撤销；换绑当前会话仅在已知且不依赖旧身份时保留，其他会话仍撤销。新增撤销原因9/IDENTITY_UNBOUND；HTTP领域错误保持400/403，成功204/200保持。
- RED覆盖缺少直接Go校验、会话未撤销和通用错误500；实现后定向通过。真实PostgreSQL触发器/advisory锁屏障并用pg_blocking_pids确认顺序，覆盖reauth、登录、pending、刷新、换绑、退出、冻结/注销；保留既有22个换绑并发组合。最后锚点测试仍严格要求一个成功一个ErrLastAnchor。
- 完整go test -json -count=1 ./...通过42包、2163项含子测试PASS、0失败，build/vet/diff通过；13个新增必需回归已登记并逐项核对PASS。日志.test-output/task-4/。
- 独立review_revocation对d1bc58e..34b5b2a判定规格及质量Approved，无Critical/Important/Minor。专项核对用户→会话锁序、完成重放和提交后吊销。
- 34b5b2a固定Linux快照的根/领域/HTTP race已通过，真实TestServiceIntegrationIdentityReplacement PASS（14.99s）；完整服务包结束结果随后记录。RecoveryFixture仍留独立恢复阶段。
- 现有指标没有会话撤销业务reason API，保持有界依赖/Authenticate分类，验证新reason通用审计roundtrip，无额外指标接口。

## 实施阶段：客户端快照与生命周期（Task 5）

- 检查点c9ec742：仅五条认证观察路径校验ClientInfo；普通INITIAL/LATEST、pending加密首次描述与旧payload兼容、reauth/新刷新LATEST、重放不更新、同事务当前详情（含未知来源）、列表批量读取，以及有界清理/匿名化接线。
- RED确认缺少快照、非法输入被接受、清理来源孤儿；11项新增真实PG回归通过并加入必需清单。额外mutation RED把上下文删除移到会话锁前，确实捕获refresh重新留下快照；恢复顺序后GREEN。
- 全仓go test -json -count=1 ./...通过42包，0失败（根39.221s、migrations32.265s、user86.176s）；build/vet/sqlc一致性/diff通过。Windows仍跳过18项Linux服务测试及普通RecoveryFixture。原始日志.test-output/session-context/task-5-suite.json；首次误用WSL bash缺sqlc退出127，改指定Git Bash后检查通过。
- 独立review_snapshots判定规格符合、质量Approved。Minor锁前置条件注释已在c45b065补齐并重新生成，差异仅注释；未为注释重跑全套。当前快照Linux user race通过，日志.test-output/linux-source/.test-output/linux-snapshots.jsonl。
- 审查指出全套WARN，已比对3639424的linux-baseline.jsonl确认同一TestMigrateStartCloseAgainstRealDB的audit dropped、TestBeforeDeleteSharedTransaction/admin=true/success的audit validation rejected已存在；其他WARN属于维护取消/故障、IdP失败/重定向测试。测试通过不等于日志无警告，没有以关闭日志或删测试掩盖问题。
- 交接Task6：只在SignInWithCode/Idp、CompleteSignIn、Reauthenticate、Refresh五条路径解析元数据；binding/send/revoke不扩展。宿主回调错误不得把原始描述写日志；当前/两面列表必须独立DTO并保留原协议错误格式。
