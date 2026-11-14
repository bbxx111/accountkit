# accountsvc-service-authentication Specification

## Purpose
规定 accountsvc 内部服务接口（内省与已清理用户拉取）如何认证业务服务调用方：HTTP Basic 与外部 OIDC 提供方签发的客户端凭据 Bearer 访问令牌两种方式、逐接口的客户端授权、认证挑战、提供方故障分类、启动校验与可观测性约束。

## Requirements

### Requirement: 两种调用认证方式并存

内省、已清理用户拉取与身份解析 SHALL 共用统一客户端注册表，按客户端启用 HTTP Basic 凭据及外部提供方按 RFC 6749 §4.4 客户端凭据模式签发的 Bearer 访问令牌。两种方式 SHALL 可以单独或同时启用；同时启用时任一方式认证成功且具有端点权限即可。请求 SHALL 恰好携带一个 `Authorization` 头；缺失、多于一个或方案无法识别 SHALL 视为未提供有效凭据，且不读取任何一个头的内容。客户端未启用请求方式 SHALL 返回 401。认证与权限检查 SHALL 先于请求方法、内容类型、请求体和查询参数的处理；SHALL 不在认证失败后切换方式。仅启用 Basic SHALL 不要求 OIDC 依赖，仍经过相同注册表和权限路径。

#### Scenario: 两种方式同时启用
- **WHEN** 客户端同时启用 Basic 与 Bearer 并持有所请求端点权限，调用方分别使用有效 Basic 凭据和有效 Bearer 令牌
- **THEN** 两个请求都进入接口的正常处理

#### Scenario: 多个 Authorization 头
- **WHEN** 请求同时携带 Basic 与 Bearer 两个 `Authorization` 头，或两个 Bearer 头
- **THEN** 返回 401，视为未提供有效凭据，不附 `error="invalid_token"`

#### Scenario: 未启用的方式
- **WHEN** 注册表只启用 Basic，请求携带 Bearer 令牌
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

通过校验的 Bearer 令牌 SHALL 以 `azp` 作为调用方客户端 ID，Basic SHALL 以通过秘密验证的用户名识别同一注册表中的客户端。端点 SHALL 分别要求 `tokens.introspect`、`purged_users.read` 或 `identities.resolve`，同一客户端 SHALL 可以同时持有三项权限，两种认证方式共享权限集合。有效 Bearer 的 `azp` 未登记 SHALL 返回 403；已认证客户端缺少当前端点权限 SHALL 返回 403，而不是 401。JWT scope、角色与请求体 client_id SHALL 不扩大注册表权限。

#### Scenario: 属于另一接口的客户端
- **WHEN** 有效令牌的 `azp` 对应客户端只有 purged_users.read，且内省能力已配置，调用方请求内省
- **THEN** 内省返回 403，不执行任何令牌内省

#### Scenario: audience 误配的管理员令牌
- **WHEN** 人类管理员令牌的 `aud` 被误配为包含服务 audience，其 `azp` 是未登记为服务客户端的管理前端 ID，且请求的能力已配置
- **THEN** 返回 403，不授予任何接口的访问

#### Scenario: 身份解析权限隔离
- **WHEN** 身份解析已配置，已认证客户端仅持有 tokens.introspect 或 purged_users.read，却请求身份解析
- **THEN** 返回 403，不解析请求体或访问身份数据；持有 identities.resolve 不自动授予其他两项权限

### Requirement: 认证挑战

401 响应 SHALL 按整个注册表启用方法的并集，为每个方式各给出一个 `WWW-Authenticate` 头：`Basic realm="<realm>"` 与 `Bearer realm="<realm>"`，内省的 realm 为 `accountsvc-introspection`，拉取为 `accountsvc-purge-feed`，身份解析为 `accountsvc-identity-resolution`；SHALL 不按未验证用户名或 azp 改变挑战。仅当请求恰好携带一个 Bearer `Authorization` 头且令牌被判定无效时，Bearer challenge SHALL 附 `error="invalid_token"`；仅客户端方式未启用 SHALL 不附该属性。有效 Basic 缺权限的 403 SHALL 不带 challenge，Bearer 的 403 SHALL 只给出带 `error="insufficient_scope"` 的 Bearer challenge。503 SHALL 不带 challenge。所有响应 SHALL 设置 `Cache-Control: no-store`。

#### Scenario: 无凭据
- **WHEN** 注册表两种方式都启用，请求的能力已配置且不带 `Authorization`
- **THEN** 返回 401 和两条 challenge，均不含 `error` 属性

