## Purpose

让业务服务无需逐请求调用内省，即可用公开的 JWKS 在本地校验消费者 access token。本规范规定 ES256 签发、公钥发布、密钥轮换顺序和吊销延迟边界。

## ADDED Requirements

### Requirement: ES256 签发与验签

消费者 access SHALL 只使用 ES256（P-256）签发，header SHALL 包含十进制版本号 `kid` 和 `typ: at+jwt`。签发 SHALL 使用 active 版本的私钥。accountkit 自身验签 SHALL 按 `kid` 选取已配置版本的公钥，并 SHALL 拒绝以下令牌，判定为无效：`alg` 不为 ES256、`typ` 不为 `at+jwt`、`kid` 缺失、非数字或未配置。claims SHALL 保持 `iss`、`aud`、`sub`、`sid`、`scope`、`auth_time`、`jti`、`iat`、`exp`，时效和偏差规则不变。签名密钥配置 SHALL 只接受 P-256 私钥，并 SHALL 拒绝 HMAC 密钥、其他曲线或其他密钥类型。

#### Scenario: 签发格式
- **WHEN** 登录、刷新或重新认证签发 access
- **THEN** 令牌 header 为 `alg=ES256`、`typ=at+jwt`、`kid=<active 版本>`，claims 字段与原格式一致

#### Scenario: 拒绝 HS256 与算法混淆
- **WHEN** 令牌使用 HS256（包括把公钥当作 HMAC 密钥伪造的令牌）、`none`，或缺少 `typ`
- **THEN** Authenticate、严格认证和内省都判定为无效令牌，不泄露具体原因

#### Scenario: 旧配置被拒绝
- **WHEN** 宿主仍以 HS256 随机字节作为签名密钥，或提供非 P-256 曲线的私钥，或 active 版本未配置
- **THEN** 配置校验失败并指出对应配置项，不输出密钥内容

#### Scenario: 多版本验签
- **WHEN** 配置版本1和版本2，active 为版本2
- **THEN** 新签发的令牌使用版本2；版本1签发、仍在有效期内的令牌继续可以验签

### Requirement: JWKS 公钥发布

库 SHALL 提供只读的 JWKS 输出，内容为所有已配置版本的公钥：`kty=EC`、`crv=P-256`、`kid` 为版本号、`alg=ES256`、`use=sig`。JWKS SHALL 不包含私钥参数，也不包含其他实例的密钥。accountsvc SHALL 在 `GET /.well-known/jwks.json` 提供该 JWKS：无需认证，返回 `application/json`，并设置有界的公共缓存期限。JWKS SHALL 只随配置变化，生成时不访问数据库或 Redis。

#### Scenario: 获取公钥
- **WHEN** 业务服务请求 JWKS
- **THEN** 返回所有已配置版本的公钥，可以验证这些版本签发的 access

#### Scenario: 不泄露秘密材料
- **WHEN** 检查 JWKS 响应
- **THEN** 不包含 `d` 或任何私钥字段

#### Scenario: 依赖故障
- **WHEN** PostgreSQL 或 Redis 不可用
- **THEN** JWKS 仍按配置正常返回

### Requirement: 业务服务本地验签规则

接入规范 SHALL 要求本地验签的业务服务满足以下全部要求：

- 只接受 `alg=ES256` 和 `typ=at+jwt`；
- 按 `kid` 从 JWKS 选取公钥；
- 校验 `iss`、`aud` 与 accountsvc 配置完全一致；
- 校验 `exp` 存在，时间偏差不超过 accountkit 配置的偏差；
- 校验 `sub`、`sid` 分别符合 `u_`、`s_` 资源 ID 格式。

JWKS 中没有对应 `kid` 时，业务服务 SHALL 节流刷新 JWKS 后再判断，不得无限信任旧缓存，也不得在刷新失败时放行。本地验签通过 SHALL 只表示签名和时效有效。业务服务仍 SHALL 校验 scope、必要的近期认证和资源归属；`user:bind`、`user:undelete` 不得访问普通业务。

#### Scenario: 吊销延迟边界
- **WHEN** 会话被撤销，业务服务只做本地验签
- **THEN** 该会话已签发的 access 在剩余有效期加验签偏差内仍可能通过；规范明确这一窗口最长为 AccessTokenTTL 加偏差，需要立即失效的接口必须调用内省

#### Scenario: 未知 kid
- **WHEN** 业务服务收到使用新轮换版本签发的 access，而本地 JWKS 缓存中没有该版本
- **THEN** 节流刷新 JWKS 后用新公钥验证；刷新失败时拒绝请求，按依赖不可用处理

### Requirement: 签名密钥轮换顺序

接入与运维文档 SHALL 规定以下轮换顺序：

1. 新增版本并重启所有副本，使新版本出现在 JWKS 中；
2. 等待不短于 JWKS 公共缓存期限和业务服务最大缓存期限的时间；
3. 切换 active 版本并重启所有副本；
4. 至少再等待 AccessTokenTTL 加偏差；
5. 移除旧版本。

同一实例的所有副本 SHALL 使用一致的密钥集合与 active 版本。

#### Scenario: 过早切换 active
- **WHEN** 业务服务尚未缓存新版本时就切换了 active
- **THEN** 业务服务遇到未知 `kid` 时节流刷新 JWKS，不会把新令牌永久判为无效；文档说明按顺序轮换可以避免这次额外刷新

#### Scenario: 移除旧版本
- **WHEN** 旧版本在 active 切换后超过 AccessTokenTTL 加偏差才被移除
- **THEN** 已没有有效 access 依赖旧版本，JWKS 不再包含它
