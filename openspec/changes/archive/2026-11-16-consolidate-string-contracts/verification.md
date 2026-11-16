# 验证记录

## 2026-11-16：创建与ff准备

分支：`refactor/consolidate-string-contracts`，基于本地develop的`a11ea46`。用户已确认全仓范围，并明确授权创建变更及ff。原`configure-admin-permissions`未归档，不由本次改写或归档。

本轮仅准备[提案](proposal.md)、[设计](design.md)、[任务](tasks.md)及[78组候选清单](inventory.json)。`.openspec.yaml`设置`skip_specs: true`；本次保持既有行为，因此不新增或修改行为规格，CLI确认specs为skipped，不创建specs目录。

候选清单保留原值、位置、语义拥有者和任务映射，省去原临时报告的词法匹配上下文。盘点源基线为`7f43faa`，与a11ea46源码相同，仅合并验收文档不同；实施不依赖.test-output中的原报告。所有候选均为planned，实施任务保持未勾选。

### 已执行

- OpenSpec CLI为1.14.3；已读取当前项目root、创建/状态和各工件指令，proposal/design/tasks依赖齐备，specs显式跳过。
- `openspec validate --all --strict`：47项通过、0失败（45份主规格、原管理权限变更及本变更）。skip_specs的INFO为明确的纯重构声明，不是验证缺失豁免。

### 清单与工件核验

- 78个候选ID唯一，全部为planned；每项primaryTask均对应真实任务，共21个主要任务映射。30项实施任务均未勾选，apply状态为ready、0/30，不把工件齐备视为实现完成。
- 367处源文件行号引用有效，10个本地文档链接可解析；6个工件/支撑文件均位于本变更目录，无符号链接或路径越界，未创建与skip_specs冲突的specs目录。
- JSON解析、Markdown末尾换行/行尾空白及Git差异检查通过；盘点基线与当前起点的Git差异仅为旧变更验收记录，源码相同。
- 独立builder-check审查结论为spec PASS、quality PASS、`nothing important left`；已核对全仓覆盖、跨包依赖、78组任务映射、SDK独立消费、存储字节及平台门禁边界，未发现需补充决定的实质问题。原始审查报告保留于`.test-output/string-contracts-ff/review.md`。
- CLI最终状态为proposal/design/tasks完成，specs显式skipped；全量严格校验47/47通过。按AGENTS.md在本轮工件检查与审查通过后做一次本地阶段提交；未开始实施、未推送、未合并或归档。

### 实施后的证据边界

本轮未修改Go/Dart/TS、脚本、部署或主规格，未执行构建、运行测试、数据库/race/恢复、SDK真实服务/平台/远端消费检查。前一变更的日志和本轮规格校验不等于本次重构通过运行门禁。

apply后需按任务记录每阶段实际命令、独立审查、提交和清单闭环；精确SDK工具、macOS/Xcode等必需依赖缺失时相关任务保持未完成，不把skip或近似版本当作通过。临时日志和原始材料放.test-output，长期证据只维护本记录。

## 2026-11-16：实施基线与详细计划

使用已创建任务分支和当前工作区，沿用此前工作树创建受沙箱限制后的原地执行方式；不重复创建工作树。详细执行计划为[全仓实施计划](../../../../docs/superpowers/plans/2026-11-16-consolidate-string-contracts.md)，执行依赖及写入范围明确；无共享写入的Go HTTP、Flutter、TS/工具按项目规则并行，Go领域及配置接入顺序实施。

- 起点`a94a070`，78组候选全部planned；30项任务映射及原值清单已核对。已有协议、审计、存储和拒绝边界断言作为行为基线，不为保持行为的纯提取编造先红；新增帮助函数先写测试观察缺失失败。
- Go1.26.5 windows/amd64、GOWORK=off，`go test -count=1 ./...`基线通过，日志`.test-output/string-contracts/baseline-go.log`；未配置数据库，skip不作为真实集成证据。
- 固定临时Git快照`2c0a898b9981ade4c6d43bd47fe53c892e8b5680`，本地代理版本`v1.1.1-0.20261115185502-2c0a898b9981`；旧角色宿主和新权限宿主两个独立module在GOWORK=off、无replace下tidy/build/run通过，原始记录`baseline-consumers/verification.json`。只用于兼容基线，不创建源分支tag或发布。
- 当前Flutter3.47.9/Dart3.13.7不等于要求3.47.4/3.13.3，精确工具另行准备；本机无macOS/Xcode，不把其他平台或近似版本当作原生门禁通过。现有产品容器均不操作，真实验证只创建本任务资源。

