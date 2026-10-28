## Context

accountsvc 有两个只供内部业务服务调用的接口：

| 接口 | 当前认证 | 错误风格 | 未配置时 |
|---|---|---|---|
| `POST /v1/introspect` | `ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` Basic（必需） | RFC 6749/7662 顶层 `error` | 启动失败 |
| `GET /v1/purgedUsers` | `ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS` Basic（可选） | AIP-193 `apierror` | 503 `PURGE_FEED_NOT_CONFIGURED` |

两份 Basic 列表由 `basicclients.SharedSecret` 保证不共用秘密。管理面已有 `adminauth`：启动时 OIDC discovery 并加载 JWKS（失败则启动失败），只接受 RS256，拒绝 `jku`/`x5u`，要求 `kid`、`exp`、`iat`，固定 30 秒偏差，按 kid 缓存密钥 1 小时，未知 kid 每分钟至多刷新一次，刷新失败归类为 503，并带「super-admin 包含 operator」的角色规则。

需求方（ai-food）用 Keycloak 为业务服务签发客户端凭据令牌。约束见 proposal；本设计回答其五个问题（见 D2、D5、D6、测试矩阵、文档）。

## Goals / Non-Goals

**Goals:**
- 两个接口可选接受客户端凭据 Bearer，与 Basic 并存，逐接口独立授权。
- 拒绝管理员令牌、消费者令牌及其他客户端的 ID token。
- 依赖故障一律 503；IdP 故障不影响消费者认证主路径与 Basic 调用。
- 管理员认证行为与错误格式不变。

**Non-Goals:**
- 不支持 RS256 以外算法、mTLS（RFC 8705）、DPoP、令牌内省到 IdP、opaque token。
- 不做 scope/角色映射，不提供客户端库或令牌获取组件。
- 不新增 Prometheus 指标（见 D8）。
- 不改内省响应、游标格式，不涉及迁移。

## Decisions

### D1 分层：公共 JWT 层 + 只做判定的 serviceauth

```
            +--------------------+
            |  oidcjwt (internal)|  discovery / JWKS 缓存 / RS256 解析
            +---------+----------+  jku,x5u 拒绝 / kid / exp+iat / 30s
                      |
        +-------------+-------------+
        v                           v
  adminauth (行为不变)         serviceauth
  角色、ADMIN_* 错误           Basic(basicclients) | Bearer(oidcjwt) -> azp 授权
                                     |
                       +-------------+-------------+
                       v                           v
                 introspection               purgefeed
               (OAuth 错误映射)          (AIP-193 错误映射)
```

- `oidcjwt` 从 `adminauth` 平移：`Verifier{issuer, audience}`，`Parse(ctx, raw) (jwt.MapClaims, error)`，错误区分 `ErrInvalid` 与 `ErrUnavailable`。初始化两种模式：`Eager`（管理员沿用：启动时 discovery+JWKS，失败即返回错误）与 `Lazy`（服务：见 D6）。adminauth 现有测试作为不回归证据，不改其断言。
- `serviceauth.Authenticator` 每个接口一个实例，持有该接口的 Basic matcher（可空）、`azp` 集合（可空）、共享的 `oidcjwt.Verifier`。`Authenticate(*http.Request) Result`：

  ```go
  type Outcome int // OK, Unauthenticated, Forbidden, Unavailable
  type Result struct {
      Outcome        Outcome
      ClientID       string // Basic 用户名或 azp；仅 OK/Forbidden 时有值
      Method         string // "basic" | "bearer" | "none"
      BearerAttempted bool  // 请求携带了 Bearer 令牌（决定 error="invalid_token"）
  }
  ```
  另提供 `Challenges(r Result, forbidden bool) []string` 生成 `WWW-Authenticate` 值，两个处理器共用，避免两套 challenge 规则漂移。
- `basicclients.Matcher` 新增 `Match(r) (clientID string, ok bool)`；`Authorized` 保留为其包装，现有调用不变。

理由：管理员与服务调用共享最敏感的 JWT 校验细节，但授权模型完全不同（角色继承 vs 显式客户端列表）。只共享解析层，可满足约束 6，且判定与错误格式解耦满足约束 3。

