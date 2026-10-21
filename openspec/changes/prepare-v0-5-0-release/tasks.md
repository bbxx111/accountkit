# Tasks

## 1. 收尾与发布材料

- [x] 1.1 修复三处规格长文本限制并独立审查，验证 OpenSpec strict 27/27 通过；以 --no-ff 合入头像分支。
- [x] 1.2 编写 v0.5.0 发布说明、版本门禁和文档版本，复核公开 API/依赖/HTTP/新实例边界与链接。
- [x] 1.3 对发布说明做独立审查，运行生成一致性、迁移历史、OpenSpec strict 和 OpenAPI 契约检查，记录实际证据。

## 2. 最终候选与发布

- [ ] 2.1 提交发布材料，以独立 merge commit 合入 develop 和 main，推送并记录最终候选 SHA。
- [ ] 2.2 核验最终候选在线 CI 全部通过，包括 sqlc、完整库/服务、race、必需用例及真实恢复。
- [ ] 2.3 创建并推送 v0.5.0 annotated tag，核对远端指向，按已审查正文发布正式 GitHub Release。
- [ ] 2.4 两个独立宿主从远端无 replace 消费 v0.5.0，验证根门面/直接 enduser 新能力与初始 SQL 哈希。
- [ ] 2.5 补记发布状态、CI、tag/Release、消费证据和未验证范围，提交并集成文档。

## Workflow follow-up

- 产品环境部署和本发布变更归档另行明确执行。
