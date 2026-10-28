# Proposal

## Why

v0.6.0 之后，消费者 access 改用 ES256 与严格内省、accountsvc 阿里云短信、已清理用户拉取清单（直接修改当前 0001）、accountsvc 配置前缀统一、服务调用 Bearer 认证、邮件模板清单与多语言均已完成并归档到 develop。用户确认版本号为 v0.7.0，并授权开始发布准备；需要固定发布范围、编写发布说明与版本门禁，并对准备快照执行完整验证，为后续单独授权的合并、tag 与 Release 提供证据。

## What Changes

- 盘点 v0.6.0..develop 的已归档变更与配套维护，编写 `docs/releases/v0.7.0.md`：新增能力、破坏性变化（ES256 与 `JWTKeys` 类型、内省语义、`ACCOUNTSVC_` 前缀、内省 Basic 变量改名、当前 0001 新增 `purged_user`、服务邮件改用清单布局）、新实例安装与不可接管边界、宿主与接入方责任、未联调范围。
- 在发布检查表中增加 v0.7.0 版本门禁并更新当前待办；把 README、兼容、功能、Go 集成、OpenAPI 文档中的“当前版本/下一个版本”表述同步为 v0.7.0 候选，两份 OpenAPI 的 `info.version` 与 `info.description` 改为 v0.7.0（准备中）。
- 对准备快照执行 OpenSpec strict、生成一致性、迁移历史、OpenAPI 检查，以及 Linux 验证镜像中的完整库与服务 race/必需门禁/恢复演练，结果写入本变更 `verification.md`；发布说明经独立审查。
- 合并 develop/main、最终候选 CI、tag、GitHub Release 与远端消费确认属于后续步骤，分别取得明确授权后执行。

## Capabilities

### New Capabilities

无。本次为发布与文档维护，设置 skip_specs，不新增运行时行为。

### Modified Capabilities

无。

## Impact

范围为发布说明、发布检查表、README 与 `docs/` 的版本表述、两份 OpenAPI 的版本号与描述，以及本变更工件；不修改生产代码、SQL、生成代码、冻结源或历史 tag。当前 0001 相对 v0.6.0 已按 `add-purged-user-feed` 的明确授权新增 `purged_user` 表，v0.6.0 数据库不能直接接管；本轮不提供旧数据迁移工具，不安排产品部署。
