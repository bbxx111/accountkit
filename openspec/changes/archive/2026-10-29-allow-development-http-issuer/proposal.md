## Why

接入方（ai-food）对接 v0.7.0 时发现 `ACCOUNTSVC_ADMIN_ISSUER` 与 `ACCOUNTSVC_SERVICE_ISSUER` 无条件要求绝对 HTTPS 地址，没有开发例外，也没有自定义 CA 选项。本地 Keycloak 以 `http://localhost:8180`（容器内 `http://host.docker.internal:8180`）提供服务，accountsvc 的管理面与服务 Bearer 认证在本地无法启动。接入方用户决定：让 accountsvc 在本地调试时支持 HTTP issuer，并打一条 WARN，而不是为本地 Keycloak 配置开发 CA。

## What Changes

- `ACCOUNTSVC_MODE=development` 时，`ACCOUNTSVC_ADMIN_ISSUER` 与 `ACCOUNTSVC_SERVICE_ISSUER` 额外接受 `http://` 绝对 URL；其他格式约束（无凭据、无查询、无片段、无首尾空白）不变。不新增开关变量。
- 开发模式下整条验证链路对 HTTP 一致放开：discovery 文档地址、discovery 返回的 `jwks_uri` 与令牌 `iss` 精确匹配都按 HTTP issuer 工作；重定向仍一律拒绝。
- serve 在开发模式下使用 HTTP issuer 时，于监听前为每个 HTTP issuer 各记一条 WARN（只写变量名，不写值）。
- `ACCOUNTSVC_MODE=production`（默认）行为不变：仍拒绝非 HTTPS issuer，错误信息与变量名不变；production 下 discovery 返回 HTTP `jwks_uri` 仍判定不可用。
- 文档：`docs/accountsvc.md` 配置表 `ADMIN_ISSUER` / `SERVICE_ISSUER` 条目与 `MODE` 说明补充开发例外。
- 不涉及数据库迁移、公开 Go API、HTTP 契约或错误码；无破坏性变化。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `accountsvc-admin-authentication`：「外部 JWT access token 验证」补充 development 模式接受 HTTP issuer 及 WARN 要求；production 仍只接受 HTTPS。
- `accountsvc-service-authentication`：「服务令牌配置校验」签发方的 HTTPS 要求改为「production 只接受 HTTPS，development 另接受 HTTP 并记 WARN」。
- `accountsvc-runtime`：「生产发送与内省凭据要求」补充 production 下管理员与服务令牌 issuer 只接受 HTTPS，development 的 HTTP 例外须记 WARN。

## Impact

- 代码：`internal/accountsvc/oidcjwt`（issuer 与 discovery/JWKS 地址校验按 `AllowHTTP` 选项放开）、`internal/accountsvc/adminauth`（`Config.AllowHTTP`）、`internal/accountsvc/config.go`（按 `Mode` 设置）、`internal/accountsvc/run.go`（启动 WARN）。
- 文档：`docs/accountsvc.md`。
- 发布：接入方需要新 tag 以从 tag 构建镜像；版本号与 tag 由用户另行授权，本变更不包含发布动作。
