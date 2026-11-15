# Tasks

## 1. 候选说明与升级契约
- [x] 1.1 编写v1.1.0候选发布说明，更新兼容/服务/发布清单，明确保留v1路径及服务迁移影响；独立审查和链接/差异检查通过。

## 2. 版本状态与SDK边界
- [x] 2.1 同步README、功能/Go/OpenAPI文档及三份OpenAPI版本元数据，保留路由和SDK版本，新增pending兼容矩阵；OpenAPI、覆盖登记及SDK元数据检查通过。

## 3. 发布准备验证
- [x] 3.1 在一次性Linux/PG/Redis环境完成sqlc、迁移历史、库与服务race/必需门禁、恢复三阶段和非root权限检查，保存实际结果与未验证项。

## 4. 准备交付
- [x] 4.1 完成全量strict、文档/引用和版本边界复核，记录准备快照与最终main候选区别，提交已完成的准备工作并明确后续发布步骤。

## Workflow follow-up

- 已按后续授权以独立 merge commit 集成 develop/main 并推送，最终候选 `6eef5e3` 的 Go/SDK CI 全部成功。
- 已创建并推送 annotated v1.1.0 tag、核对固定对象和提交，并使用正式发布说明创建 GitHub Release。
- 两个独立无 replace、GOWORK=off 的远端 Go 消费验证通过；新增公开符号、冻结 SQL 已核对，SDK 兼容证据已补记，tag 未移动。
- 用户已授权随后归档；先完成发布补记集成，再归档并修复引用。产品部署与真实提供方/网关验收另行执行。
