# Proposal

## Why

OpenSpec严格校验发现七份主规格的十一条需求正文过长，单条混合多个独立行为，妨碍检索与验收。整理结构须保留全部约束，并防止未归档认证变更覆盖回旧结构。

## What Changes

- 拆分已报告的十一条超长需求，原标题与原场景保留，移出的规范性规则成为独立需求及对应场景。
- 同步 harden-accountsvc-authentication 中覆盖相关需求的增量块，保持其已确认的认证行为变化。
- 记录规则迁移对应关系，执行独立语义审查及全量严格校验。
- 更新关联认证验收记录的状态引用，保留当时的历史失败证据。
- 仅维护规格文档，不修改产品行为、公开API、代码、SQL、测试断言或校验阈值。
- 后续按用户明确要求，依据既有实现校正默认清单语言协商和八张数据表加引擎版本表的表述，单独记录修正依据。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

无行为或约束变化；本变更设置 skip_specs: true，直接维护既有规格结构，不另建重复delta。

## Impact

范围为 accountsvc-runtime、accountsvc-service-authentication、consumer-token-introspection、embedded-auth-package、purged-user-feed、verification-delivery、versioned-auth-storage 七份主规格，以及已存在认证变更中的关联delta。既有归档历史不修改，本次不执行归档或发布。
