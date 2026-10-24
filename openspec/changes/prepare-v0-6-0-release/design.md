# Design

## Context

v0.5.0 发布后共归档八项变更：`add-google-sign-in`、`add-identity-provider-registry`、`add-wechat-mini-program-sign-in`、`add-facebook-sign-in`、`add-alipay-app-sign-in`、`add-qq-app-sign-in`、`add-weibo-app-sign-in`、`configurable-provider-profile-backfill`。各变更已有自己的验收记录；本变更只整理发布材料并对准备快照重新验证，不重复构造运行时功能。

## Goals / Non-Goals

**Goals:** 固定 v0.6.0 的范围，给出破坏性变化的调用迁移说明和新实例边界，在准备快照上取得完整本地验证证据，为后续最终 main 候选 CI 与发布做好准备。

**Non-Goals:** 旧数据库迁移工具、产品部署、真实第三方平台联调、额外运行时功能、移动历史 tag、发布镜像或二进制。

## Decisions

- 使用 v0.6.0：用户已明确选择。本次包含新增公开类型/配置、破坏性 Go/HTTP 变化和当前初始结构变化，区别于修复版本；1.0 前以次版本号承载破坏性变化，与 v0.3.0–v0.5.0 一致。
- 发布说明按能力分节，破坏性变化集中在“升级与兼容”节，详细对照链接到 `docs/compatibility.md` 各节，不在两处复制完整迁移表。
- 当前 0001 的 SHA256 为 `4c6b565e336aba88db5f6bf219407b026a7c1b438057b61470377005ed2509c5`，与 v0.5.0 发布的 `491068d7…` 不同；v0.5.0 及更早结构无写拒绝，dirty 优先。新实例按迁移手册使用新空 schema，不 Force、改版本或清库。
- 文档版本表述在准备阶段改为“v0.6.0（准备中）”，正式发布后再补记 tag、候选 SHA、CI 与消费证据；不提前写“已发布”。
- 准备快照的完整验证在 Linux 验证镜像中执行 `verify.sh` 与 `verify-accountsvc.sh`，使用一次性数据库与 Redis 库号；正式门禁仍以最终 main 候选的在线 CI 为准。
- 本地集成均使用 `--no-ff`；合并 develop/main、推送、tag 与 Release 各自取得明确授权后执行。

## Risks / Trade-offs

- 破坏性变化面广（凭证信封、`IdentityKind`、提供方装配、微信 subject、资料补填）：发布说明逐项列出调用迁移要点并链接兼容说明；客户端与服务端须同步升级。
- 第三方平台均未真实联调：发布说明与检查表明确由集成方负责，模拟与 HTTP 契约测试不代表真实平台行为。
- 准备快照与最终候选可能不同：任何后续提交都以最终 main 候选 CI 重新判定，不以本地准备证据替代。
