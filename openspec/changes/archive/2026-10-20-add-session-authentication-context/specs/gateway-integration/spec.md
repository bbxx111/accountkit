## MODIFIED Requirements

### Requirement: 入口策略与身份职责

项目 SHALL 提供与具体网关产品无关的接入说明，区分消费者、管理员、内省和探针入口。公网网关 SHALL 使用明确的路由暴露策略，不因内省位于 `/v1` 下而直接透传全部 `/v1/*`。部署方 SHALL 限制服务端口的直接访问来源。网关认证 SHALL 不替代服务中的消费者/管理员身份验证、内省 Basic 或角色与资源归属检查。

消费者与管理端的账号路由及引用 SHALL 沿用 users；两个新增消费者端点 /users:sendSignInCompletionCode 和 /users:completeSignIn SHALL 分别列入允许清单，不以 /v1/* 通配开放。两者使用请求体中的 sign_in_completion_token；已有 Bearer 路由的身份要求 SHALL 保留。此前未发布的 accounts 路径 SHALL 不被网关隐式映射或重定向为 users 接口。

当前会话查询 SHALL 作为明确的消费者GET路径 /users/me/sessions/current 纳入允许清单，实际前缀由宿主决定；该查询仍要求消费者Bearer与允许scope，不因与内省同处/v1而开放内省。

#### Scenario: 内省与探针不经公网暴露
- **WHEN** 部署方执行网关接入验收
- **THEN** 检查公网访问内省/探针被拦截且不进入后端、授权内部调用可以访问、非授权来源不能通过直连端口绕过策略

#### Scenario: 开放新增消费者端点
- **WHEN** 部署方在既有 users 网关允许清单中增加两个登录补全端点
- **THEN** 只有明确的消费者新路由被开放，内省、管理和探针边界不变；服务自身继续校验票据及证明

#### Scenario: 开放当前会话查询
- **WHEN** 网关增加当前会话详情路由
- **THEN** 仅新消费者GET路径被开放，内部内省、管理与探针访问边界保持

### Requirement: 传输代理与凭据透传

接入说明 SHALL 区分服务自身 HTTPS、网关终止后 HTTPS 回源、受控网关/网格提供外部 TLS 的 HTTP 应用连接。调用方 SHALL 校验 TLS 证书，外部终止模式 SHALL 配合可信网络隔离；不得仅靠客户端 X-Forwarded-Proto 声称链路安全。代理 SHALL 正确保留认证头、设备头、内容类型、请求ID及认证挑战、Retry-After、no-store等响应语义，不默认自动重试认证和写请求，不记录凭据正文。来源IP处理 SHALL 遵循配置的可信代理链。

客户端描述 SHALL 通过可选客户端头或可用的标准UA/Client Hints传入，代理/CORS允许清单按实际使用配置。新头缺失和自动解析失败 SHALL 不阻断认证；元数据不能替代可信ClientIP解析。BFF原客户端信息 SHALL 通过明确适配传递，不能无条件信任任意转发头或将BFF自身UA当作终端设备。

#### Scenario: 伪造来源和代理错误
- **WHEN** 验收使用伪造转发头、服务超时或含凭据的请求
- **THEN** 不可信来源不能伪造计数/审计IP，代理不自行重复发码或写操作，日志和追踪数据不出现完整凭据，认证及缓存响应语义保持

#### Scenario: 旧客户端和可选头透传
- **WHEN** 旧客户端只发送原设备头，或新客户端通过网关发送可选客户端描述
- **THEN** 旧流程可继续认证，新合法元数据按允许清单透传；不改变token请求体和原认证挑战/缓存语义