## 阶段：TS管理SDK与JS工具定义

- 完成L064–L066/L070–L072：SDK内部资源名/HTTP与安全消息同源，filter tuple派生原类型和Set；工具拥有SDK descriptor、冻结HTTP方法集合及创建/清理同源前缀。未扩大SDK入口或加入父目录运行依赖，未知SDK与覆盖拒绝规则保持。
- L066的create_time及已单份query tuple保留局部表达；L078的shell阶段/env目前保持原显式调用，Go恢复驱动与跨语言闭环交后续阶段，不提前记该组完成。
- 精确Node24.21.0/pnpm11.24.0：TS typecheck/format/test通过（56项）；node工具测试31通过、1项POSIX专属在Windows跳过；OpenAPI 24/11及SDK覆盖36操作通过。原始日志task3-*.log与实施报告位于.test-output/string-contracts。
- 独立审查task-3-review.md：spec/quality均通过，无实质finding。真实服务/Git完整消费、Linux取消补验和全仓门禁尚未执行；7.1已完成，7.2及9.1/9.3等综合要求保持未勾选。

## 阶段：Go HTTP/JOSE内部契约

- 新增internal/httpcontract、internal/jose及httpapi/internal/resourcename；Header/MIME、OAuth与同义reason、challenge、缓存策略、资源名和RequestID校验集中。保留authn.Reason*与tokens公开常量兼容，JWK/JWT算法复用现有定义，默认/rfc9068和三类身份验证不合并。
- 消费者/管理/三个服务HTTP面的reason按拥有者定义；status/reason/审计reason分别维护。filter/browser只共用字段名、kind映射和品牌/rank描述数据，输入解析和预算不改。
- 新helper测试先因缺少符号失败，再实现并通过；分区go test -count=1及最终重点回归通过。独立审查规格/质量通过、无实质finding；审查指出缺少vet空日志，root已重跑完整分区vet并明确保存task-1-vet.log，exit0。
- 根accountkit/config、accountsvc顶层和user/idp引用留后续阶段接入；L009/L011/L012/L036/L038/L046仅记录阶段进展，不提前关闭整个组，2.2–2.4仍待跨模块闭环。2.1与5.1已完成。

## 阶段：Flutter内部协议

- L057–L063完成：按认证/HTTP/wire/路由/Dio metadata及校验规则建立内部定义，实际匿名请求与排除集合共用7条路由；未知kind仍可扩展，token存储字节和epoch/revision/刷新重放逻辑保持。资源helper先以缺失方法测试失败，再实现并回归。
- 独立审查确认代码规格保持，唯一README中文被写成问号的问题已通过apply_patch修复并复核关闭；spec/quality均通过。77项单元包含独立匿名路由、application extra隔离、父资源和历史TokenRecord字节断言。
- 补充工具Flutter3.47.9/Dart3.13.7的77项测试和analyze通过；随后在任务目录准备精确Flutter3.47.4 revision9584c6713b / Dart3.13.3，冻结pub安装、format、analyze及77项单元均通过，日志flutter-3.47.4-*.log。首次任务SDK克隆因Windows长路径失败，仅在该临时克隆启用core.longpaths并完成检出，未修改全局或现有D:/flutter。
- 未改SDK入口、版本、依赖或锁；6.1/6.2完成，6.3的真实服务、Git和原生平台门禁继续待验证，不以单元测试替代。

## 阶段：Go领域、审计、存储与指标

- L002/L013–L017/L019/L020/L022/L024/L025/L034/L040/L044/L045/L054–L056已完成实现收敛；审计原因、资源、Actor与业务action按原语义分开。新增跨包最小公开无类型常量为4个认证资源类型及4个验证码Dimension，既有字段/方法集保持。ForSchema、迁移TableName和metricdata标签单位位于内部边界。
- SQLSTATE保留08/53整个前缀，包括未知码；身份解析专有55P03/25P04/57014不扩到flow/session。动态USER原因对已知、0及未知正负状态保持旧输出；存储AAD/指纹/subject/Redis域仅命名，不改算法或原值。Lua/SQL/tag/模板raw string逐文件对照93bea6c无变动。
- 新ForSchema和USER原因helper均有缺符号先红、实现后绿记录；范围go test -count=1及vet通过。审查P2指出Schema五个重复关键字漏收敛，已补私有定义并通过profileattributes回归，定点复核关闭，当前spec/quality通过。
- 复用已锁定pgerrcode后，仅将相同版本从indirect移入直接require块；go.sum、其他版本及prometheus/client_model基线标记未改。只读tidy -diff另显示既有格式/元数据差异，未盲目应用；go build ./...通过。
- L021的google/facebook/oidc nonce/label/fingerprint、L023根任务名、L033根审计装配仍待后续接入。实际数据库、race、恢复及JSON存储往返待综合验证，3.2–3.5/5.2等依其完整要求保持未勾选；3.1已完成。

