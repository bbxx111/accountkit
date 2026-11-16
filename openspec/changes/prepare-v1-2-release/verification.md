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
