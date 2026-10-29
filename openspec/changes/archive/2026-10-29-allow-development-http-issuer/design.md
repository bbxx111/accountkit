## Context

管理员认证（`adminauth`）与服务 Bearer 认证（`serviceauth`）共用 `internal/accountsvc/oidcjwt`：`ValidIssuer` 要求绝对 HTTPS URL；`Verifier.fetchJSON` 对 discovery 地址与 `jwks_uri` 再次调用同一 HTTPS 校验；`Parse` 以 `jwt.WithIssuer` 精确匹配 `iss`。配置阶段 `adminauth.Config.Validate` 与 `loadService` 都直接调用 `ValidIssuer`；`Config.Mode` 在二者之前解析，取值只能是 `production`（默认）或 `development`。既有先例：短信 endpoint 在非 production 允许回环 HTTP；`examples/remoteauth` 以 `AllowHTTP` 字段表达同类放宽。

接入方本地 Keycloak 地址为 `http://localhost:8180`，容器内为 `http://host.docker.internal:8180`，因此不能只放开回环地址。

## Goals / Non-Goals

**Goals:**
- development 模式下 HTTP issuer 可完成配置校验、discovery、JWKS 加载与 `iss` 匹配。
- production 行为与错误信息逐字不变。
- 开发例外在启动日志中可见。

**Non-Goals:**
- 不新增开关变量、自定义 CA 或跳过 TLS 验证的选项。
- 不放开重定向，不改变 discovery issuer 必须与配置精确一致的规则。
- 不改动消费者 JWT、第三方登录或短信 endpoint 的校验。

## Decisions

### D1 触发条件：仅 `ACCOUNTSVC_MODE=development`

放宽由现有 `Mode` 隐式决定，不新增变量。理由：开发模式已经是「本地调试」的唯一显式信号（HTTP 监听、无认证 SMTP 均由它控制），再加一个开关只会让生产误配多一条路径。production 下代码路径与现在完全相同。

### D2 放宽范围：整条链路，不限回环地址

`oidcjwt.Options` 新增 `AllowHTTP bool`；`Verifier` 保存该标志，`fetchJSON` 与 `discover` 的地址校验据此允许 `http://`。因此开发模式下 discovery 地址（由 issuer 派生）、`jwks_uri` 与 `iss` 匹配一致工作。不限制回环地址，因为容器内访问宿主 Keycloak 使用 `host.docker.internal`。重定向仍拒绝（`CheckRedirect` 不变）。

### D3 接口形态

- `oidcjwt.ValidIssuer(issuer)` 保持只接受 HTTPS；新增 `oidcjwt.ValidPlainIssuer(issuer)` 在同样约束下另接受 `http://`。两者共用一个私有校验函数。
- `adminauth.Config` 新增 `AllowHTTP bool`，`Validate` 据此选择校验函数，`New` 透传到 `oidcjwt.Options`。
- `ServiceConfig` 新增 `AllowHTTP bool`，`loadService` 据此校验并由 `serviceAuthenticators` 透传。
- `loadAdmin` 与 `loadService` 均以 `c.Mode == "development"` 赋值。`Mode` 在二者之前解析，无顺序问题。

### D4 WARN 位置与文案

serve 在 `migrate` 返回之后、管理员 discovery 之前调用 `warnPlainHTTPIssuers(cfg, logger)`，使 WARN 先于任何明文网络访问。文案：

- `accountsvc: administrator issuer uses plain HTTP; allowed only in development mode`，属性 `settings=ACCOUNTSVC_ADMIN_ISSUER`
- `accountsvc: service token issuer uses plain HTTP; allowed only in development mode`，属性 `settings=ACCOUNTSVC_SERVICE_ISSUER`

沿用既有启动 WARN 的约定：只写变量名，不写值。migrate 不记录。

### D5 不改错误信息

production 拒绝 HTTP issuer 的错误仍为 `ACCOUNTSVC_ADMIN_ISSUER / ADMIN_AUDIENCE / ADMIN_ROLES_CLAIM / ADMIN_USERNAME_CLAIM` 与 `ACCOUNTSVC_SERVICE_ISSUER`；`oidcjwt.New` 与 `adminauth.Validate` 在未放开时的错误文本不变。

## Risks / Trade-offs

- 开发模式下令牌经明文网络传输，可被同网段读取。接受：仅限显式 `development`，并有 WARN；production 不受影响。
- 开发模式下 `jwks_uri` 为 HTTP 时同样接受，即使 issuer 是 HTTPS。接受：开发模式整体放宽比区分来源更可预期，且 production 不变。
- 容器内以 `host.docker.internal` 访问 Keycloak 时，Keycloak 返回的 discovery `issuer` 与令牌 `iss` 必须与配置值逐字相同，这是既有规则，本变更不放宽；接入方需固定 Keycloak 的 hostname。

## Migration Plan

无数据或配置迁移。production 部署无需改动；本地 compose 可直接设置 HTTP issuer。回退只需恢复 HTTPS issuer。

## Open Questions

无。
