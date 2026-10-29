## Context

`httpapi/admin/identities.go` 的 `revealIdentity` 直接以 `apierror.WriteJSON` 写出明文；`WriteJSON` 只设 Content-Type。消费者侧的凭证、会话与 OAuth 响应各自在写出前设置 `Cache-Control: no-store`，错误体由 `apierror.Write` 统一设置。OpenAPI `enduser.json` 对这些响应声明了 `Cache-Control` 头（`const: no-store`），`admin.json` 的 `:reveal` 200 响应 `headers` 为空，描述只写“宿主应对敏感响应实施缓存和日志保护”。

## Goals / Non-Goals

**Goals:** `:reveal` 成功响应自带 `Cache-Control: no-store`，契约与实现一致。

**Non-Goals:** 不为全部管理面响应统一加 no-store（掩码身份与审计列表本身不含明文，且宿主可能有自己的缓存策略）；不引入中间件；不改 `WriteJSON` 语义。

## Decisions

- 只在 `revealIdentity` 成功路径设置头，与消费者侧“在写出前就地设置”的做法一致，不新增通用中间件。
- OpenAPI 以与 `enduser.json` 相同的 `headers` 结构声明；`check-openapi.cjs` 不校验响应头，故额外由处理器测试断言该头。
- 描述中保留“宿主应对敏感响应实施日志保护”，去掉对缓存的宿主依赖表述。

## Risks / Trade-offs

- 无破坏性：只新增响应头。宿主若已有网关层 no-store，叠加无影响。
