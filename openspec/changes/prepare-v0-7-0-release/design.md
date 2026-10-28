# Design

## Context

v0.6.0 发布后共归档六项变更：`add-local-access-token-verification`、`add-accountsvc-sms-and-email-brand`、`add-purged-user-feed`、`unify-accountsvc-env-prefix`、`add-service-bearer-auth`、`add-accountsvc-email-templates`；另有四项不经 OpenSpec 的维护（业务服务错误与配置契约文档、服务集成测试端口竞争与秘密泄露误报修复、网关取消用例诊断、CI 去重）。各变更已有自己的验收记录；本变更只整理发布材料并对准备快照重新验证，不重复构造运行时功能。

## Goals / Non-Goals

**Goals:** 固定 v0.7.0 的范围，给出破坏性变化的调用迁移说明和新实例边界，在准备快照上取得完整本地验证证据，为后续最终 main 候选 CI 与发布做好准备。

**Non-Goals:** 旧数据库、旧令牌或旧配置的迁移工具；产品部署；阿里云、SMTP、IdP 的真实联调；额外运行时功能；移动历史 tag；发布镜像或二进制。

## Decisions

- 使用 v0.7.0：用户已明确选择。本次包含当前初始结构变化（`purged_user`）、破坏性 Go/HTTP/配置变化（ES256、内省语义、`ACCOUNTSVC_` 前缀、Basic 变量改名、服务邮件文案）与新增公开类型/配置，区别于修复版本；1.0 前以次版本号承载破坏性变化，与 v0.3.0–v0.6.0 一致。
- 发布说明按能力分节，破坏性变化集中在“升级与兼容”节，详细对照链接到 `docs/compatibility.md` 各节，不在两处复制完整迁移表。
- 当前 0001 的 SHA256 为 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6`，与 v0.6.0 发布的 `4c6b565e…` 不同；v0.6.0 已作为独立 pin 固定（`add-purged-user-feed` 已完成，避免 v0.6.0 发布时的首个候选 CI 失败重演）。v0.6.0 及更早结构无写拒绝，dirty 优先；新实例按迁移手册使用新空 schema，不 Force、改版本或清库。
- 文档版本表述在准备阶段改为“v0.7.0（准备中）”，正式发布后再补记 tag、候选 SHA、CI 与消费证据；不提前写“已发布”。
- 准备快照的完整验证在 `tests/Dockerfile` 构建的 Linux 验证镜像中执行 `verify.sh` 与 `verify-accountsvc.sh`，使用本任务创建的一次性 PostgreSQL 17 / Redis 7 容器与数据库，结束后删除；正式门禁仍以最终 main 候选的在线 CI 为准。
- 本地集成均使用 `--no-ff`；合并 develop/main、推送、tag 与 Release 各自取得明确授权后执行。

## Risks / Trade-offs

- 破坏性变化跨越令牌、配置、数据库与邮件文案：发布说明逐项列出调用迁移要点并链接兼容说明；accountsvc 部署须一次完成改名、换密钥与新空库，客户端刷新一次令牌即可。
- 阿里云短信、SMTP 邮件客户端显示、外部 IdP 均未真实联调：发布说明与检查表明确由集成方负责，模拟与 HTTP 契约测试不代表真实平台行为。
- 准备快照与最终候选可能不同：任何后续提交都以最终 main 候选 CI 重新判定，不以本地准备证据替代。
