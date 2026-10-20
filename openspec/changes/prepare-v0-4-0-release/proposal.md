## Why

v0.3.0 之后已有会话认证依据与客户端描述、会话上下文初始结构整合与简化、数据库设计评审索引调整及数据库运行契约修复合入 develop，其中包含 Go/HTTP 破坏性变化和旧数据库不能直接接管的边界。用户要求开始准备下一版本，需要整理 v0.4.0 的范围、兼容边界及当前验证证据，形成可审阅的发布准备结果。

## What Changes

- 新增 v0.4.0 发布说明，重点说明旧 v0.3.0 数据库不能直接接管、解绑/换绑撤销语义、直接 Go 解绑调用迁移、连接池最低容量、新会话查询与客户端描述，以及开发中曾出现字段的删除。
- 更新发布检查清单，增加 v0.4.0 版本门禁，区分准备完成、最终 main 候选门禁与 tag/Release 后的远端消费确认。
- 对精确运行时快照执行生成、迁移历史、完整库/服务 race 与必需测试、恢复演练，补齐数据库设计评审此前缺少的绑定快照验收，记录审查和未验证项。

## Capabilities

### New Capabilities

无。本次为文档与发布验证维护，设置 `skip_specs: true`，不新增运行时行为规格。

### Modified Capabilities

无。既有主规格和已归档变更是发布说明的依据；不改变接口、SQL、密钥/令牌格式或产品验收责任。

## Impact

涉及 docs/releases/v0.4.0.md、docs/release-checklist.md、docs/compatibility.md（补记撤销原因 IDENTITY_UNBOUND）和本次工件。原始证据放 ignored .test-output/prepare-v0.4-release/；验证使用带任务标签的一次性容器与数据库。授权范围为发布准备；提交、合并至 develop/main、推送、tag/Release 及远端消费确认须分别取得明确授权，不部署产品、不自动归档，历史标签与源快照保持。
