## Purpose

规定 accountsvc 内部服务接口（内省与已清理用户拉取）如何认证业务服务调用方：HTTP Basic 与外部 OIDC 提供方签发的客户端凭据 Bearer 访问令牌两种方式、逐接口的客户端授权、认证挑战、提供方故障分类、启动校验与可观测性约束。

## ADDED Requirements

### Requirement: 两种调用认证方式并存

内省与已清理用户拉取 SHALL 各自独立配置可接受的调用认证方式：HTTP Basic 客户端凭据，以及外部提供方按 RFC 6749 §4.4 客户端凭据模式签发的 Bearer 访问令牌。两种方式 SHALL 可以单独或同时启用；同时启用时任一方式认证成功即可。请求 SHALL 恰好携带一个 `Authorization` 头；缺失、多于一个、方案无法识别或方案未在该接口启用时 SHALL 视为未提供有效凭据，且不读取任何一个头的内容。认证 SHALL 先于请求方法、内容类型、请求体和查询参数的处理。仅启用 Basic 的部署 SHALL 与引入 Bearer 之前的行为一致。

#### Scenario: 两种方式同时启用
- **WHEN** 接口同时启用 Basic 与 Bearer，调用方分别使用有效 Basic 凭据和有效 Bearer 令牌
- **THEN** 两个请求都进入接口的正常处理

#### Scenario: 多个 Authorization 头
- **WHEN** 请求同时携带 Basic 与 Bearer 两个 `Authorization` 头，或两个 Bearer 头
- **THEN** 返回 401，视为未提供有效凭据，不附 `error="invalid_token"`

#### Scenario: 未启用的方式
- **WHEN** 接口只启用 Basic，请求携带 Bearer 令牌
- **THEN** 返回 401，challenge 只包含 Basic

### Requirement: Bearer 令牌校验

Bearer 令牌 SHALL 仅在签名算法为 RS256、头部含非空 `kid`、不含 `jku` 与 `x5u`，且签名可由配置签发方经 OIDC discovery 公布的 JWKS 中同 `kid` 的 RSA 密钥验证时通过。声明 SHALL 满足：`iss` 精确等于配置的服务签发方；`aud` 包含配置的服务 audience；`exp` 与 `iat` 均存在，按固定 30 秒时钟偏差判定未过期且已签发；`exp - iat` 不超过 1 小时；`azp` 为非空字符串。任一条件不满足 SHALL 判定令牌无效。令牌超过 16 KiB SHALL 判定无效。服务 SHALL 不读取令牌中的角色或 scope，也不套用管理员的角色继承规则。

#### Scenario: 被拒绝的令牌
- **WHEN** 令牌的 `alg` 为 `none`、`HS256` 或 `ES256`，或头含 `jku`/`x5u`，或缺 `kid`，或 `aud` 不含服务 audience，或已过期超过 30 秒，或 `exp - iat` 超过 1 小时，或缺少 `azp`
- **THEN** 判定令牌无效，返回该接口的 401 令牌无效错误

#### Scenario: 消费者与管理员令牌
- **WHEN** 调用方提交消费者 ES256 access token，或人类管理员的访问令牌（`aud` 为管理员 audience）
- **THEN** 判定令牌无效，返回 401，不返回任何被保护数据

#### Scenario: 时钟偏差内
- **WHEN** 令牌已过 `exp` 但不超过 30 秒
- **THEN** 仍判定有效

### Requirement: 按客户端逐接口授权

通过校验的 Bearer 令牌 SHALL 以 `azp` 作为调用方客户端 ID，且仅当该值在当前接口的允许列表中时获得授权。内省与拉取的允许列表 SHALL 相互独立且不相交，一个客户端 SHALL 不能同时获准调用两个接口。`azp` 不在当前接口列表中（包括属于另一接口的列表）SHALL 返回 403，而不是 401。

#### Scenario: 属于另一接口的客户端
- **WHEN** 有效令牌的 `azp` 只在拉取接口的允许列表中，调用方请求内省
- **THEN** 内省返回 403，不执行任何令牌内省

#### Scenario: audience 误配的管理员令牌
- **WHEN** 人类管理员令牌的 `aud` 被误配为包含服务 audience，其 `azp` 是管理前端的客户端 ID
- **THEN** 返回 403，不授予任何接口的访问

### Requirement: 认证挑战

401 响应 SHALL 为每个已启用的方式各给出一个 `WWW-Authenticate` 头：`Basic realm="<realm>"` 与 `Bearer realm="<realm>"`，内省的 realm 为 `accountsvc-introspection`，拉取为 `accountsvc-purge-feed`。仅当请求恰好携带一个 Bearer `Authorization` 头且令牌被判定无效时，Bearer challenge SHALL 附 `error="invalid_token"`。403 SHALL 只给出带 `error="insufficient_scope"` 的 Bearer challenge。503 SHALL 不带 challenge。所有响应 SHALL 设置 `Cache-Control: no-store`。

#### Scenario: 无凭据
- **WHEN** 两种方式都启用，请求不带 `Authorization`
- **THEN** 返回 401 和两条 challenge，均不含 `error` 属性

