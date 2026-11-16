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
