# Spec Delta

## ADDED Requirements

### Requirement: Facebook 观测取值
通过 FACEBOOK 完成的认证 SHALL 使用固定的认证方法值 `facebook`，两种凭证路径 SHALL 不作为额外标签区分。Limited 路径使用缓存签名密钥继续验证的故障分支 SHALL 以独立于 Apple、Google 与 OIDC 的降级场景计数。Classic 路径的一次校验 SHALL 记为一次 IdP 验证器逻辑调用，SHALL 不按其实际发出的 Graph 请求数计数。数据删除处理 SHALL 不新增指标。

#### Scenario: 两种凭证登录
- **WHEN** 一次 Limited 登录与一次 Classic 登录各自成功
- **THEN** 两次都计入方法 `facebook`，指标中不出现凭证路径或 Facebook 用户 id

#### Scenario: Facebook 缓存密钥继续验证
- **WHEN** Limited 验证使用已缓存的签名密钥，且本次刷新失败或被跳过，验证仍然成功
- **THEN** 记录 Facebook 缓存降级场景，不计为 Apple、Google 或 OIDC 降级

#### Scenario: Classic 校验计数
- **WHEN** 一次 Classic 校验依次调用了两个 Graph 接口并成功
- **THEN** 只记录一次 IdP 验证器调用
