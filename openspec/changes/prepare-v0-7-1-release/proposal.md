# Proposal

## Why

v0.7.0 发布后，接入方（ai-food）本地调试需要 accountsvc 在开发模式下接受 `http://` 的管理员与服务令牌签发方；该变更 `allow-development-http-issuer` 已完成、归档并以 `--no-ff` 合入 develop（`2397058`）。接入方要从 tag 构建本地镜像，用户确认版本号为 v0.7.1，并授权发布准备；需要固定发布范围、编写发布说明与版本门禁，并对准备快照执行完整验证，为后续单独授权的合并 main、tag 与 Release 提供证据。

## What Changes

- 盘点 v0.7.0..develop 的变更，编写 `docs/releases/v0.7.1.md`：开发模式 HTTP issuer 的触发条件、放开范围、WARN、production 不变；无数据库、公开 Go API、HTTP 契约或错误码变化。
- 在发布检查表中增加 v0.7.1 版本门禁并更新当前待办；把 README、功能、Go 集成、OpenAPI、兼容文档中的“当前版本”表述同步为 v0.7.1 候选，两份 OpenAPI 的 `info.version` 与 `info.description` 改为 v0.7.1（准备中）。
- 对准备快照执行 OpenSpec strict、生成一致性、迁移历史、OpenAPI 检查，以及 Linux 验证镜像中的完整库与服务 race/必需门禁/恢复演练，结果写入本变更 `verification.md`；发布说明经独立审查。
- 合并 develop/main、最终候选 CI、tag、GitHub Release 与远端消费确认属于后续步骤，分别取得明确授权后执行。

## Capabilities

### New Capabilities

无。本次为发布与文档维护，设置 skip_specs，不新增运行时行为。

### Modified Capabilities

无。

## Impact

范围为发布说明、发布检查表、README 与 `docs/` 的版本表述、两份 OpenAPI 的版本号与描述，以及本变更工件；不修改生产代码、SQL、生成代码、冻结源或历史 tag。当前 0001 与 go.mod/go.sum 相对 v0.7.0 无变化。
