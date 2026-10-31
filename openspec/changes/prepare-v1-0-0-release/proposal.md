## Why

开发期历史整合、公开 API 清理及其完整验收已完成，用户明确要求发布 v1.0.0。需要把当前单一基线形成可独立获取的第一个正式 Go module 版本，并保存候选、CI、tag、Release 与真实远端消费的对应证据。

## What Changes

- 将已提交的整合归档纳入 develop，编写 v1.0.0 发布说明与版本专属门禁。
- 按已有规则执行完整 Linux 验证、独立审查和最终 main 候选 CI；保留 merge commit。
- 用户已单独确认：先发布 v1.0.0 并验证远端消费，再删除九个 v0 tag 和现存八个 Release；完成后更新当前文档，保留提交历史与归档记录。
- 创建指向精确候选的 annotated v1.0.0 tag 和 GitHub Release；两个独立临时 module 从远端获取并编译根门面与直接 HTTP 适配器。
- 发布后补记证据并将当前文档状态由候选更新为已发布，再合入 develop/main；不移动正式 tag。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

无。沿用 migration-safety、embedded-auth-package 等既有规格，不修改生产代码、数据库定义、依赖、HTTP/Go 契约。本变更为发布执行与文档维护，声明 `skip_specs: true`。

## Impact

涉及 docs/releases/v1.0.0.md、docs/release-checklist.md、README、当前状态文档、AGENTS.md 的开发期 tag 状态，以及本变更验收记录。远端分支、tag、Release 为发布目标；无产品部署，不补作真实服务商/网关验收，不自动归档本发布准备变更。