备选：在 adminauth 里加「服务模式」开关——拒绝，角色规则和 `ADMIN_*` 错误码容易误用到服务路径。

### D2 授权：`azp` 允许列表（回答问题 1）

每个接口一份 `azp` 允许列表；令牌通过签名、`iss`、`aud`、时效校验后，`azp` 必须在**当前接口**的列表中，否则 403。两份列表启动时 SHALL 不相交。

选 `azp` 而非 scope/客户端角色：
- **与 Basic 语义一致**：Basic 是「哪些客户端 ID 可调用此接口」，`azp` 列表是同一模型；不相交校验在 accountsvc 本地可验证，直接对应 `SharedSecret`（约束 1）。scope/角色由 IdP 管理员授予，accountsvc 启动时无法证明「没有一个客户端同时拿到两个 scope」。
- **授权决策留在 accountsvc 配置**：IdP 只证明「你是谁」，不决定「你能调用什么」；Keycloak 的 default client scope、realm 角色继承等容易让权限意外扩散。
- **天然排除人类管理员令牌**：管理员经前端客户端登录，其令牌 `azp` 是前端客户端 ID，不在列表中——即使 audience mapper 配错让 `aud` 含服务 audience，仍为 403。

代价：`azp` 来自 OIDC Core，RFC 9068（JWT access token profile）使用 `client_id`。Keycloak 的访问令牌同时带 `azp`，首版只认 `azp`；若将来接入只发 `client_id` 的 IdP，再增加可选的声明名配置（见 Review Decisions 1）。

`azp` 必须为非空字符串；缺失、非字符串或为空 → 401（令牌不满足服务令牌格式），不是 403。

### D3 令牌校验规则

在 `oidcjwt` 公共规则（RS256、拒绝 `jku`/`x5u`、必需 `kid`、`exp`+`iat` 必需、`iss` 精确匹配、`aud` 含配置值、30 秒偏差）之上，服务令牌另要求：
- `azp` 见 D2。
- `exp - iat` ≤ 1 小时（固定）。客户端凭据令牌应短期，超长令牌视为 IdP 配置错误，返回 401。Keycloak 默认 5 分钟。
- 不强制 `typ` 头（Keycloak 为 `JWT`，非 RFC 9068 的 `at+jwt`），ID token 由 D4 的 audience 约束排除。
- 不读取 `sub`、角色或 scope；日志与 Result 只用 `azp`。

令牌长度上限沿用 16 KiB。

实施偏离（2026-10-28）：adminauth 现有行为只在 `iat` 存在时校验它，并不要求必须有。为保持管理员行为不变，`oidcjwt` 公共层只要求 `exp`、存在时校验 `iat`；「`iat` 必需」与 `exp - iat` 上限一起放在 serviceauth 中执行。服务令牌的外部行为与规范一致。

### D4 audience 与 issuer 隔离（约束 2）

serve 启动时，配置任一 Bearer 客户端列表即要求：
- `ACCOUNTSVC_SERVICE_ISSUER`：绝对 HTTPS URL，规则同 `adminauth.Config.Validate`；可以等于 `ACCOUNTSVC_ADMIN_ISSUER`；SHALL 不等于 `ACCOUNTSVC_JWT_ISSUER`。
- `ACCOUNTSVC_SERVICE_AUDIENCE`：非空、无首尾空白；SHALL 不等于：
  - `ACCOUNTSVC_JWT_AUDIENCE`（消费者）；
  - `ACCOUNTSVC_ADMIN_AUDIENCE`（管理员启用时）；
  - 任一第三方应用/客户端 ID：微信与小程序 AppID、Apple Bundle ID、Google Client ID、Facebook App ID、QQ、微博、支付宝 AppID、OIDC 提供方 client ID（比现有管理员检查更全，管理员检查本次不改）；
  - 任一允许的 `azp`——否则该客户端自身的 ID token（`aud`=`azp`=客户端 ID）可冒充访问令牌。

消费者令牌因 ES256 + 不同 `iss` 已被拒绝；人类管理员令牌因 `aud` 不同被拒绝，`aud` 误含服务 audience 时再由 `azp` 拒绝（D2）。

反向：配置了 issuer/audience 但两个 token 列表都为空，或列表非空而缺 issuer/audience，均 SHALL 启动失败（避免半配置）。

