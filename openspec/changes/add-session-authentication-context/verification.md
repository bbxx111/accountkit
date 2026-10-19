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
