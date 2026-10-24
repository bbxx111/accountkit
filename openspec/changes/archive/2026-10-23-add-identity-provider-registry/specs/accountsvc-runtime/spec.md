# Spec Delta

## ADDED Requirements

### Requirement: 服务 OIDC 提供方配置
serve SHALL 从 `ACCOUNTKIT_OIDC_PROVIDERS` 读取逗号分隔的 OIDC 类型名，并为每个类型读取 `ACCOUNTKIT_OIDC_<KIND>_ISSUER`、`_JWKS_URL`、`_CLIENT_IDS` 和可选的 `_NONCE_TTL`、`_NAME_CLAIMS`、`_PICTURE_CLAIMS`（变量未设置为默认映射，设置为空为不读取）。缺失或非法时 SHALL 在监听前非零退出并指出具体变量名，不泄露配置值。显式 Enabled 包含未在 `ACCOUNTKIT_OIDC_PROVIDERS` 中配置的第三方类型时 SHALL 指出 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS`。仅启用 OIDC 类型且 Required 为空时，无 SMTP SHALL 仍可启动。未设置任何 OIDC 变量时 migrate SHALL 不要求它们；已列出 OIDC 类型但其配置不完整或非法时，migrate 与 serve SHALL 同样启动失败并指出变量名，与其他已提供但格式非法的库配置一致。

#### Scenario: 缺少 issuer
- **WHEN** `ACCOUNTKIT_OIDC_PROVIDERS=CORP_SSO` 而未设置 `ACCOUNTKIT_OIDC_CORP_SSO_ISSUER`
- **THEN** serve 启动失败并指出 `ACCOUNTKIT_OIDC_CORP_SSO_ISSUER`

#### Scenario: 仅 OIDC 的服务
- **WHEN** 显式 Enabled 为 CORP_SSO、Required 为 []、该类型配置有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动并完成 CORP_SSO 登录

#### Scenario: migrate 不需要 OIDC 配置
- **WHEN** 执行 migrate 且未设置任何 OIDC 变量
- **THEN** 迁移照常执行，不因 OIDC 配置失败

#### Scenario: migrate 遇到不完整的 OIDC 配置
- **WHEN** 执行 migrate，`ACCOUNTKIT_OIDC_PROVIDERS` 列出 CORP_SSO 但缺少其 issuer
- **THEN** 启动失败并指出 `ACCOUNTKIT_OIDC_CORP_SSO_ISSUER`
