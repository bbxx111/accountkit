## Why

两个客户端 SDK 已完成实现并通过 PR #20 合入 develop。用户明确要求按发布步骤归档功能、集成 main、发布两个 SDK 首版并验证远端消费，需要保存候选提交、CI、tag、Release 与实际安装证据。

## What Changes

- 同步三个 SDK 主规格并归档 add-account-client-sdks，修复证据引用。
- 发布 accountkit_flutter 1.0.0 与 @accountkit/admin-client 1.0.0，使用独立 annotated tag 和 GitHub Release。
- 发布准备通过独立 merge commit 进入 develop/main；最终 main 候选完整 Go 与 SDK CI 通过后发布。
- 发布后从真实远端 tag 验证冻结安装、类型/静态检查、行为测试与 TS 消费构建，再补记已发布状态。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

无。本变更为已有能力的发布与文档维护，声明 skip_specs: true。

## Impact

涉及 SDK 发布说明、当前状态、OpenSpec 归档与验收记录，以及远端 develop/main、两个 SDK tag/Release。不更改生产 Go/SQL/HTTP 契约，不发布新的 Go module 版本，不修改 ai-food，不执行产品部署。