### D5 错误映射（回答问题 2）

| 判定 | 内省（OAuth） | 拉取（AIP-193） |
|---|---|---|
| 无 Authorization / 头多于一个 / 方案不支持 / Basic 错误 | 401 `invalid_client` | 401 `INVALID_CLIENT`（不变） |
| 携带 Bearer 但无效（签名、iss、aud、过期、算法、jku、azp 缺失、寿命过长） | 401 `invalid_token` | 401 `TOKEN_INVALID` |
| Bearer 有效但 `azp` 不在本接口列表 | 403 `insufficient_scope` | 403 `CLIENT_NOT_AUTHORIZED` |
| 取不到签名密钥（discovery/JWKS 失败、刷新冷却中且上次失败） | 503 `temporarily_unavailable` | 503 `DEPENDENCY_UNAVAILABLE` + `Retry-After: 1` |
| 两种方式都未配置（先于认证判断） | 503 `introspection_not_configured`（见 D9 偏离） | 503 `PURGE_FEED_NOT_CONFIGURED`（不变） |

拉取 401 选 `TOKEN_INVALID`：与 R3 契约一致，调用方处理是「重新向 IdP 换令牌后重试一次」；保留 `INVALID_CLIENT` 给 Basic/缺失凭据，调用方可据此区分「令牌问题」与「凭据配置问题」。

拉取 403 不用 `INSUFFICIENT_SCOPE`：R3 契约中该码的客户端处理是「刷新一次」，对 `azp` 授权无效，换令牌不会改变结果；`CLIENT_NOT_AUTHORIZED` 明确表示「此客户端未被授权调用该接口，需改配置」，不应重试。内省 403 仍用 RFC 6750 标准的 `insufficient_scope`，是 OAuth 风格内可用的最接近值。

Bearer 未启用时携带 Bearer → 视为方案不支持（401 `invalid_client` / `INVALID_CLIENT`），challenge 只给 Basic，不带 `error`。

所有响应保持 `Cache-Control: no-store`。认证先于方法、Content-Type 和请求体校验（与现在一致）。

### D6 challenge 规则（约束 4）

每个启用的方式一个独立的 `WWW-Authenticate` 头行（RFC 9110 允许多值，分行更易解析）：

```
WWW-Authenticate: Basic realm="accountsvc-introspection"
WWW-Authenticate: Bearer realm="accountsvc-introspection", error="invalid_token"
```

- realm 与现有一致：`accountsvc-introspection`、`accountsvc-purge-feed`；Bearer 用相同 realm。
- `error="invalid_token"` 仅当 `BearerAttempted`（请求恰有一个 Authorization 头且方案为 Bearer）且判定为 401。
- 403 只返回 `Bearer realm=..., error="insufficient_scope"`。
- 503 不带 challenge。
- 仍要求恰好一个 Authorization 头；多个头 → 401，不读取任何一个，`BearerAttempted=false`。

### D7 IdP 不可用：启动与运行期（回答问题 3）

**启动**：配置校验（D4）失败 SHALL 在监听前退出。随后服务 verifier 以 `Lazy` 模式构造，在启动阶段**尝试**一次 discovery+JWKS（受启动超时约束，单次上限 10 秒），失败只记 WARN（含 issuer，不含响应体），**不阻止启动**——即使只配置了 Bearer。理由：accountsvc 的主职责是消费者登录与刷新，不应因服务调用方的 IdP 故障整体不可用；Bearer 请求按运行期规则返回 503，调用方可回退 Basic（若配置）。`/readyz` 不纳入 IdP 状态，避免 IdP 故障导致编排系统摘除整个服务。

管理员认证保持 `Eager`：管理面启用时 IdP 不可达仍启动失败（现有行为，不在本次范围）。若服务 issuer 与管理员 issuer 相同且管理面启用，启动实际仍依赖该 IdP，文档写明。

**运行期**：
冷却语义与 adminauth 的 `key()` 完全一致，只把间隔参数化（管理员 1 分钟不变，服务 10 秒）：

