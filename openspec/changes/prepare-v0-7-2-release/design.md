# Design

## Context

v0.7.1（tag 对象 `5d64776`，提交 `e67d262`）发布后 develop 多出三项已合入内容：`add-admin-reveal-no-store`（已归档，合并提交 `21d9fe8`，归档合并 `61c6b9c`）、`caller_cancel` 偶发用例容忍（合并提交 `df42c46`）与接入方真实环境验收补记（合并提交 `6b5c22d`）。修复变更有自己的验收记录；本变更只整理发布材料并对准备快照重新验证，不重复构造运行时功能。

## Goals / Non-Goals

**Goals:** 固定 v0.7.2 的范围，说明 `:reveal` 响应头变化的边界与其余契约不变，在准备快照上取得完整本地验证证据，为后续最终 main 候选 CI 与发布做好准备。

**Non-Goals:** 真实网关验证响应头透传；产品部署；额外运行时功能；移动历史 tag；发布镜像或二进制。

## Decisions

- 使用 v0.7.2：版本号由本变更按补丁递增选定（用户在修复合入后指示继续发布准备，未单独点名版本号；若用户另定版本号，发布材料随之改名）。本次只有一项向后兼容的新增行为（`:reveal` 所有响应多一个 `Cache-Control: no-store` 响应头），无路由、字段、错误码、公开 Go API、数据库或依赖变化，按补丁版本号承载；与 v0.7.1 的做法一致。新增响应头记入 OpenAPI 管理契约（`adminRevealIdentity` 200 `headers`），属于额外声明，不改变既有客户端的解析。
- 发布说明只设一节能力说明加“其他改动”“升级与兼容”“运行与验证”，不复制 OpenAPI 与规格的全文。
- 当前 0001 的 SHA256 仍为 `ef2d1f229fa15b660cc7ecb4f417615b76866c19494641c96faf25bab24188b6`，与 v0.7.1 发布的定义逐字节相同，不新增 pin；go.mod/go.sum 无变化。
- 文档版本表述在准备阶段按[状态措辞约定](../../../docs/release-checklist.md#发布动作与消费确认)改为“v0.7.2（候选）”，发布说明状态为“发布候选”并写明 tag 将固定该提交及可预知的 Release 链接；正式发布后再补记 tag 对象、候选 SHA、CI 与消费证据；不写“已发布”或“准备中”。两份 OpenAPI 的 `info.version` 改为 v0.7.2，描述注明操作集合与 v0.7.1 相同、管理契约 `:reveal` 响应声明 no-store。
- `docs/compatibility.md` 新增首节记录 `:reveal` 响应头变化及升级/回退边界，v0.7.1 的开发模式小节下移保留。
- 准备快照的完整验证在 `tests/Dockerfile` 构建的 Linux 验证镜像中执行 `verify.sh` 与 `verify-accountsvc.sh`，使用本任务创建的一次性 PostgreSQL 17 / Redis 7 容器与数据库，结束后删除；正式门禁仍以最终 main 候选的在线 CI 为准。
- 本地集成均使用 `--no-ff`；合并 develop/main、推送、tag 与 Release 各自取得明确授权后执行。

## Risks / Trade-offs

- `caller_cancel` 的容忍分支只覆盖已诊断的客户端响应竞争（502 `upstream unavailable`、代理已报 context canceled、上游在打断后结束），其他结果仍失败；若 CI 出现新的失败形态，须重新诊断而不是继续放宽。
- 准备快照与最终候选可能不同：任何后续提交都以最终 main 候选 CI 重新判定，不以本地准备证据替代。
