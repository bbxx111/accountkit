# 验证记录

## 2026-11-17：授权与起点

用户在讨论v1.2.0的管理员授权破坏性迁移及发布门禁后明确要求“发布”。本轮授权覆盖发布准备、必要分支集成/推送、正式tag与GitHub Release、远端Go消费和发布补记；不重复索取同一发布授权，不包含产品部署或自动归档本变更。

起点为develop的aa434ed4bed86f734b5b0959d5f8bd2e1ddf74dd，main仍为v1.1.0发布补记；工作区干净，任务分支chore/release-v1-2-0。已fetch全部历史/tag，远端无v1.2.0及v0 tag，无开放PR；最近正式版为v1.1.0。OpenSpec CLI1.14.3确认项目根正确，开始前无活动变更。

旧实现的Go/SDK/Android/iOS证据可用于确定变更范围，不能代替本次准备快照和最终main候选门禁。现有产品容器仅盘点，本次验证另建独立资源；原始材料统一保存在被忽略的.test-output/release-v1.2.0/。

## 候选文档阶段

已固定v1.1.0至aa434ed的差异：管理角色显式绑定为破坏性部署迁移，Go权限接口为可选加法扩展，字符串重构保持协议/存储原值；生产SQL、go.sum和SDK版本/锁文件未变，pgerrcode仅同版本从间接移至直接依赖。候选说明包含11路由/10权限、完整环境示例、空绑定403、scope边界、全部副本重启/排空及成套回退。

- 当前README、集成/协议/兼容/服务与发布指南统一为v1.2.0（候选）；三份OpenAPI仅修改info.version/description，路径、schema/DTO及扩展保持。两SDK仍1.0.0，兼容矩阵新增两条pending行，不提前宣称已验证。
- 精确Node24.21.0：OpenAPI 24/11操作、SDK 36操作覆盖、194个本地链接/UTF-8/JSON边界检查通过；SDK release metadata-only两包通过，仅验证版本/矩阵结构。Windows工具33项中32通过，1项POSIX专属skip，须由Linux补验。
- sqlc1.31.1的check-generated.sh通过；当前OpenSpec46项严格校验通过。独立候选文档审查Spec/Quality通过，无Critical/Important/Minor finding，报告在本计划SDD目录task-1-review.md。
- 本计划SDD进度/分工记录位于`.superpowers/sdd/2026-11-17-release-v1-2-0/`；所有原始结果在本变更忽略目录，不写第二份长期验收报告。

本阶段只完成候选资料与审查，准备快照的真实数据库/race/恢复、新SDK平台门禁、最终main候选、tag及正式消费均仍待执行。已创建本任务专用网络、PG17/Redis和Linux验证容器，完整ID保存在resources.json；容器Go1.26.5、pg_dump17.11，恢复源/目标为两个不同的新空库。

## 准备快照实际门禁

准备源提交为`41b9078857f3be2376fe8d8ea6f5564c8f554b03`。源码和完整Git历史经source.tar复制到任务容器本地文件系统，未直接在Windows挂载目录运行整套Go测试；tested-go-manifest.json记录635个Go文件，测试后与当前工作树全部匹配。

