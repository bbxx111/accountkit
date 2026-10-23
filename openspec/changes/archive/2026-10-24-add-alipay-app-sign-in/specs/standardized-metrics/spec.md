# Spec Delta

## ADDED Requirements

### Requirement: 支付宝认证与资料降级观测
ALIPAY 认证 SHALL 使用固定方法值 alipay，一次验证流程 SHALL 记录一次 verify_alipay 依赖逻辑调用，不将换码与资料请求数量当作验证器调用数量。身份验证成功但资料请求超时、错误、解析或验签失败、标识不匹配时 SHALL 以固定 alipay_profile_unavailable 降级场景计数。资料正常缺字段或字段净化后省略 SHALL 不计该降级。指标 SHALL 不携带 AppID、用户标识、授权码、密钥、URL 或任意上游错误文本。

#### Scenario: 身份成功资料失败
- **WHEN** 换码验签成功，资料查询超时，外层上下文有效且领域登录成功
- **THEN** 记录一次 alipay 登录成功、一次 verify_alipay 成功和一次 alipay_profile_unavailable，不能记为凭证无效或登录失败

#### Scenario: 资料成功但领域拒绝
- **WHEN** 提供方验证成功但身份绑定冲突或账号状态导致领域拒绝
- **THEN** 验证器仍记录其成功，认证调用记录最终领域拒绝，不把上游成功等同于登录成功

#### Scenario: 资料正常缺字段
- **WHEN** 资料响应有效且没有昵称或头像
- **THEN** 不计资料不可用降级；正常登录与验证器结果仍按实际记录

#### Scenario: 两个网络请求
- **WHEN** 一次提供方验证执行了换码与资料查询两个 HTTP 请求
- **THEN** 只计一次 verify_alipay 逻辑调用及其整体耗时，不宣称该指标是 HTTP 请求计数

#### Scenario: 身份验签失败
- **WHEN** 换码响应签名错误
- **THEN** 记录身份验证失败且不执行资料请求，不计为资料查询降级

#### Scenario: 调用方取消
- **WHEN** 资料查询因外层上下文取消而终止整个调用
- **THEN** 按现有取消分类记录，不计为允许登录的资料降级
