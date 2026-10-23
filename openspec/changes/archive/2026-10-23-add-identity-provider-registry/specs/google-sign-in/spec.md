# Spec Delta

## MODIFIED Requirements

### Requirement: Google 凭证
系统 SHALL 在登录和绑定中接受 kind 为 GOOGLE 的凭证，其 credential 同时包含 id_token 与 nonce。登录、绑定和补全 SHALL 使用同一校验结果。第三方重新认证 SHALL 继续沿用重新登录。

#### Scenario: 已有身份登录
- **WHEN** 客户端提交签名有效、受众允许且 nonce 未使用的 Google ID token，其 sub 已属于活动账号
- **THEN** 登录该账号，不创建第二个用户

#### Scenario: 凭证不完整或并列
- **WHEN** GOOGLE 凭证缺少 id_token 或 nonce，或请求仍使用旧的 `google` 成员
- **THEN** 返回 400，不访问 Google，不创建身份
