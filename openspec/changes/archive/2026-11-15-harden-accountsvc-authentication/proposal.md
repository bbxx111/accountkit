# Proposal

## Why

三类调用方已具有独立信任边界，但凭据头解析不一致，内部路由清单不完整，管理员签发方故障会阻止整个服务启动。服务 Bearer 只按注册权限授权，不能表达单枚令牌的最小权限，也缺少明确的机器主体绑定。

## What Changes

- 统一三类入口的 Authorization 解析并拒绝重复头，保持各自协议错误和 Principal。
- 集中登记路由类别及服务权限，不变更 URL；补充网关身份解析边界。
- 管理员认证改为静态装配、后台预加载和请求期有界刷新，远端故障仅影响管理员请求。
- **BREAKING**：服务 Bearer 强制按注册权限与 scope 取交集，无旧授权模式或缺省放行。
- **BREAKING**：服务 Bearer 绑定显式配置的机器主体；外部 JWT 使用显式现有或 RFC 9068 profile，禁止声明回退猜测。

## Capabilities

### New Capabilities

- `http-credential-parsing`: 三类入口的唯一 Authorization 头与凭据格式边界。

### Modified Capabilities

- `accountsvc-admin-authentication`: 认证装配、提供方故障隔离及显式令牌 profile。
- `accountsvc-service-authentication`: scope 交集、服务主体绑定与令牌 profile。
- `accountsvc-runtime`: 路由类别清单和管理员后台预加载生命周期。
- `gateway-integration`: 身份解析内部入口及部署核验。
- `consumer-token-introspection`: 强制服务 scope 与机器身份后继续保持被查询用户 token 语义。

## Impact

涉及 httpapi/authn、internal/accountsvc、内部凭据解析包、相关测试、部署模板和接入文档；不修改消费者 JWT 格式、数据库或冻结迁移。按用户要求先完成解析、路由和故障隔离，再实施其他认证强化；不推送、不发布、不归档。
