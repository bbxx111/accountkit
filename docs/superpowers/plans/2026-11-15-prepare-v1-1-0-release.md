# v1.1.0 候选准备 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 在保留v1路径的前提下完成v1.1.0候选说明、迁移边界及本地发布准备验证。
**Architecture:** 已实现功能不变，仅发布文档和元数据；两个无共享文件的文档组并行，统一审查后冻结准备快照验证。
**Tech Stack:** Go1.26.5、sqlc1.31.1、PostgreSQL17、Redis7、既有Go/SDK验证脚本。
**Spec:** [发布设计](../../../openspec/changes/archive/2026-11-15-prepare-next-release/design.md)。

## Global Constraints
- 用户保留v1 module/import；准备v1.1.0候选，HTTP /v1与/admin/v1不变。
- 显著说明accountsvc服务配置和授权有破坏性变化，不能声称所有部署向后兼容。
- 不改Go/SQL/依赖/SDK清单版本，不新增服务版本轴；不推送、合入main、打tag或发布。
- v1.0.0与SDK1.0.0历史不改写；候选说明不得声称已发布。

## Review Focus
- 四旧变量是真实v1.0.0迁移项；azp模式改名不是正式历史迁移。
- Basic=P与Bearer=P∩scope、专用sub和错误顺序必须准确。
- 消费者16KiB是新增边界，管理员/服务原来已有上限。
- OIDC故障不影响整体readiness，认证依赖必须独立验收。
- 基线CI、当前本地快照、最终main候选和正式tag消费证据必须区分。

### Task 1: 发布说明与升级材料
**Files:** docs/releases/v1.1.0.md、docs/compatibility.md、docs/accountsvc.md、docs/release-checklist.md。
- [x] 从v1.0.0差异与审计编写候选说明，包括预定Release链接、运行要求、新功能、破坏性迁移、成套回退、产品边界。
- [x] 固定v1.1.0候选口径，保留v1.0.0历史和现有方法；检查所有本地链接与差异，独立复核。

### Task 2: 文档版本与SDK兼容元数据
**Files:** README.md、docs/features.md、docs/go-integration.md、docs/openapi.md、docs/openapi/{enduser,admin,service}.json、docs/client-sdks.md、sdks/compatibility.json。
- [x] 当前文档标注v1.1.0候选，三份OpenAPI只改info版本/描述；保持所有paths、DTO、security和覆盖baseline。
- [x] SDK保留1.0.0及已验证v1.0.0矩阵行，添加v1.1.0 pending行和prepare-next-release/verification.md证据；说明没有新SDK发布。
- [x] 运行check-openapi、check-sdk-coverage及其测试、两个SDK check-sdk-release --metadata-only；独立复核，不把metadata-only称为兼容验证完成。

### Task 3: 本地发布准备门禁
- [x] 保存候选源码清单与Git基线，在既有Linux验证镜像内复制完整源码及Git历史到本地文件系统；专用PG/Redis、普通测试库和两个新空恢复库。
- [x] 执行sqlc1.31.1 check-generated、OpenSpec strict、OpenAPI，以及Linux verify.sh（含历史迁移、build/vet/race/必需清单与恢复seed/verify/source-unchanged）。
- [x] 执行verify-accountsvc.sh及非root权限用例；记录跳过项和真实覆盖，清理仅本任务创建资源。

### Task 4: 交付准备记录
- [x] 独立整体复核，确认module/import、冻结迁移和SDK版本未变，版本声明一致，无失效引用。
- [x] 更新verification与任务状态，提交准备文档；后续main/tag/Release/远端消费列为需明确授权的workflow follow-up，不标已完成。
