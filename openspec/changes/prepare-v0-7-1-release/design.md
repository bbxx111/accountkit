# Design

## Context

v0.7.0（tag 固定 `6f3b072`）发布后 develop 只多出一项已归档变更 `allow-development-http-issuer`（合并提交 `2397058`），以及 v0.7.0 的发布补记与归档。该变更有自己的验收记录；本变更只整理发布材料并对准备快照重新验证，不重复构造运行时功能。

## Goals / Non-Goals

**Goals:** 固定 v0.7.1 的范围，说明开发模式例外的边界与 production 不变，在准备快照上取得完整本地验证证据，为后续最终 main 候选 CI 与发布做好准备。

**Non-Goals:** 真实 Keycloak 联调；产品部署；额外运行时功能；移动历史 tag；发布镜像或二进制。

## Decisions

- 使用 v0.7.1：用户已明确选择。本次只有一项向后兼容的新增行为（仅 development 生效），无数据库、公开 Go API、HTTP 契约或错误码变化，按补丁版本号承载；与 v0.3.0–v0.7.0 以次版本号承载破坏性变化的做法一致。
- 发布说明只设一节能力说明加“升级与兼容”“运行与验证”，不复制 `docs/accountsvc.md` 的完整配置表。
- 当前 0001 的 SHA256 仍为 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6`，与 v0.7.0 发布的定义逐字节相同，不新增 pin；go.mod/go.sum 无变化。
- 文档版本表述在准备阶段改为“v0.7.1（准备中）”，正式发布后再补记 tag、候选 SHA、CI 与消费证据；不提前写“已发布”。两份 OpenAPI 的 `info.version` 改为 v0.7.1，描述注明操作集合与 v0.7.0 相同。
- 准备快照的完整验证在 `tests/Dockerfile` 构建的 Linux 验证镜像中执行 `verify.sh` 与 `verify-accountsvc.sh`，使用本任务创建的一次性 PostgreSQL 17 / Redis 7 容器与数据库，结束后删除；正式门禁仍以最终 main 候选的在线 CI 为准。
- 本地集成均使用 `--no-ff`；合并 develop/main、推送、tag 与 Release 各自取得明确授权后执行。

## Risks / Trade-offs

- 开发例外若被误用于 production：代码以 `ACCOUNTSVC_MODE` 为唯一开关，production 路径不变，发布说明与配置文档明确只限 development 并有 WARN。
- 准备快照与最终候选可能不同：任何后续提交都以最终 main 候选 CI 重新判定，不以本地准备证据替代。