- **任何一次**对 IdP 的刷新尝试（discovery 和/或 JWKS），**无论成功还是失败**，都记录尝试时间；此后 10 秒内不再发起新的尝试。并发请求共享同一次在途刷新（单飞），刷新由 verifier 自己的有界上下文执行，不随某个调用方取消。
- 窗口内需要密钥（未缓存的 kid、密钥已过期、discovery 尚未成功）时：上次尝试成功 → 401（kid 确实未知）；上次尝试失败 → 503。
- 因此带随机 kid 的令牌每 10 秒最多让每个 verifier 访问 IdP 一次，无法把每个请求放大为一次 JWKS 拉取。
- discovery 未成功前，Bearer 请求在窗口外触发 discovery+JWKS；失败按上条返回 503。
- 已加载的密钥在 1 小时内有效；过期后刷新失败 → 503（不继续使用过期密钥）。
- 已知影响：IdP 轮换签名密钥时，若新 kid 的首个令牌恰好落在上次成功刷新后的 10 秒窗口内，会短暂返回 401；调用方换令牌重试仍可能在同一窗口内失败。应对方式是 IdP 先把新密钥发布进 JWKS（Keycloak：先以 passive 状态加入或先进入 JWKS），确认已被拉取后再切为 active，文档写明。
- discovery 返回的 `issuer` 与配置不一致：视为依赖配置错误，503 并记 ERROR，不是 401。
- 服务 issuer 与管理员 issuer 相同时两个 verifier 各自缓存，互不影响；故障分类各自独立。
- 认证阶段单独设 3 秒上限（现有 3 秒超时只覆盖认证之后的查询）；等待密钥超时 → 503。

实施偏离（2026-10-28，独立审查发现）：启动阶段同步预加载在签发方**挂起**（丢包、DNS 黑洞）时会耗尽 `STARTUP_TIMEOUT` 预算，使 serve 启动失败，违反「签发方故障不阻止启动」。改为监听就绪后在后台预加载（上限 10 秒），关停时取消并等待；失败记 WARN，issuer 不一致记 ERROR。另为服务 verifier 设单次刷新超时 5 秒（小于 10 秒冷却），使挂起的签发方在冷却窗口内即判失败，后续 Bearer 请求立即 503；管理员保持原 10 秒超时与 1 分钟冷却。

### D8 可观测性（约束 7）

- 不记录令牌、Basic 秘密或 Authorization 头；日志只出现 `client_id`（Basic 用户名或 `azp`）与 `auth_method`（`basic`/`bearer`）。
- 不为每个成功请求写日志（与现有两个处理器一致）。记录：IdP discovery/JWKS 失败（WARN，按冷却周期自然限频，字段 `issuer`）、discovery issuer 不一致（ERROR）、403 拒绝（INFO，`client_id`、`endpoint`）以便排查配置。
- 本次不新增 Prometheus 指标：accountsvc 目前没有自有 HTTP 指标，指标经库的 `observability.Observer` 暴露，增加服务私有指标需要额外接线。若后续增加，标签固定为 `endpoint∈{introspect,purged_users}`、`method∈{basic,bearer,none}`、`result∈{ok,unauthenticated,forbidden,unavailable}`。

### D9 配置

| 变量 | 格式 | 说明 |
|---|---|---|
| `ACCOUNTSVC_SERVICE_ISSUER` | HTTPS URL | 两个接口共用 |
| `ACCOUNTSVC_SERVICE_AUDIENCE` | 字符串 | 两个接口共用；隔离规则见 D4 |
| `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS` | JSON 字符串数组 | 允许调用内省的 `azp` |
| `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS` | JSON 字符串数组 | 允许调用拉取的 `azp` |

- 数组元素唯一、非空、无空白/控制字符、长度 ≤ 256；空数组视同未配置。
- 两个数组 SHALL 不相交；任一元素 SHALL 不等于服务 audience。
- 内省：`ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS` 与 `ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS` 均可选，规则与拉取相同（见下方偏离）。
- 两个接口共用一个 audience，靠 `azp` 区分接口；不为每个接口单设 audience，减少 IdP mapper 配置。
- 错误消息只指出变量名，不回显值（与现有一致）。migrate 不解析这些变量。

