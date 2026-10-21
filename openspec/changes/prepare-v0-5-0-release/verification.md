# v0.5.0 发布验收

日期：2026-10-21。用户要求处理 v0.4.0 后剩余收尾，确认尚无正式部署、无需旧数据库迁移，并明确选择“按 v0.5.0 正式发布”。本记录仅将实际完成的步骤记为通过。

## 当前准备结果

- 三项新增功能任务已完成并归档：资料属性、本人资料 HTTP/accountsvc 配置、头像。
- 规格长文本修复提交 `09d4cdd`，独立只读审查 PASS；`openspec validate --all --strict` 27/27 通过，无警告，未改变运行时契约。
- 头像以 --no-ff 合入 develop，merge commit 为 `68c8b9e1a1651e0482259a0425f2f365219ece81`。发布准备分支为 `docs/prepare-v0-5-0-release`。
- 远端检查时 develop 为 `eb5863d5170226024fc76f9154a9477369ccd987`、main 为 `f55a66e51504f96d3990c58edacbcefa053bbad9`；远端 v0.5.0 尚不存在。正式操作前将再次核对，不能用此快照覆盖后续远端修改。
- 发布材料独立审查通过；唯一发现为管理面 OpenAPI 错将新增头像归于 v0.4.0，已修正两面描述和 HTTP 文档，限定复审 PASS，无遗留问题。
- 本轮 `openspec validate --all --strict` 28/28 通过，`check-generated.sh`、`check-migrations.sh`、`git diff --check` 通过；Swagger Parser 校验两份 OpenAPI 3.1.0/v0.5.0 通过，`check-openapi.cjs` 核对消费者 24、管理面 11 操作及 DTO 通过，发布材料本地文件链接均存在。
- 相对头像实施提交 `4e84a78`，Go、SQL、go.mod/go.sum、scripts 和 CI 文件均无差异；不把本轮文档维护称为重新运行本地完整测试。

## 验证策略与历史证据

本轮没有运行时代码变更。头像定版已运行完整库验证：44 包、894 个顶层测试、793 项必需门禁、race 和 pg_dump/pg_restore 均通过，权限文件用例有非 root 补验；见[头像验收](../archive/2026-10-21-add-user-avatar-url/verification.md)。本人资料 HTTP 阶段另有完整服务进程 race 及 29 项服务必需门禁证据，见[HTTP 验收](../archive/2026-10-21-add-self-profile-http/verification.md)。这些是准备证据，不代表最终候选在线 CI 通过。

正式发布须核对最终 main SHA 对应的 GitHub Actions：生成检查、verify.sh、verify-accountsvc.sh 均成功，随后固定 tag/Release 并验证远端消费。当前在线候选、发布及消费验证仍待执行。

## 未验证范围

产品部署、实际服务商与设备联调、网关/监控平台和目标环境验收未执行，不作为库版本发布已完成的证据。本轮没有旧数据库迁移任务，不修改既有历史保护。没有授权发布镜像或二进制。