#### Scenario: 无效 Bearer
- **WHEN** 注册表两种方式都启用，请求的能力已配置且带一个无效的 Bearer 令牌
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

serve SHALL 读取统一客户端注册表和 `ACCOUNTSVC_SERVICE_ISSUER`、`ACCOUNTSVC_SERVICE_AUDIENCE`。至少一个注册客户端启用 bearer 时 SHALL 要求签发方和 audience；没有 bearer 客户端但签发方或 audience 非空 SHALL 视为配置错误。签发方 SHALL 为不含凭据、查询和片段的绝对 URL：`ACCOUNTSVC_MODE=production` 时 SHALL 只接受 HTTPS；`ACCOUNTSVC_MODE=development` 时 SHALL 另接受 `http://`，此时 discovery 地址与 `jwks_uri` 同样允许 HTTP，serve SHALL 在监听前记录一条指出 `ACCOUNTSVC_SERVICE_ISSUER` 的 WARN。签发方 SHALL 不等于消费者 JWT issuer，可以等于管理员 issuer。audience SHALL 非空且无首尾空白，SHALL 不等于消费者 JWT audience、已启用的管理员 audience、任一已配置的第三方登录应用或客户端 ID，以及任一启用 bearer 的客户端 ID。违反任一规则 SHALL 在监听前非零退出并指出变量名，不回显值。migrate SHALL 不解析新旧服务客户端或 issuer/audience 变量。

#### Scenario: 列表相交
- **WHEN** 同一客户端启用 bearer 并显式持有 tokens.introspect 和 purged_users.read
- **THEN** 配置通过，不执行旧逐接口名单不相交约束

#### Scenario: audience 冲突
- **WHEN** 服务 audience 等于消费者 audience、管理员 audience、某个第三方应用 ID 或某个启用 bearer 的客户端 ID
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_SERVICE_AUDIENCE`

#### Scenario: 半配置
- **WHEN** 存在 bearer 客户端但缺少签发方或 audience，或设置了签发方/audience 但没有 bearer 客户端
- **THEN** serve 启动失败并指出缺失或多余的变量名

#### Scenario: 生产模式拒绝 HTTP 签发方
- **WHEN** `ACCOUNTSVC_MODE` 为 production（含未设置）且 `ACCOUNTSVC_SERVICE_ISSUER` 为 `http://` 地址
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_SERVICE_ISSUER`，不回显值

#### Scenario: 开发模式接受 HTTP 签发方
- **WHEN** 存在有效 bearer 客户端，`ACCOUNTSVC_MODE=development` 且 `ACCOUNTSVC_SERVICE_ISSUER` 为不含凭据、查询和片段的 `http://` 地址
- **THEN** serve 通过配置校验并记录一条指出 `ACCOUNTSVC_SERVICE_ISSUER` 的 WARN；Bearer 令牌按该 HTTP issuer 完成 discovery、JWKS 加载与 `iss` 精确匹配

### Requirement: 调用认证的可观测性

日志与错误响应 SHALL 不包含 Bearer 令牌、Basic 秘密或 `Authorization` 头内容；调用方只以客户端 ID（Basic 用户名或 `azp`）和认证方式（`basic`/`bearer`）标识。若记录认证方式或结果的指标，标签取值 SHALL 限定为固定集合：接口 `introspect`/`purged_users`/`identities_resolve`，方式 `basic`/`bearer`/`none`，结果 `ok`/`unauthenticated`/`forbidden`/`unavailable`。

#### Scenario: 拒绝日志
- **WHEN** 有效令牌因 `azp` 未授权被拒绝，或签发方刷新失败
- **THEN** 日志只出现客户端 ID、接口、签发方地址等非秘密字段，不出现令牌内容

### Requirement: 统一客户端环境变量

serve SHALL 始终构造同一个统一客户端注册表，ACCOUNTSVC_SERVICE_CLIENTS 的值为逗号分隔别名；未设置、显式空或全空白均表示空注册表，不选择另一种配置路径。每个别名 SHALL 对应 ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_ID、_AUTH_METHODS、_PERMISSIONS、_BASIC_SECRETS。别名 SHALL 匹配 [A-Z][A-Z0-9_]{0,31}，ID 为 1–256 字节且无冒号、空白或控制字符；别名和 ID 分别唯一。列表元素裁剪两端空白后 SHALL 拒绝空项、重复项；已声明客户端的 ID、方法和权限必填。SHALL 拒绝未声明别名和未知字段，包括主列表缺失或为空时的孤立字段，不回显值。