实施偏离（2026-10-28，用户决定）：变量名按认证方式成对命名。设计原稿的 `SERVICE_TOKEN_ISSUER` / `SERVICE_TOKEN_AUDIENCE` 改为 `SERVICE_ISSUER` / `SERVICE_AUDIENCE`（与 `ADMIN_ISSUER` / `ADMIN_AUDIENCE` 对称）；`*_TOKEN_CLIENTS` 改为 `*_BEARER_CLIENTS`；既有 Basic 变量 `INTROSPECTION_CLIENTS` / `PURGE_FEED_CLIENTS` 同步改为 `*_BASIC_CLIENTS`，格式不变、不读取旧名。尚无产品部署，不提供旧名检测或迁移。

实施偏离（2026-10-28，用户决定）：内省调用认证改为与拉取相同的可选规则，取代原稿「至少一个非空、否则启动失败」。两种方式都未配置时 serve（含 production）照常启动，`/v1/introspect` 先于认证返回 503 `introspection_not_configured`；错误体沿用该接口的 OAuth 顶层 `error`（小写下划线，与 RFC 6749 扩展错误码风格一致），不改用 AIP-193，避免同一接口出现两种错误格式。代价是漏配内省不再在启动时暴露，而是表现为依赖内省的关键接口全部失败，部署检查须覆盖该项。

## Risks / Trade-offs

- [IdP 宕机期间 Bearer-only 调用方不可用] → 返回 503 可重试；建议过渡期保留 Basic 作为回退；文档说明。
- [IdP 停用客户端到令牌失效有延迟（最长为令牌剩余寿命，≤1 小时）] → 需要立即撤销时从 `azp` 列表移除并滚动重启；文档写明。
- [服务 audience 被 IdP 管理员误加到人类客户端 mapper] → `azp` 列表兜底（D2）。
- [两个 verifier 对同一 IdP 重复拉取 JWKS] → 量很小（每小时一次），换取故障隔离，接受。
- [抽取公共层改动管理员认证代码] → 平移不改语义，以现有 adminauth 测试全部不改通过为准。
- [`azp` 非 RFC 9068 标准声明] → 评审确认首版不支持，需要时加声明名配置（Review Decisions 1）。

## Migration Plan

1. 部署新版本，只配 Basic：行为与现在完全相同。
2. IdP 建客户端与 audience mapper；accountsvc 增加 issuer/audience 与 token 列表，滚动重启；调用方切换到 Bearer。
3. 观察稳定后移除对应 Basic 列表（移除前确认 Bearer 已生效；两种都移除时内省返回 503）。
回退：调用方切回 Basic；或移除 token 配置并重启。无数据迁移。

## 测试矩阵（回答问题 4）

单元测试使用本地 HTTPS 测试 IdP（discovery + JWKS，可切换为失败）与 RSA/ECDSA 测试密钥，两个接口各跑一遍，并断言状态码、错误码/`error`、`WWW-Authenticate` 行和 `Cache-Control`：