## 阶段：配置、第三方、发送器、示例与测试装配

- 根门面与accountsvc接入HTTP/JOSE、维护Task、AuditField与审计默认帮助函数；六个IdP默认endpoint由internal/idpdefaults共同拥有，保留根配置填充和验证时机。各提供方请求/响应、签名、nonce与诊断label分别拥有定义；SMTP最小增加三个无类型TLS模式常量，现有字段仍为string。配置域各自集中环境名/后缀，不扩展公开环境项。
- accountsvc空schema审计namespace保留旧`accountkit//resources`，未借默认函数变为accountkit/account/resources。readiness新增仅测试的policy_seed_marker元数据，Dart输出和shell最后提取仍使用原标记；无新配置或生产SDK依赖，设计D6记录了该接线细化。
- 新默认endpoint、namespace、readiness helper均有缺少符号先红和实现后绿记录。全仓Go单元、build、范围vet通过，原始task5-suite-final.log/task5-build.log/task5-vet-final.log；此时无数据库的skip仍不算集成结果。
- Task 5：fix round 1/5。审查P2指出QQ/微博AppID虽同正则也必须各自拥有，已恢复根与idp的独立规则；删除七个根库未消费的服务配置常量。核对发现L041六类管理审计拒绝分类尚未处理，已补私有定义并更正原实施报告。`go test -count=1 . ./user/idp ./httpapi/admin`与同范围vet通过，task5-fix1-test.log/task5-fix1-vet.log；独立定点复核全部ADDRESSED，spec/quality通过，无开放finding。
- 配置脚本曾误造成config.go重复片段，两次整文件覆盖被自动审批拒绝；已保存损伤与候选差异，仅用锚定patch修复本任务损伤，未用HEAD覆盖工作树。最终测试/构建及独立审查均在修复后执行，审查确认未残留重复或丢分支。

## 综合验证：SDK、工具与独立消费

- 工具链精确为Node24.21.0、pnpm11.24.0、Flutter3.47.4/Dart3.13.3；toolchain-actual.json记录实际输出。Windows原toolchain CLI的spawnSync pnpm无法解析.cmd；使用原validateToolchain函数对实际Node/pnpm.cjs/Flutter machine输出检查通过，未修改或放宽检查器，不宣称原CLI入口通过。
- `verify-sdk-consumers --sdk all --platform none`通过：两包均通过固定本地Git子目录独立安装、分析/构建与消费测试（sdk-consumers-none.log）。Flutter额外`--sdk flutter --platform android`通过并生成APK（sdk-consumers-android-same-drive.log）；SDK自身77项单元和TS56项单元的精确工具结果见前述阶段。
- Android首次尝试遇镜像下载截断，改用官方Flutter存储后下载完整；随后Gradle因D盘pub缓存与C盘临时项目的Kotlin增量路径冲突失败。仅为验证进程设置C盘任务专用PUB_CACHE后构建通过，未改SDK/锁文件/工具代码或伪造缓存状态。
- live-sdk.log：真实accountsvc的TS管理SDK全部11操作测试通过；Flutter真实服务与受控IdP共3项测试、嵌入策略演进NeedsBinding与资料合并1项通过。策略seed通过新readiness元数据产生旧标记；重启前后复用专用schema，结束只停止本次启动的fixture进程。
- Node工具最终Windows33项中32通过、1项POSIX专属skip（final-node-windows.log）；Linux33/33通过（final-node-linux-path.log），含忽略SIGTERM的取消、临时Git/路径安全、seed元数据拒绝与最终旧标记。首次Linux运行因未把已安装Node加入子进程PATH失败，补正确进程PATH后全通过，未修改测试来放行。
- `node scripts/check-openapi.cjs`、check-sdk-coverage.mjs通过：24/11操作及36项覆盖保持；两包check-sdk-release以各自v1.0.0候选tag和--metadata-only通过，仅检查元数据，未创建tag或宣称发布就绪。此前误用不存在的check-openapi.mjs失败，已改用仓库实际.cjs入口重跑。
- 最终Go独立消费快照`838a35a969d5ad1bcd9959d2daac26844c6bff25`，本地proxy版本`v1.1.1-0.20261115200649-838a35a969d5`：旧角色宿主与新权限宿主均GOWORK=off、无replace地tidy/build/run通过（final-consumers/verification.json）。快照包含当时未提交实现，不依赖工作树或源分支tag。
- 本地验收阶段的主机为Windows，缺macOS/Xcode26.1.1，故当时未执行iOS原生构建，6.3/10.2/10.3保持未完成。该缺口现已通过下述GitHub Actions的macOS任务补齐，未以Android或单元结果替代。

