# Proposal

## Why

v0.7.1 发布后，接入方（ai-food）以真实 Keycloak 验收时观察到管理面 `GET /users/{user}/identities/{identity}:reveal` 响应缺少 `Cache-Control: no-store`。修复变更 `add-admin-reveal-no-store` 已完成、归档并以 `--no-ff` 合入 develop（`21d9fe8`，归档 `61c6b9c`）；同批合入的还有 `caller_cancel` 偶发用例的容忍调整（`df42c46`）与接入方真实环境验收补记（`6b5c22d`）。接入方等待修复版本号以跟进升级；用户在合入推送完成后指示继续，版本按补丁递增为 v0.7.2。需要固定发布范围、编写发布说明与版本门禁，并对准备快照执行完整验证，为后续单独授权的合并 main、tag 与 Release 提供证据。

## What Changes

- 盘点 v0.7.1..develop 的变更，编写 `docs/releases/v0.7.2.md`：`:reveal` 所有响应带 `Cache-Control: no-store`（OpenAPI 管理契约声明该头）、测试维护与文档补记；无数据库、公开 Go API、路由/字段/错误码或依赖变化。
- 在发布检查表中增加 v0.7.2 版本门禁并更新当前待办；把 README、功能、Go 集成、OpenAPI、兼容文档中的“当前版本”表述同步为 v0.7.2（候选），两份 OpenAPI 的 `info.version` 与 `info.description` 改为 v0.7.2。
- 对准备快照执行 OpenSpec strict、生成一致性、迁移历史、OpenAPI 检查，以及 Linux 验证镜像中的完整库与服务 race/必需门禁/恢复演练，结果写入本变更 `verification.md`；发布说明经独立审查。
- 合并 develop/main、最终候选 CI、tag、GitHub Release 与远端消费确认属于后续步骤，分别取得明确授权后执行。

## Capabilities

### New Capabilities

无。本次为发布与文档维护，设置 skip_specs，不新增运行时行为。

### Modified Capabilities

无。`:reveal` 响应头的规格增量已随 `add-admin-reveal-no-store` 归档同步到主规格。

## Impact

范围为发布说明、发布检查表、README 与 `docs/` 的版本表述、两份 OpenAPI 的版本号与描述，以及本变更工件；不修改生产代码、SQL、生成代码、冻结源或历史 tag。当前 0001 与 go.mod/go.sum 相对 v0.7.1 无变化。
