# Proposal

## Why

v0.5.0 之后，身份提供方注册表与统一凭证信封、Google、通用 OIDC、微信小程序与固定标识模式、Facebook 及数据删除、支付宝、QQ、微博，以及可配置的提供方资料补填均已完成并归档到 develop。用户确认版本号为 v0.6.0，并要求开始发布准备；需要固定发布范围、编写发布说明与版本门禁，并对准备快照执行完整验证，为后续单独授权的合并、tag 与 Release 提供证据。

## What Changes

- 盘点 v0.5.0..develop 的已归档变更，编写 `docs/releases/v0.6.0.md`：新增能力、破坏性变化（统一凭证信封、`IdentityKind` 字符串底型、`user.Deps` 提供方装配、`identity.kind` 列类型、微信 subject 格式、资料补填规则）、新实例安装与不可接管边界、宿主责任与未联调范围。
- 在发布检查表中增加 v0.6.0 版本门禁，更新当前待办；把 README、Go 集成、OpenAPI、功能、兼容与迁移文档中的“当前版本/未发布”表述同步为 v0.6.0 候选，两份 OpenAPI 的 `info.version` 改为 v0.6.0。
- 对准备快照执行生成一致性、迁移历史、OpenAPI、OpenSpec strict、完整库与服务 race/必需门禁及恢复演练，结果写入本变更 `verification.md`；发布说明经独立审查。
- 合并 develop/main、最终候选 CI、tag、GitHub Release 与远端消费确认属于后续步骤，分别取得明确授权后执行。

## Capabilities

### New Capabilities

无。本次为发布与文档维护，设置 skip_specs，不新增运行时行为。

### Modified Capabilities

无。

## Impact

范围为发布说明、发布检查表、README 与 `docs/` 的版本表述、两份 OpenAPI 的版本号，以及本变更工件；不修改生产代码、SQL、生成代码、冻结源或历史 tag。当前 0001 相对 v0.5.0 已按 `add-identity-provider-registry` 与 `add-google-sign-in` 的明确授权改变，v0.5.0 数据库不能直接接管；本轮不提供旧数据迁移工具，不安排产品部署。
