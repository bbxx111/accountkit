# Tasks

依据[设计](design.md)及[详细计划](../../../docs/superpowers/plans/2026-11-17-release-v1-2-0.md)执行，证据统一写入[verification.md](verification.md)。

## 1. 发布说明与迁移

- [x] 1.1 固定v1.1.0至当前源码的API/HTTP/配置/存储差异，明确本次v1.2.0版本决定和管理员破坏性迁移；核对原值、冻结SQL及依赖版本并保存审查结果。
- [x] 1.2 编写v1.2.0候选发布说明与升级/回退步骤，更新兼容、服务和发布指南；通过内容、链接和独立审查，不将开发期未发布变量当正式升级项。

## 2. 候选版本状态

- [x] 2.1 更新README、Go/SDK/OpenAPI文档及三份OpenAPI元数据为候选，添加pending SDK兼容行，保持模块/路由/SDK版本和锁文件；运行OpenAPI、SDK覆盖/元数据检查。
- [x] 2.2 完成候选文档及源码一致性复核，OpenSpec严格校验、格式/引用/diff检查通过，按阶段本地提交。

## 3. 准备快照验证

- [x] 3.1 在任务专用Linux/PostgreSQL17/Redis运行生成/迁移历史、build/vet、全仓race/必需测试、真实恢复三阶段和accountsvc进程门禁；保存准备快照SHA与原始结果。
- [x] 3.2 对Compose与root权限等条件skip逐项补验，确认普通RecoveryFixture的约定skip由真实恢复覆盖；只按完整ID及标签清理任务资源。
- [x] 3.3 精确工具完成受影响SDK单元、真实服务/策略、固定Git消费及Android/iOS门禁，分别记录；完成后将v1.2.0兼容行标verified并复核元数据。

## 4. 最终发布候选

- [x] 4.1 将完整准备材料按独立merge commit集成develop及main并推送，工作区干净，固定包含全部候选说明的最终main SHA。
- [x] 4.2 最终main SHA对应Go及SDK在线检查全部成功；复核同名tag不存在、远端无v0 tag及候选完整性，记录实际链接后才能发布。

## 5. 正式发布与消费

- [x] 5.1 创建并推送指向受测main候选的annotated v1.2.0 tag，使用同一发布说明创建GitHub Release；核对tag对象、解引用提交和Release内容一致，不移动tag。
- [x] 5.2 两个新独立宿主以GOWORK=off、无replace获取正式v1.2.0，分别编译根门面及直接适配器，核对旧/新管理员接口、公开新增符号、go list版本及冻结SQL哈希。

## 6. 发布补记与闭环

- [x] 6.1 正式消费通过后将文档状态改为已发布，补齐tag/候选/CI/消费证据和SDK矩阵，以独立文档提交集成develop及main，保持正式tag不变。
- [x] 6.2 完成发布结果独立复核、严格/链接/元数据及资源清理检查，核对远端版本与干净工作区；只在真实完成后关闭全部任务。

## Workflow follow-up

- 本轮“发布”已授权必要分支推送/集成、正式tag、Release及发布后确认；不再为同一动作重复询问。
- 发布准备变更的归档与产品部署分别按后续明确要求执行。
