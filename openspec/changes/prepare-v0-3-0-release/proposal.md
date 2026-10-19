## Why

v0.2.0 之后已有身份策略、通用审计、发送器及观测能力等变更合入 develop，需要整理 v0.3.0 的范围、兼容边界及当前验证证据，形成可审阅的发布准备结果。用户已确认按 v0.3.0 准备。

## What Changes

- 新增 v0.3.0 发布说明，重点说明 Go/HTTP 调用迁移和旧数据库不能直接接管的边界。
- 更新发布检查清单，区分准备完成、最终 main 候选门禁与 tag/Release 发布后的远端消费确认。
- 对当前运行时代码执行生成、完整库/服务与恢复验证，记录精确快照、审查和未验证项。

## Capabilities

### New Capabilities

无。本次为文档与发布验证维护，设置 `skip_specs: true`，不新增运行时行为规格。

### Modified Capabilities

无。既有主规格和已归档变更是发布说明的依据；不改变接口、SQL、密钥/令牌格式或产品验收责任。

## Impact

涉及 docs/releases/v0.3.0.md、docs/release-checklist.md 和本次工件。原始证据放 ignored .test-output/prepare-v0.3-release/；验证使用专用一次性资源。最初授权为发布准备；准备完成后，用户针对提交、合并至 main、最终候选 CI 和 tag/Release 发布的下一步明确要求继续。后续执行正式发布及远端消费确认，不部署产品或自动归档，旧发布标签与源快照保持。
