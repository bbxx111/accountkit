## Scope

2026-11-01，按用户“按步骤进行发布”授权执行两个 SDK 首版发布。Go v1.0.0 版本不变，未涉及 ai-food 或产品部署。

## Preparation

- 功能变更 43/43 已完成；三个新增规格同步至主规格，归档位置为 ../archive/2026-11-01-add-account-client-sdks/。
- 实现证据见[功能验收记录](../archive/2026-11-01-add-account-client-sdks/verification.md)。发布准备只调整规格、证据引用和文档，不更改 SDK/Go 运行时实现。
- 候选包清单与 sdk/flutter/v1.0.0、sdk/admin-ts/v1.0.0 元数据检查通过，服务兼容基线 v1.0.0。
- OpenSpec 全量 strict 45 项通过；OpenAPI 35 操作、SDK 覆盖、两个发布元数据入口、差异空白与归档/当前文档的本地相对文件链接检查通过。三个新主规格保留 delta 的 Purpose 与所有 Requirement 正文。
- 独立发布审查及限定复核为 Approve：修复 Release 正文相对链接，补充归档记录历史时点；无遗留实质问题。功能基线 develop 的 SDK 三项 CI 已通过，Go 门禁进入 accountsvc 真实依赖/信号验证；最终结果另行补记。

## Candidate and Release

候选 main、最终 CI、正式 tag/Release 与远端消费将在实际完成后登记，当前不声明已发布。

## Boundaries

无真实提供方授权、iOS 真机/签名或产品上线验证；最终候选 iOS 无签名编译与远端 Flutter platform none 消费分别记录。原始输出保留 ignored .test-output/client-sdks-release/。