#### Scenario: 独立别名
- **WHEN** SERVICE_CLIENTS=AI_FOOD 且对应 ID=ai-food-server，其他必需字段有效
- **THEN** 配置通过，Basic 用户名和已验签 azp 均匹配 ai-food-server，AI_FOOD 仅用于配置

#### Scenario: 配置拼写或重复
- **WHEN** 存在重复别名、重复 ID、尾随逗号空项、未声明别名字段，或 AUTH_METHOD 拼错
- **THEN** 在监听前失败并只指出相关变量名和错误类别

#### Scenario: 缺省和显式空注册表一致
- **WHEN** SERVICE_CLIENTS 未设置、为空或全空白，且不存在客户端字段、非空白旧配置或非空 issuer/audience
- **THEN** 三种情况均构造非 nil 空注册表并正常启动，三个服务接口各返回其 NOT_CONFIGURED 503

#### Scenario: 空列表不豁免孤立字段
- **WHEN** SERVICE_CLIENTS 缺失、为空或全空白，但存在任一 SERVICE_CLIENT_<ALIAS>_* 字段
- **THEN** 监听前拒绝该未声明字段，不将其静默忽略或恢复旧路径

### Requirement: 统一方法与权限目录

AUTH_METHODS SHALL 只接受 basic、bearer；PERMISSIONS SHALL 只接受 tokens.introspect、purged_users.read、identities.resolve，均大小写敏感且无通配符。SHALL 允许同一客户端同时启用两种方式并持有上述权限的任意非空子集，两种方式共享同一权限集合。SHALL 拒绝空集合和未知值，包括单数形式 identity.resolve。未来新增权限 SHALL 随对应端点的明确契约注册，不因配置字符串自动产生能力。

#### Scenario: 同一身份访问两项能力
- **WHEN** 一个客户端启用 basic,bearer 且拥有两个权限，分别以两种有效凭据请求两个接口
- **THEN** 四种组合均进入正常业务处理

#### Scenario: 未知权限
- **WHEN** 配置 identity.resolve、星号、未知权限或空权限列表
- **THEN** 启动失败，不忽略无效条目或默认授予全部权限

#### Scenario: 注册身份解析权限
- **WHEN** 一个客户端只配置 identities.resolve，或同时持有全部三项权限，且认证配置有效
- **THEN** 配置通过，仅对其显式权限放行；只读 Bearer scope 中的同名值不能授予权限

### Requirement: 统一 Basic 秘密与轮换

启用 Basic 的客户端 SHALL 配置逗号分隔的 BASIC_SECRETS，每项为规范标准 Base64，解码不少于 32 字节，原 Base64 文本作为密码。SHALL 拒绝重复秘密、跨客户端复用秘密及 Basic 未启用时的非空秘密。密码匹配 SHALL 使用固定长度摘要的常数时间比较并检查所有轮换条目。SHALL 支持同客户端新旧秘密重叠轮换；移除旧秘密并重启后旧秘密失效。秘密 SHALL 不输出到错误、日志或指标。

#### Scenario: 重叠轮换
- **WHEN** 同一客户端配置新旧两份秘密后，移除旧秘密并重启
- **THEN** 重叠期两者获得相同权限，重启后旧秘密返回 401，新秘密继续可用

#### Scenario: 跨客户端复用
- **WHEN** 两个不同 ID 使用相同 Basic 秘密，或仅 bearer 客户端仍配置非空秘密
- **THEN** serve 启动失败，不合并身份或忽略秘密

### Requirement: 统一 Bearer 信任配置

SHALL 在至少一个客户端启用 bearer 时要求 SERVICE_ISSUER/AUDIENCE，否则非空信任参数 SHALL 作为半配置拒绝。SHALL 保持本规格 Bearer 签名、claims、Discovery/JWKS 缓存、故障分类及启动规则。issuer URL、production HTTPS、development HTTP 警告及消费者/管理员/第三方 audience 隔离 SHALL 遵循本规格的服务令牌配置校验；audience SHALL 不等于任一启用 bearer 客户端的 ID。SHALL 只从已验证令牌 azp 识别调用方，不从 scope、角色或客户端提交字段授予权限。

#### Scenario: Basic-only 无提供方
- **WHEN** 全部客户端仅 basic，未配置 issuer/audience
- **THEN** serve 不访问 OIDC 提供方，Basic 正常运行

