# Proposal

## Why

本批功能已归档并合入develop，基线CI通过。用户已选择保留v1模块路径，本次按新增能力准备v1.1.0候选；必须明确Go公开源码与accountsvc部署契约的不同兼容面，保留服务接入破坏性变化的完整迁移说明。

## What Changes

- 对照正式v1.0.0盘点新增能力和真实破坏性变化，明确未发布实施过程中的改名不属于正式版本升级项。
- 修正兼容指南与服务手册的过时摘要，整理配置、主体、scope、错误、readiness及回退要求。
- 编写v1.1.0候选发布文档与核验计划；保留module/import和HTTP路由，accountsvc与库同源交付，明确本批服务配置与授权不向后兼容；最终main候选须有精确SHA对应的线上证据。
- 当前范围仅为文档准备和核验，skip_specs为true；不迁移到/v2、不新增服务版本轴，不改变已实现行为或恢复旧认证模式。
- 本轮不执行tag、GitHub Release、main集成或产品部署。

## Capabilities

### New Capabilities

无。发布准备不新增运行能力。

### Modified Capabilities

当前无运行契约变更；只准确说明已实现并归档的行为，以及用户选择保留v1后的本次发布兼容边界。

## Impact

影响发布说明、兼容与服务接入文档、OpenAPI版本元数据及本变更验收记录。Go module/import、数据库、依赖和SDK版本保持；不改历史正式tag或Release。
