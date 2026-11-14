# Spec Delta

## MODIFIED Requirements

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