## 综合验证：Go、数据库与恢复

- 任务专用PostgreSQL17/Redis，Linux验证容器使用本地文件系统源码副本及完整Git历史；未操作现有产品容器。`GOWORK=off CGO_ENABLED=1 bash scripts/verify.sh`通过：独立build/vet、全仓race、必需测试登记、迁移历史与真实pg_dump/pg_restore三阶段均通过，客户端为PostgreSQL17.11。原始记录在`.test-output/string-contracts/linux-evidence/`，full-linux.log和recovery.BMG2XC保留原始恢复材料。
- 全仓race运行1300个顶层测试，1297通过、3个skip：TestRecoveryFixture普通套件预期skip，恢复脚本随后强制完成seed/verify/source-unchanged；Compose用例因镜像缺Docker CLI跳过，已在Windows具备Docker CLI的宿主补验全部16子例通过（compose-windows.log）；不可读文件用例因容器root能读而跳过，后续以非root补验。没有把这些skip统计为通过；无测试的包级skip不代表缺失集成依赖。
- `bash scripts/verify-accountsvc.sh`通过真实进程、TLS、信号和数据库/race门禁，277个顶层测试中275通过，另外两项为同一Compose与root不可读文件条件skip。带integration标签的Go fixture11个顶层测试全部通过，无skip（sdk-fixture-linux.jsonl）。12个新增必需顶层测试已纳入主登记，其中服务新增测试也纳入对应服务登记，未删旧条目。
- 全分支独立审查覆盖78组，唯一P2为L009的metrics与外层reveal缓存两处遗漏。Final fix round 1/5（Task5累计修复轮2）：已复用SetNoStore，保留原条件、时机且不增加Pragma；范围测试/vet通过，定点复核ADDRESSED，当前spec/quality通过、无开放源码finding。原始报告final-review.md及task5-fix2-*.log。
- 全仓验证后的源码只差最后两处等价接线（metrics.go/run.go），其余633个Go文件一致；已覆盖最终两文件，重新通过全仓build/vet及完整accountsvc真实进程/race门禁，日志final-linux-evidence/accountsvc-final-linux.log和accountsvc-tests.jsonl。最终服务顶层275通过、Compose和root权限两项条件skip；前者宿主补验如上，后者通过UID65534执行TestProfileAttributesUnreadableFile补验（profile-unreadable-nonroot.log），无剩余Go条件skip缺口。恢复与其他领域源码无变化，不重建已使用的恢复源/目标库或重复清空数据。
- final-source-match.log确认最终验证容器与提交源码的635个Go文件SHA256全部一致。测试结束前核对完整容器ID、名称、任务标签与网络成员，仅移除本任务三个容器/匿名卷及专用网络（cleanup-result.json）；原始测试、恢复材料及精确工具保留在忽略目录用于复查。
- sqlc1.31.1的check-generated.sh通过（generated-check.log），冻结生产SQL及哈希未变；OpenSpec严格校验47/47通过。清单78项均有实际符号/文件/保留说明，原始78组值及367处基线引用未改；JSON、任务映射、文档链接、gofmt/LF/diff检查通过。Go阶段结束时尚缺的iOS环境由后续远端任务补齐。

## 2026-11-16：远端SDK与iOS验收

