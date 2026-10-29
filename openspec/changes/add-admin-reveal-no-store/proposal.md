## Why

管理面 `GET /admin/v1/users/{user}/identities/{identity}:reveal` 返回身份明文（手机号/邮箱），但其 200 响应没有 `Cache-Control: no-store`；库内只有错误体、消费者凭证与会话响应设置了该头。接入方（ai-food）在 v0.7.1 验收时经网关观察到这一点。明文身份是最敏感的管理面输出，不应依赖宿主或网关记住为该路由关闭缓存。

## What Changes

- `:reveal` 的 200 响应 SHALL 带 `Cache-Control: no-store`；错误响应沿用既有 `apierror.Write` 的 no-store。
- OpenAPI `admin.json` 的该操作 200 响应声明 `Cache-Control` 头；`docs/openapi.md` 补一句说明。
- 不改 HTTP 方法、路径、DTO、审计或角色要求；其他管理面响应（掩码身份、会话、审计）不在本次范围。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `embedded-auth-package`：「管理接口及审计兼容」补充身份明文响应禁止缓存的要求与场景。

## Impact

- 代码：`httpapi/admin/identities.go` 一行；`httpapi/admin/identities_test.go` 断言。
- 文档：`docs/openapi/admin.json`、`docs/openapi.md`。
- 无数据库、配置或依赖变化；向后兼容（只新增响应头）。
