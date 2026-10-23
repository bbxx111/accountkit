# Spec Delta

## ADDED Requirements

### Requirement: QQ 认证与资料降级观测
QQ 认证 SHALL 使用固定方法值 qq，一次验证流程 SHALL 记录一次 verify_qq 依赖逻辑调用，不将令牌校验与资料请求数量当作验证器调用数量。身份校验成功但资料请求超时、错误、ret 非 0、is_lost 或解析失败时 SHALL 以固定 qq_profile_unavailable 降级场景计数。资料正常缺字段、头像因域名名单被丢弃或字段净化后省略 SHALL 不计该降级。指标 SHALL 不携带 AppID、用户标识、令牌、URL 或任意上游错误文本。

#### Scenario: 身份成功资料失败
- **WHEN** 令牌校验成功，资料查询超时，外层上下文有效且领域登录成功
- **THEN** 记录一次 qq 登录成功、一次 verify_qq 成功和一次 qq_profile_unavailable，不记为凭证无效或登录失败

#### Scenario: 两个网络请求
- **WHEN** 一次提供方验证执行了 /me 与资料查询两个 HTTP 请求
- **THEN** 只计一次 verify_qq 逻辑调用及其整体耗时

#### Scenario: 头像被丢弃
- **WHEN** 资料响应可信，但头像为名单外 HTTP 地址而被丢弃
- **THEN** 不计资料不可用降级

#### Scenario: 令牌校验失败
- **WHEN** /me 返回令牌校验失败
- **THEN** 记录身份验证失败且不执行资料请求，不计为资料查询降级

#### Scenario: 调用方取消
- **WHEN** 资料查询因外层上下文取消而终止整个调用
- **THEN** 按现有取消分类记录，不计为允许登录的资料降级