| # | 场景 | 内省期望 | 拉取期望 |
|---|---|---|---|
| 1 | 有效令牌，`azp` 在本接口列表 | 进入正常处理 | 200 |
| 2 | `aud` 不含服务 audience | 401 `invalid_token` | 401 `TOKEN_INVALID` |
| 3 | 人类管理员令牌（同 issuer，`aud`=管理员 audience） | 401 | 401 |
| 3b | 管理员令牌 `aud` 误含服务 audience，`azp`=管理前端 | 403 | 403 `CLIENT_NOT_AUTHORIZED` |
| 4 | 消费者 ES256 令牌 | 401 | 401 |
| 5 | `azp` 不在任何列表 | 403 `insufficient_scope` | 403 |
| 6 | `azp` 属于另一接口的列表 | 403 | 403 |
| 7 | `azp` 缺失/为空/非字符串 | 401 | 401 |
| 8 | 已过期（超出 30 秒偏差）；偏差内仍通过 | 401 / 通过 | 同左 |
| 9 | `exp - iat` > 1 小时 | 401 | 401 |
| 10 | `alg=none`、`HS256`（用公钥字节作 HMAC 密钥）、`ES256` | 401 | 401 |
| 11 | 头含 `jku` 或 `x5u` | 401 | 401 |
| 12 | 缺 `kid`；未知 `kid` 刷新后仍无 | 401 | 401 |
| 13 | JWKS/discovery 不可用（启动后首次、冷却期内、密钥过期后） | 503 `temporarily_unavailable` | 503 `DEPENDENCY_UNAVAILABLE` + `Retry-After: 1` |
| 14 | IdP 恢复后冷却结束 | 恢复正常 | 恢复正常 |
| 14b | 随机 kid 令牌在 10 秒窗口内连续并发请求 | 401；测试 IdP 收到的 JWKS 请求 ≤1 次/窗口 | 同左 |
| 14c | 上次刷新成功后窗口内出现新 kid；窗口过后同一令牌 | 401；随后通过 | 同左 |
| 15 | 两个 Authorization 头（Basic+Bearer 或两个 Bearer） | 401 `invalid_client`，无 `error` 属性 | 401 `INVALID_CLIENT` |
| 16 | 无 Authorization，两种都启用 | 401，两条 challenge，无 `error` | 同左 |
| 17 | 仅 Basic 启用时携带 Bearer | 401 `invalid_client`，仅 Basic challenge | 同左 |
| 18 | Basic 与 Bearer 都启用时，有效 Basic 仍通过 | 通过 | 200 |
| 19 | discovery `issuer` 与配置不一致 | 503 | 503 |
| 20 | 依赖失败不被归为 401（IdP 超时） | 503 | 503 |

配置测试（serve 启动失败、只指出变量名、不回显值；migrate 不解析）：两份 `azp` 列表相交；audience 等于消费者/管理员 audience、任一第三方 ID 或任一 `azp`；issuer 非 HTTPS 或等于消费者 issuer；有列表缺 issuer/audience，或有 issuer/audience 无列表；数组重复/空元素/非数组；内省两种方式都未配置。IdP 不可达时 serve 仍启动且 `/readyz` 为 200。

回归：adminauth 现有测试不改断言全部通过；仅 Basic 配置下两个接口的现有测试不改通过。

进程级集成（Linux，真实二进制）：本地 HTTPS 测试 IdP 下两个接口 Bearer 端到端各一条；IdP 关闭时启动成功且 Bearer 返回 503、Basic 正常。日志断言不含令牌。

## 文档（回答问题 5）

- `docs/accountsvc.md`：「业务服务错误与配置契约」增加服务调用认证小节（两种方式、D5 错误表、D6 challenge、调用方处理建议：`TOKEN_INVALID` 换令牌重试一次，`CLIENT_NOT_AUTHORIZED` 不重试，503 退避）；配置表增加 D9 变量；IdP 故障与撤销延迟说明；Keycloak 配置要点作为示例而非依赖：仅开服务账号的机密客户端；服务 audience mapper 只勾选 "Add to access token"，不勾选 "Add to ID token"（与 D4「aud 不得等于任何 azp」互为部署侧与服务侧防线）；访问令牌寿命建议 ≤5 分钟；签名密钥轮换先发布新密钥进 JWKS（passive）再切 active，以免 10 秒刷新窗口内短暂 401（D7）。
- `docs/gateway-integration.md`：内部入口须透传 `Authorization: Bearer`，不得在网关剥离或替换；网关不得用自身令牌调用这两个接口。
- `deploy/accountsvc/.env.example`：新增变量的空模板。
- `docs/compatibility.md`：内省「必须配置 Basic」放宽为「至少一种」的说明。
- `examples/remoteauth`：README/注释说明调用内省可使用 Bearer，示例代码保持 Basic。

## Review Decisions

2026-10-27 经需求方与用户评审确认：

1. 首版只认 `azp`，不支持 RFC 9068 `client_id`；以后需要时再加声明名配置。
2. `exp - iat` 上限固定 1 小时，不提供配置项。
3. 拉取接口 403 使用 `CLIENT_NOT_AUTHORIZED`。
4. 服务刷新冷却 10 秒，语义同 adminauth：任何尝试（成功或失败）后窗口内不再拉取（D7）。
5. 评审补充：D7 写明冷却适用范围并加随机 kid 放大测试；文档写明 IdP 轮换签名密钥的影响与先发布后启用；Keycloak 示例要求 audience mapper 只加到访问令牌。
