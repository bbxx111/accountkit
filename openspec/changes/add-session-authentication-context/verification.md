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

## 实施阶段：HTTP与宿主接线（Task 6）

- 检查点bf86d52：可选CurrentSessionService、当前会话GET与专用DTO、根/HTTP Deps.ClientInfo、五条认证路径的有界解析、安全callback错误处理。既有Service必需方法集不变，消费者/管理员各自明确DTO。
- 实现前新GET为405、元数据丢失/非法描述仍消费、代理新路径404均实际RED；接入后GREEN。隐私及两个DTO移除变体证明测试能捕获回归，已恢复。
- Windows完整go test -json -count=1 ./...退出0，42个测试包通过、8个无测试包、0失败；scoped vet和diff通过。新增10个顶层测试登记必需清单；9个实际通过，新增真实服务用例Windows跳过，Linux由控制器单独执行，不能用Windows结果代替。
- 真实PG根HTTP流程覆盖登录、重新认证更新来源、解绑当前来源后401；查询计数证明消费者列表固定2次SQL、管理员3次，普通Authenticate及内省0 SQL且关闭PG池仍可用，当前会话GET池关闭503。
- 独立review_http_context对2c8e7fb..bf86d52判定Spec compliant、Quality Approved，无发现。Linux固定提交验证结果另行记录。

## 查询执行计划核验（2026-10-20）

- PostgreSQL17，本任务一次性库内建立事务隔离schema；1000账号、2000身份、10000会话（每账号10条、10%来源未知）、18000来源关联、20000快照。使用0001+0002原文建表，ANALYZE后执行实际查询的EXPLAIN (ANALYZE,BUFFERS)，最后ROLLBACK，不留下schema或数据。
- 当前行共享锁命中idx_session_user_id_id；来源查询命中关联主键和账号身份索引；INITIAL/LATEST命中session_client_info_pkey和账号会话索引。四条执行时间分别0.037/0.081/0.055/0.043ms，均无全表扫描，仅为本地合成样本观察，不是生产延迟承诺。
- 四会话批量快照通过单条SQL读取，内部索引loop=4不是HTTP/Go逐条查询；0.052ms。应用层固定读取次数由Task6真实PG计数测试另行证明。
- 反向身份查询命中idx_session_authentication_identity_identity_id_session_id，9行；真实撤销CTE使用该反向索引及账号会话部分索引，按id排序加锁，更新9条命中+1条未知会话，0.394ms。撤销写入随事务回滚。
- 原始SQL与输出：.test-output/session-context/final-explain.sql、final-explain.log；此快照SQL后续未改变时可复用该证据。

## 实施阶段：文档、OpenAPI与恢复夹具（Task 7）

- 检查点70ef246：更新README、兼容/Go/迁移/服务/网关/OpenAPI说明、可编译会话示例、两面OpenAPI及严格漂移检查；开发版本明确为未发布变更。直接Go解绑前置条件、keyed初始化和协调切换/回退限制均已说明。
- 恢复夹具增加known双身份来源、unknown历史会话、INITIAL App1.0/LATEST App2.0；完整快照纳入两个子表全部列，seed/verify/source-unchanged均核对语义。必需清单补充恢复/契约和Task1三个核心顶层测试。
- RED/GREEN：基线文档缺current路由/schema、原恢复快照遗漏来源关联均得到实际失败，恢复当前实现后通过。临时变体已恢复，无遗留修改。
- 范围验收：node契约检查22消费者+11管理操作通过；Swagger Parser两文档合法；136个相对链接/锚点0失败；示例编译及3个执行示例、全部8个HTTP包、新真实PG快照回归、user校验合并测试及范围vet通过。普通RecoveryFixture仍预期skip，不当作恢复成功。
- 原始日志：.test-output/session-context/task-7-{validation,red-proof,swagger,doc-links}.log。一次审核服务usage limit后恢复同权限操作成功，无权限绕过；不是未解决的安全拒绝。
- 独立review_docs_recovery对37ea446..70ef246判定Spec compliant、Quality Approved，无发现。完整Linux/race/恢复由控制器固定70ef246执行，结果另记。
- 补记Task6固定bf86d52的Linux/race：根、enduser、admin、accountsvc四包通过，新TestServiceIntegrationCurrentSessionContext实际PASS5.16s，唯一skip是普通RecoveryFixture；日志.test-output/linux-source/.test-output/linux-http-context.jsonl。

## 实施中的执行口径与影响

以下是控制器按已授权范围作出的执行判断，便于复核；没有改变用户确认的来源与撤销规则。