#### Scenario: 半配置与受众冲突
- **WHEN** 存在 bearer 却缺 issuer/audience，或没有 bearer 但配置信任参数，或 audience 与启用 bearer 的客户端 ID 相同
- **THEN** 监听前拒绝并指出相关变量名

#### Scenario: 提供方故障隔离
- **WHEN** Bearer 所需公钥不可用且没有仍有效的缓存
- **THEN** Bearer 返回 503，已配置 Basic 继续可用，服务监听和 readiness 保持既有行为

### Requirement: 统一认证与授权结果

SHALL 先按能力是否配置判定 NOT_CONFIGURED，再校验唯一 Authorization 头和凭据、客户端方法开关、端点权限，最后才解析业务请求。Basic 未知用户名或错误秘密返回 401；有效 Bearer 的未知 azp 返回 403；已登记客户端使用未启用方式返回 401；已认证但缺权限两种方式都返回 403。接口是否配置 SHALL 以至少一个客户端持有该权限判定。SHALL 不自动回退认证方式。消费者和管理员凭据 SHALL 不作为服务凭据接受。所有构造路径 SHALL 使用 Registry 与 RequiredPermission，包括内部 Basic-only 便利构造；nil Registry SHALL 不触发旧映射或名单授权。

#### Scenario: Basic 已认证但缺权限
- **WHEN** 两项能力均已配置，客户端有效 Basic 只拥有清理权限却调用内省
- **THEN** 返回 403，不解析或验证请求体中的消费者 token

#### Scenario: 未启用方法
- **WHEN** 客户端仅允许 basic，但提交签名和 claims 均有效且 azp 匹配的服务 Bearer
- **THEN** 返回 401，不授予该客户端已有权限，也不回退 Basic

#### Scenario: 多头与未登记身份
- **WHEN** 请求携带多个 Authorization 头，或仅携带一个有效但 azp 未登记的服务令牌
- **THEN** 前者返回 401 且不解析头内容，后者返回 403，两者均无业务处理

### Requirement: 统一认证挑战

401 SHALL 以全注册表启用方法的并集输出原接口 realm 的挑战，不按请求中的未验证 ID 改变挑战。仅在单一 Bearer 已尝试校验且令牌无效时附 invalid_token；客户端方式未启用不附该属性。Basic 权限不足的 403 SHALL 不带 WWW-Authenticate，Bearer 权限不足的 403 SHALL 带 Bearer insufficient_scope。503 SHALL 无挑战。所有响应 SHALL no-store；错误体保持原接口协议。

#### Scenario: 两种方式启用时无凭据
- **WHEN** 注册表启用 Basic 与 Bearer，已配置能力收到无凭据请求
- **THEN** 返回 401，两条挑战分别声明 Basic 和 Bearer，均无 error 属性

#### Scenario: 方法不同的权限拒绝
- **WHEN** 有效 Basic 和有效 Bearer 分别请求缺少授权但已配置的能力
- **THEN** 均返回 403；Basic 无挑战，Bearer 带 insufficient_scope，响应均 no-store

### Requirement: 移除旧逐接口配置

serve SHALL 拒绝任一非空白旧变量 ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS、ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS、ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS、ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS，包含值为 {} 或 []，无论 SERVICE_CLIENTS 是否设置。错误 SHALL 只指出变量名与配置已移除的固定原因，不回显值；空白旧占位 SHALL 忽略。SHALL 移除旧配置映射、名单、模式选择、JSON 解析/装配/认证路径及弃用警告，不自动转换或合并旧身份与权限。migrate SHALL 不读取或校验这些变量。

#### Scenario: 仅旧配置不能启动
- **WHEN** 未设置 SERVICE_CLIENTS，任一旧逐接口变量非空白
- **THEN** serve 在监听前失败并指出已移除变量，不静默变为空注册表或继续旧认证

#### Scenario: 混合旧配置与空 JSON 仍拒绝
- **WHEN** SERVICE_CLIENTS 已设置，且任一旧变量非空白，包括 {} 或 []
- **THEN** serve 在监听前失败，不因旧 JSON 为空而接受，也不合并权限

#### Scenario: 空白旧占位
- **WHEN** 四项旧变量只含空值或空白，其他现行配置有效
- **THEN** serve 按注册表正常启动，不记录弃用警告

#### Scenario: Basic-only 构造遵守同一权限路径
- **WHEN** 内部便利构造仅配置 Basic，客户端已认证但缺少已配置能力的权限
- **THEN** 返回 403，不通过 nil Registry 或旧映射分支放行
