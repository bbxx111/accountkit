# Proposal

## Why

显式管理权限配置和全仓字符串契约收敛均已归档并合入develop，用户已在了解管理员部署迁移影响后明确要求发布v1.2.0。需要形成准确的发布材料、最终main候选及逐提交验证证据，再完成不可移动的正式tag、Release和远端消费确认。

## What Changes

- 对照正式v1.1.0说明新增Go权限扩展、内部重构及 **BREAKING** 的accountsvc管理员显式角色授权；保留Go module/import和HTTP路由，明确旧部署不配置绑定会返回403，以及成套回退要求。
- 将README、集成/协议/兼容文档、三份OpenAPI版本元数据及SDK兼容矩阵更新为v1.2.0候选；SDK保持独立1.0.0，不新增服务版本轴或运行行为。
- 通过独立Linux/PostgreSQL/Redis验证准备快照，并对最终main候选执行精确SHA对应的Go及SDK/Android/iOS在线门禁。
- 按本轮完整发布授权，使用独立merge commit集成develop/main，创建并推送annotated v1.2.0 tag和GitHub Release；验证两个无replace独立Go宿主的正式版本消费。
- 以独立补记提交更新为已发布并保存tag、候选、CI和消费结果；正式tag不移动，发布准备变更的归档仍为单独步骤。

## Capabilities

### New Capabilities

无。发布流程和文档不新增运行能力。

### Modified Capabilities

无运行需求变化。管理权限、服务运行、嵌入兼容和SDK分发沿用已同步主规格；skip_specs为true，不新建增量规格。

## Impact

影响发布说明、版本状态文档、OpenAPI元数据、SDK兼容登记和本变更验收记录。Go公开API为加法兼容；本次指定v1.2.0不等于服务部署全面向后兼容，也不授权未来任意破坏v1契约。不改变SQL、冻结迁移/哈希、令牌/密文、依赖或SDK版本；真实提供方、网关及产品部署继续由集成方验收。
