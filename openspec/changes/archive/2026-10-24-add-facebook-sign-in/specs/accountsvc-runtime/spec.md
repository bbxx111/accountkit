# Spec Delta

## ADDED Requirements

### Requirement: 服务 Facebook 配置
serve SHALL 读取 `ACCOUNTKIT_FACEBOOK_APP_ID`、`_APP_SECRET` 及可选的 `_GRAPH_API_VERSION`、`_NONCE_TTL`、`_GRAPH_API_BASE_URL`、`_JWKS_URL`。App ID 非空而 Secret 缺失或其他项非法时 SHALL 在监听前非零退出并指出变量名，不泄露配置值。显式 Enabled 包含 FACEBOOK 而 App ID 为空时 SHALL 指出 `ACCOUNTKIT_FACEBOOK_APP_ID`。仅启用 FACEBOOK 且 Required 为空时，无 SMTP SHALL 仍可启动。未设置 Facebook 变量时 migrate SHALL 不要求它们。服务 SHALL 不提供数据删除回调路由。

#### Scenario: 缺少 App Secret
- **WHEN** 设置了 `ACCOUNTKIT_FACEBOOK_APP_ID` 而未设置 `ACCOUNTKIT_FACEBOOK_APP_SECRET`
- **THEN** serve 启动失败并指出 `ACCOUNTKIT_FACEBOOK_APP_SECRET`，错误不含其他配置值

#### Scenario: 启用 Facebook 但未配置 App
- **WHEN** 显式 Enabled 包含 FACEBOOK 且 `ACCOUNTKIT_FACEBOOK_APP_ID` 为空
- **THEN** 启动失败并指出 `ACCOUNTKIT_FACEBOOK_APP_ID`

#### Scenario: 仅 Facebook 的服务
- **WHEN** 显式 Enabled 为 FACEBOOK、Required 为 []、App ID 与 Secret 有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动并完成 Facebook 登录，不要求邮件发送

#### Scenario: migrate 不需要 Facebook 配置
- **WHEN** 执行 migrate 且未设置任何 Facebook 变量
- **THEN** 迁移照常执行，不因 Facebook 配置失败