#### Scenario: 无效 Bearer
- **WHEN** 两种方式都启用，请求带一个无效的 Bearer 令牌
- **THEN** 返回 401，Basic challenge 不变，Bearer challenge 含 `error="invalid_token"`

### Requirement: 签发方故障分类

签发方 discovery 或 JWKS 不可用 SHALL 判定为依赖不可用并返回 503，SHALL 不判定为 401。服务 SHALL 对签发方的刷新尝试限频：任何一次刷新尝试（无论成功或失败）之后 10 秒内 SHALL 不再发起新的尝试，并发请求 SHALL 共享同一次在途刷新。窗口内需要未缓存的 `kid`、密钥已过缓存期或 discovery 尚未成功时：上一次尝试成功 SHALL 判定令牌无效（401），上一次尝试失败 SHALL 返回 503。缓存的签名密钥 SHALL 在加载 1 小时后失效，刷新失败时 SHALL 不继续使用过期密钥。discovery 返回的 `issuer` 与配置不一致 SHALL 返回 503 并记录错误。认证阶段 SHALL 有不超过 3 秒的等待上限，超时返回 503。

#### Scenario: 随机 kid 不放大请求
- **WHEN** 攻击者在 10 秒内并发发送大量带随机 `kid` 的令牌
- **THEN** 每个窗口内服务至多向签发方发起一次刷新，这些请求返回 401（上次刷新成功）或 503（上次刷新失败）

#### Scenario: 签发方不可用
- **WHEN** 签发方 JWKS 无法获取或超时
- **THEN** Bearer 请求返回 503；同时启用的 Basic 请求不受影响

#### Scenario: 签发方恢复
- **WHEN** 签发方恢复且距上次尝试已超过 10 秒
- **THEN** 下一个 Bearer 请求触发刷新，有效令牌恢复通过

### Requirement: 签发方故障不阻止启动

serve SHALL 在监听前完成服务令牌配置的静态校验，但 SHALL 不因签发方 discovery 或 JWKS 不可达而启动失败，即使接口只启用了 Bearer。启动阶段预加载签名密钥失败时 SHALL 只记录警告。`/readyz` SHALL 不反映签发方状态。管理员认证的启动行为不变。

#### Scenario: 启动时签发方不可达
- **WHEN** serve 启动时服务签发方不可达
- **THEN** 服务正常监听，`/readyz` 返回 200，消费者路由与 Basic 调用可用，Bearer 调用返回 503

### Requirement: 服务令牌配置校验

serve SHALL 读取 `ACCOUNTSVC_SERVICE_TOKEN_ISSUER`、`ACCOUNTSVC_SERVICE_TOKEN_AUDIENCE`、`ACCOUNTSVC_INTROSPECTION_TOKEN_CLIENTS`、`ACCOUNTSVC_PURGE_FEED_TOKEN_CLIENTS`。两个客户端列表为 JSON 字符串数组，元素 SHALL 唯一、非空、不含空白或控制字符且不超过 256 字节；空数组视同未配置。任一列表非空时 SHALL 要求签发方和 audience；签发方或 audience 已设置而两个列表都为空 SHALL 视为配置错误。签发方 SHALL 为不含凭据、查询和片段的绝对 HTTPS URL，SHALL 不等于消费者 JWT issuer，可以等于管理员 issuer。audience SHALL 非空且无首尾空白，SHALL 不等于消费者 JWT audience、已启用的管理员 audience、任一已配置的第三方登录应用或客户端 ID，以及任一允许的 `azp`。两个列表 SHALL 不相交。违反任一规则 SHALL 在监听前非零退出并指出变量名，不回显值。migrate SHALL 不解析这些变量。

#### Scenario: 列表相交
- **WHEN** 同一 `azp` 同时出现在两个客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_TOKEN_CLIENTS`

#### Scenario: audience 冲突
- **WHEN** 服务 audience 等于消费者 audience、管理员 audience、某个第三方应用 ID 或某个允许的 `azp`
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_SERVICE_TOKEN_AUDIENCE`

#### Scenario: 半配置
- **WHEN** 设置了客户端列表但缺少签发方或 audience，或设置了签发方/audience 但两个列表都为空
- **THEN** serve 启动失败并指出缺失或多余的变量名

### Requirement: 调用认证的可观测性

日志与错误响应 SHALL 不包含 Bearer 令牌、Basic 秘密或 `Authorization` 头内容；调用方只以客户端 ID（Basic 用户名或 `azp`）和认证方式（`basic`/`bearer`）标识。若记录认证方式或结果的指标，标签取值 SHALL 限定为固定集合：接口 `introspect`/`purged_users`，方式 `basic`/`bearer`/`none`，结果 `ok`/`unauthenticated`/`forbidden`/`unavailable`。

#### Scenario: 拒绝日志
- **WHEN** 有效令牌因 `azp` 未授权被拒绝，或签发方刷新失败
- **THEN** 日志只出现客户端 ID、接口、签发方地址等非秘密字段，不出现令牌内容
