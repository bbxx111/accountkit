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

- 获明确授权后，以独立merge commit集成develop/main并推送，记录最终main候选SHA与全部线上CI；候选有变动须核对新SHA。
- 获明确发布授权后创建并推送annotated v1.1.0 tag，核对对象/解析SHA，使用同一发布说明创建Release。
- 发布后执行两个独立无replace、GOWORK=off的远端Go消费验证，核对新增公开符号和冻结SQL；补记已发布及SDK兼容证据，不移动tag。
- 发布准备变更按单独明确请求归档；产品部署与真实提供方/网关验收另行执行。