1. 用户已明确apply，按仓库默认计划/子代理流程继续，不重复索取确认。若此判断不符预期，影响是执行方式和耗时，代码仍在独立分支可审阅。
2. 允许本地实现检查点提交，但提案合回develop后，实施分支保留，不自动合并/推送。代价是实施完成后仍有单独的集成步骤。
3. 原生工作区位于默认可写根外，写入与构建通过获准的require_escalated执行，所有workdir明确。若授权不可用则该操作阻断，不以其他路径绕过。
4. 一个实现代理顺序写入，每步独立审查，控制器并行准备验证环境。代价是实现阶段耗时增加，避免共享写入冲突。
5. 2048字节总上限计算规范化文本，不计算JSON序列化开销；各字段rune上限使最大合法文本1408字节。代价是线上JSON包体可能超过2048字节，字段及输入保护仍独立生效。
6. 规范化保留仅版本观察；持久快照Merge丢弃无名称的孤立版本，首次同样Merge(nil, observation)。代价是缺名称的首次版本无法展示，避免编造名称或错误拼接。
7. 领域锁后重新认证/作用域失败沿用HTTP400/403，而不是原通用映射遗漏导致的500。影响局限于把明确业务错误恢复为既有协议分类。
8. 既有指标没有业务撤销原因接口，沿用有界依赖观测并验证通用审计IDENTITY_UNBOUND，不新增指标API。代价是没有新增专用按解绑原因计数器。
9. 新描述只在SignInWithCode、SignInWithIdp、CompleteSignIn、Reauthenticate、Refresh采集校验；绑定、换绑、发码、撤销等不扩大采集。代价是这些其他动作不会刷新客户端观察时间，符合本次快照范围。

## 最终验收（2026-10-20）

本节为当前实现的最终结果，取代提案阶段及各中间阶段的“未实施/待验证”状态；前文保留实际过程和失败修复证据。

- 最终受测/审查代码：70ef246c7bfcbb72a318051f0887d3aa24d9702c，完整历史独立副本.test-output/linux-source。随后提交仅更新本计划、tasks和本记录，不改变Go/SQL/HTTP契约。提案ae71fdf已经--no-ff合入develop为8190167；实施仍在feat/session-authentication-context，不推送、不合并该实施分支、不发布、不归档。
- 实际执行环境：Linux accountkit-verify:local、Go1.26.5/CGO、PostgreSQL17与Redis7任务专属容器；所有验证使用GOWORK=off。Windows已有范围与完整非race证据，最终并发及服务验收以本节Linux结果为准。
- scripts/verify.sh完整退出0：迁移历史保护、go build ./...、go vet ./...、go test -race -json -count=1 ./...、687项清单中的必需测试核验及真实恢复脚本通过。42个测试包PASS、2261个测试通过事件、0FAIL、8个无测试包。唯一普通测试SKIP为TestRecoveryFixture，按照既定契约另行三阶段强制执行。
- scripts/verify-accountsvc.sh完整退出0：真实accountsvc构建、范围vet、服务及外围包race、服务必需门禁全部通过；5包PASS、419个测试通过事件、0FAIL/0SKIP，主包130.742s。实际进程启用ACCOUNTSVC_TEST_RACE=1，包含SIGTERM、TLS、本地代理及本次新当前会话端到端流程。
- 真实备份恢复：先检查源recovery_source和目标recovery_target实际独立且为空，再seed、pg_dump自定义格式、pg_restore、verify、source-unchanged；三个阶段TestRecoveryFixture均强制PASS，客户端pg_dump/pg_restore17.11。核对迁移version/dirty、七个业务表、known双来源/unknown会话、INITIAL App1.0/LATEST App2.0、流程密文/引用/期限、身份解密、access/refresh及源库完整快照未改变；不以清Redis、修改版本或延长期限换取通过。
- 原始完整证据：.test-output/linux-source/.test-output/final-verify.log、tests.jsonl、final-accountsvc.log、accountsvc-tests.jsonl；恢复合成数据/备份/阶段日志保留在其recovery.KWrbQZ目录。这些是忽略的原始材料，未提交备份或测试数据。
- check-generated.sh在37ea446通过，之后未修改查询、迁移、sqlc配置或生成Go；最终verify.sh再次核验迁移历史通过。已发布0001、冻结source-baseline、正式tags和openspec/specs未改。Task7的22/11操作OpenAPI检查、Swagger合法性与136链接检查对应同一最终代码，未重复已通过检查。
- 最终独立final_branch_review对8190167..70ef246判定计划一致、Ready to merge: Yes，Critical/Important/Minor均无。审查覆盖跨模块来源、撤销、锁序、缓存重放、元数据生命周期、协议/热路径及迁移兼容；无待修复或搁置问题。审查报告及各任务报告原文保留在.test-output/session-context/reviews/，主要结论统一落在本记录。
- 对最终审查Declined to judge的裁决：真实浏览器/原生SDK及产品网关CORS、真实微信/Apple生产凭证联调、产品生产升级/回退切换均依赖未提供的宿主环境，本次未执行也不宣称验收；仓库内解析样本、HTTP/代理fixture、实际进程和独立恢复已按要求验收。这三项是外部部署边界，不是本变更省略的实现任务。
- 已精确核对并移除本任务创建的PG/Redis容器（含匿名数据卷）、专属Docker网络和两个构建缓存卷；没有清理其他容器/目录。原生工作区和实施分支保留，测试日志、备份及审查材料保留；SDD临时工作目录在证据转存并记录全部裁决后删除。

- 最终 openspec validate --all --strict：24项通过、0失败；git diff --check通过。32项验收任务及详细计划的40个步骤已依据上述实际证据完成，无遗留实施任务。
