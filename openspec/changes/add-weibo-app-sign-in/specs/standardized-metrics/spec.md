# Spec Delta

## ADDED Requirements

### Requirement: 微博认证与资料降级观测
微博认证 SHALL 使用固定方法值 weibo，一次验证流程 SHALL 记录一次 verify_weibo 依赖逻辑调用，不将令牌校验与资料请求数量当作验证器调用数量。身份校验成功但资料请求超时、限流、上游错误、用户不符或解析失败时 SHALL 以固定 weibo_profile_unavailable 降级场景计数。资料正常缺字段、HTTP 头像被丢弃或字段净化后省略 SHALL 不计该降级。指标 SHALL 不携带 AppKey、uid、令牌、URL 或任意上游错误文本。

#### Scenario: 身份成功资料限流
- **WHEN** 令牌校验成功，资料查询返回频率限制错误，外层上下文有效且领域登录成功
- **THEN** 记录一次 weibo 登录成功、一次 verify_weibo 成功和一次 weibo_profile_unavailable，不记为凭证无效或登录失败

#### Scenario: 两个网络请求
- **WHEN** 一次提供方验证执行了 get_token_info 与资料查询两个 HTTP 请求
- **THEN** 只计一次 verify_weibo 逻辑调用及其整体耗时

#### Scenario: HTTP 头像被丢弃
- **WHEN** 资料响应可信，但选中的头像为 HTTP 地址而被丢弃
- **THEN** 不计资料不可用降级

#### Scenario: 令牌校验失败
- **WHEN** get_token_info 返回令牌失效错误
- **THEN** 记录身份验证失败且不执行资料请求，不计为资料查询降级

#### Scenario: 调用方取消
- **WHEN** 资料查询因外层上下文取消而终止整个调用
- **THEN** 按现有取消分类记录，不计为允许登录的资料降级