- `bash scripts/verify.sh`实际通过：迁移历史、build/vet、全仓race、必需测试登记以及真实pg_dump/pg_restore恢复。顶层1300项，1297通过、3个约定/条件skip。恢复seed/verify/source-unchanged三阶段成功，备份和合成数据证据在linux-evidence/recovery.0J9Mzn。
- `bash scripts/verify-accountsvc.sh`独立通过，275个顶层测试pass、两项条件skip；真实服务子进程使用race构建。带integration标签的Go SDK fixture11项全通过、无skip。原始JSON与日志保存在linux-evidence/，go-summary.json解析结果无fail。
- 普通RecoveryFixture按约定skip，已由真实恢复三阶段覆盖；Compose场景因镜像没有Docker CLI skip，宿主16子例补验通过（compose-host.log）；不可读资料文件因容器root可读skip，UID65534运行同一测试二进制补验通过（unreadable-nonroot.log），未把skip当作通过。
- 精确Node24.21.0/pnpm11.24.0：TS冻结安装、typecheck/format和56项单元通过；精确Flutter3.47.4/Dart3.13.3：冻结安装、format/analyze和77项单元通过。单独任务SDK数据库上的TS全部11管理操作、Flutter3项服务/IdP及1项策略演进通过，宿主仅停止脚本拥有进程（live-sdk.log）。两SDK固定本地Git子目录独立消费通过（sdk-local-consumers.log）。
- Linux Node33/33工具测试通过，补齐Windows的POSIX专属skip（node-linux.log）。生成检查sqlc1.31.1通过，SDK和Go验证独立报告；临时消费项目自己的依赖解析不改变仓库锁文件。
- 在线SDK准备运行[43914596694](https://github.com/bbxx111/accountkit/actions/runs/43914596694)的head_sha精确为41b9078，三个任务success：SDK单元/真实服务/策略/Git消费、Android、iOS。平台日志与toolchain JSON下载到sdk-preparation-android/ios；四项工具版本与要求一致，iOS明确使用Xcode26.1.1并生成无签名Runner.app，Android生成debug APK。
- 根据上述本次实际验证，将两SDK1.0.0对服务v1.2.0行从pending改为verified。这里只验证准备源，不代替后续最终main候选或正式tag远端消费。
- 测试结束后检查完整ID、名称、任务标签和网络成员，仅移除本任务三个容器/匿名卷与网络；恢复和测试材料保留，cleanup-result.json记录成功。产品容器未操作。

候选文档阶段提交为41b9078。准备证据补记仅改变文档/SDK兼容状态，运行源码及协议与受测准备源一致；最终main SHA尚未生成，本轮尚未创建正式tag/Release或宣称远端Go消费通过。

完整准备分支的独立收尾审查通过、无开放finding（SDD目录final-candidate-review.md），核对原始Go JSON、SDK平台产物及矩阵状态来源；公开Go兼容、角色迁移、最终SHA与不可变tag边界保持。两个发布后消费程序已先在当前源码上编译检查公共API用法，但尚未获取正式v1.2.0，不能算远端消费证据。

## 最终main候选与正式发布

准备材料通过[PR #34](https://github.com/bbxx111/accountkit/pull/34)以独立merge commit集成develop（8ac08ad），随后通过[PR #35](https://github.com/bbxx111/accountkit/pull/35)固定最终main为`462d798dfc11e672cd2ee8d7ce7fbce402d29d02`。合并树与受审查候选一致，冻结后未修改候选说明。最终发布门禁为该精确SHA的[Go CI 43922266678](https://github.com/bbxx111/accountkit/actions/runs/43922266678)与[SDK CI 43922266660](https://github.com/bbxx111/accountkit/actions/runs/43922266660)，两项整体及Go单任务/SDK三个任务全部success，包含实际Android/iOS构建；旧准备源和中间PR不替代最终SHA。

中间develop SDK运行43918890092的Android首次在Flutter下载Dart SDK启动阶段失败，toolchain输出为空，尚未进入应用构建/测试。main合并保护实际拦截了该失败；保存原始任务日志后，只重跑一次相同源码和工具版本，attempt2所有任务成功，没有改源码或放宽校验。旧PR #34的另一SDK运行中，单元与Android成功、iOS取消，未将取消算作通过。最终main的两项完整成功独立于上述中间结果。

发布保护先核对本地/远端main与最终SHA、干净工作区、实际工作流身份/结论、SDK矩阵、候选说明和远端tag条件，check-only通过后才执行创建。annotated `v1.2.0`对象为`f8e624dda3e50c0e05c62e039447dc5e922b153b`，精确解引用到462d798；[GitHub Release](https://github.com/bbxx111/accountkit/releases/tag/v1.2.0)于2026-11-16 18:00:55 UTC发布（Asia/Shanghai为2026-11-17），非draft、非prerelease，正文与tag内候选发布说明一致。当前正式tag固定该受测提交，后续状态补记不移动tag。

## 正式远端Go消费

两个全新独立module经`GOPROXY=https://proxy.golang.org,direct`及`GOSUMDB=sum.golang.org`获取`github.com/bbxx111/accountkit@v1.2.0`，Go1.26.5 windows/amd64，GOWORK=off、无replace。分别验证根门面/旧RequireRole宿主与直接HTTP适配器/新权限扩展宿主；tidy/build/run均成功，运行时BuildInfo确认实际链接v1.2.0且无替换，go list版本与模块缓存来源正确。

- 权限目录及副本、新别名、既有角色常量、认证资源/验证码Dimension/SMTP TLS常量均可消费。下载模块的635个Go文件逐字节匹配最终受测Git对象，初始0001 up/down SQL匹配v1.0.0冻结哈希；消费者自行确认远端annotated tag仍指向462d798。
- 首次消费的额外源码指纹检查未通过：准备快照使用Windows工作树字节，8个未改动历史Go文件仍为CRLF，而下载内容为Git中的LF。诊断逐文件确认这8处下载内容与发布Git对象一致；未改tag或源文件，将预期指纹改为受测main Git对象后，在两个新目录重跑完整消费及635文件检查成功。首次命令和诊断保留，不将首次失败计为通过。
- 实际输出、模块版本/校验和、源文件指纹与SQL哈希位于remote-consumers/verification.json、published-git-go-manifest.json和各唯一run目录commands.json，完整来源见state.json/release-created.json/source-fingerprint-diagnosis.json。

正式版本和消费均已确认。独立发布补记已将最新文档更新为已发布，并按用户授权集成develop/main；这是独立补记提交，不能冒充受测/tagged源提交。发布准备变更仍等待以后单独归档，产品环境验收边界保持。

已发布状态文档的独立复核Spec/Quality通过、无finding（SDD目录task-5-review.md）；OpenAPI24/11、SDK36覆盖、194个本地链接/JSON/UTF-8及严格46/46均通过。补记中明确本地日期与UTC记录、tag内原候选文本不动、两个SDK仍为1.0.0。

## 发布补记与最终闭环

已发布补记提交75a7abb经[PR #36](https://github.com/bbxx111/accountkit/pull/36)合入develop（f0a3082），再经[PR #37](https://github.com/bbxx111/accountkit/pull/37)以独立merge commit合入main（70e047e）。两分支补记树相同，GitHub Release正文与已发布说明一致；正式tag对象和源码目标始终未变，没有将补记SHA称为发布源。状态维护按文档范围验证，受测运行源码与tag一致。

最终核验通过：远端最新稳定Release为v1.2.0、非draft/prerelease，tag对象f8e624d仍解引用受测462d798；精确main的Go/SDK门禁全成功；两独立正式Go宿主与635源码/冻结SQL匹配；SDK矩阵保持1.0.0对v1.2.0 verified。所有本任务测试容器及网络已不存在，原有产品容器未操作。原始最终核验为final-published-check.log，阶段对象与PR完整SHA保存在state.json。

本轮13项发布任务均已完成，最后闭环提交仅记录已发生的事实并同步任务/计划状态；按普通merge commit集成两分支，正式tag不移动。发布准备变更保持完成状态，归档需后续单独指令。真实提供方、网关及产品部署不因本次库发布而计为已验收。

最后闭环记录经独立复核通过、无finding（SDD目录final-release-review.md）；任务/计划与实际发布、消费、补记集成及清理状态一致，未将后续记录提交称为受测或tagged源码。