用户授权继续远端验收后，推送任务分支并通过既有workflow_dispatch运行client-sdks.yml。验收提交固定为`8d5cc551f7f74dab7b299ce86d9540c3958670c9`，运行[43793597139](https://github.com/bbxx111/accountkit/actions/runs/43793597139)，2026-11-16 02:40:30 UTC触发，02:45:01 UTC结束，整体结论success。

| 任务 | 结果与证据 |
|---|---|
| SDK unit, real service, embedded policy and Git consumption | success；TS56项单元、Flutter77项单元、TS全部11管理操作、Flutter3项服务/IdP与1项策略演进、36项协议覆盖、两个SDK固定Git子目录独立消费全部通过。 |
| Flutter Git consumer / ios | success；macos-15 runner实际为macOS15.8.2 arm64，显式选择Xcode26.1.1；Flutter3.47.4/Dart3.13.3，完成独立消费项目分析、测试和无签名iOS release构建，生成Runner.app。 |
| Flutter Git consumer / android | success；同样通过精确工具链检查及独立Git消费，生成debug APK。 |

- 三个任务的Node24.21.0/pnpm11.24.0、Flutter3.47.4/Dart3.13.3检查均通过。iOS日志明确记录Xcode26.1.1及`SDK isolated consumers passed (flutter, native platform=ios)`；没有缺依赖而跳过的必需平台门禁。
- 本轮下载sdk-contracts、sdk-native-ios、sdk-native-android三个产物；SDK契约与iOS完整任务日志、Android构建日志、run-final.json、artifact ID/digest元数据和13个下载文件的SHA256清单保存在`.test-output/string-contracts/remote-ci-43793597139/`。本地核验运行SHA、三个任务success、工具版本与构建/协议结果全部通过。
- 这些结果证明本次源码的固定Git子目录消费和原生编译；iOS采用--no-codesign，没有执行真机签名部署。SDK正式远端tag消费仍是独立发布验证，本次未创建版本或tag。
- 本轮回填仅修改OpenSpec工件及详细计划，运行源码与上述CI通过提交一致；Go门禁沿用本变更已完成的实际数据库/race/恢复记录，不把SDK workflow当成Go门禁替代证据。

## 本地阶段提交

已提交`5000620`详细计划、`932ed5c`管理SDK与工具、`93bea6c`HTTP/JOSE、`a26a363`Flutter、`abfcfdd`领域/审计/存储、`919a23d`配置/提供方/测试装配与跨模块接线、`8d5cc55`本地验收记录、`2a1b8e0`远端iOS补验与30/30任务闭环。任务分支已推送并完成上述CI；归档工件及链接修复另作阶段提交。

## 2026-11-16：归档与集成

用户明确要求合并、归档。OpenSpec CLI1.14.3确认30/30任务完成、proposal/design/tasks齐备、specs因skip_specs显式跳过；归档前严格校验47/47通过。CLI archive将六份工件移动至`openspec/changes/archive/2026-11-16-consolidate-string-contracts/`，specsUpdated=false，没有同步或修改主规格，也未归档原configure-admin-permissions变更。

归档后修复inventory中的484处证据相对路径、验收记录中的计划链接，以及计划对设计文件的引用；原始值、367处基线引用及78组处理结果保持。运行源码与已通过CI的8d5cc55一致，归档仅改变工件位置和记录/引用。原始日志继续保留于仓库根目录的.test-output/string-contracts。

归档内容检查通过：六份工件、78组清单不变、30项任务全勾选、12个本地Markdown链接及全部证据路径有效，635个Go文件SHA256与最终验证副本一致。归档后`openspec validate --all --strict`为46/46通过（45份主规格与剩余管理权限变更）；活动变更列表已移除本变更。归档前后的原始输出保留于archive-content-check.log、pre-archive-validate.log及post-archive-validate.log。

集成目标为develop，按仓库默认PR流程保留独立merge commit。远端develop尚未包含本地develop的三个前置提交（4721bf7、7f43faa、a11ea46），本任务分支已继承该基线，集成时保留这些已完成历史；PR候选检查覆盖完整差异，合并记录及在线检查以对应PR为准。

## 当前交付状态

78组均已实施并核对原值/拥有者及保留理由，任务30/30完成。Go、两个SDK、Android/iOS及工具的规定门禁均有本次通过证据，iOS环境缺口已由远端CI补齐；独立收尾复核Spec/Quality通过、无开放源码或文档finding（final-review.md）。远端验收回填提交2a1b8e0只更新四份工件/计划文档，运行源码与CI通过提交8d5cc55一致。变更现已归档，清单、链接、格式及源码一致性检查随归档复核；集成目标为develop。本次没有修改版本、锁文件、冻结迁移或原管理权限变更。
