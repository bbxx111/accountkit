# http-credential-parsing Specification

## Purpose

定义终端用户、管理员和服务调用入口共同遵守的 HTTP Authorization 凭据解析边界，消除重复认证头与格式差异造成的解释歧义，同时保持三个身份体系及各自错误协议和授权规则独立。

## Requirements

### Requirement: 唯一且有界的 HTTP 认证凭据
三类 HTTP 入口在能力已配置且需执行凭据认证时 SHALL 仅接受恰好一个 Authorization 头，不合并重复头或选择其中一个。scheme SHALL 大小写不敏感，使用 ASCII 空格分隔 scheme 与凭据，拒绝空凭据、制表符及多份凭据。Bearer token SHALL 不超过 16 KiB。缺失或格式错误 SHALL 按各入口既有协议返回401，不进入业务处理，不回退另一认证方式。未配置503与可信 Principal 复用的既有优先级 SHALL 保持。

#### Scenario: 重复认证头
- **WHEN** 已配置且需认证的三类入口收到两个 Authorization 头，包括两个相同 Bearer 或 Basic/Bearer 混合
- **THEN** 拒绝请求且不调用凭据验证或业务处理

#### Scenario: 格式和长度
- **WHEN** 请求携带大小写不同的 Bearer scheme、合法单凭据，或空/制表符/超长凭据
- **THEN** 前者正常解析，后者401且不回显凭据

### Requirement: 保留认证上下文与协议边界
公共解析 SHALL 不合并三类主体或授权规则。消费者可信 Principal 复用及严格认证重新验证 SHALL 保持；各入口 SHALL 保持既有 OAuth 或业务错误体、认证挑战和依赖故障分类。

#### Scenario: 严格认证重新校验
- **WHEN** 请求已有消费者 Principal 但进入严格认证
- **THEN** 仍读取并验证本次凭据；普通认证保留原可信上下文复用
