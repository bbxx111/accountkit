# Spec Delta

## ADDED Requirements

### Requirement: OIDC 观测使用有界取值
通过配置 OIDC 提供方完成的认证 SHALL 使用固定的认证方法值 `oidc`，SHALL 不把具体类型名作为指标标签。OIDC 使用缓存签名密钥继续验证的故障分支 SHALL 以独立于 Apple 和 Google 的降级场景计数。审计 SHALL 继续记录具体类型名。

#### Scenario: 多个 OIDC 类型
- **WHEN** 部署配置了 CORP_SSO 与 PARTNER_SSO，两者各完成一次登录
- **THEN** 两次登录都计入方法 `oidc`，指标中不出现这两个类型名；审计分别记录两个类型名

#### Scenario: OIDC 缓存密钥继续验证
- **WHEN** OIDC 验证使用已缓存的签名密钥，且本次刷新失败或被跳过，验证仍然成功
- **THEN** 记录 OIDC 缓存降级场景，不计为 Apple 或 Google 降级
