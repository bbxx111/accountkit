## ADDED Requirements

### Requirement: 已清理用户拉取入口

接入说明 SHALL 将 `GET /v1/purgedUsers` 列为内部入口，与内省相同：公网网关 SHALL 拒绝，请求不进入后端；只允许授权的内部业务服务访问；后端端口的直连来源同样受限。开放消费者 `/v1` 路由 SHALL 不连带开放该路由。

#### Scenario: 公网访问被拦截
- **WHEN** 部署方执行网关接入验收
- **THEN** 公网请求 `/v1/purgedUsers` 被网关拒绝且不进入后端，授权的内部业务服务可以访问
