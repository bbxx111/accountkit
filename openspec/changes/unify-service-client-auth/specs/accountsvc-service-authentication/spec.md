# Spec Delta

## MODIFIED Requirements

### Requirement: 两种调用认证方式并存

本条原逐接口规则 SHALL 仅适用于旧配置模式；统一模式 SHALL 按本规格的统一客户端配置、认证授权与挑战要求执行。

内省与已清理用户拉取 SHALL 各自独立配置可接受的调用认证方式：HTTP Basic 客户端凭据，以及外部提供方按 RFC 6749 §4.4 客户端凭据模式签发的 Bearer 访问令牌。两种方式 SHALL 可以单独或同时启用；同时启用时任一方式认证成功即可。请求 SHALL 恰好携带一个 `Authorization` 头；缺失、多于一个、方案无法识别或方案未在该接口启用时 SHALL 视为未提供有效凭据，且不读取任何一个头的内容。认证 SHALL 先于请求方法、内容类型、请求体和查询参数的处理。仅启用 Basic 的部署 SHALL 与引入 Bearer 之前的行为一致。

#### Scenario: 两种方式同时启用
- **WHEN** 使用旧配置模式，接口同时启用 Basic 与 Bearer，调用方分别使用有效 Basic 凭据和有效 Bearer 令牌
- **THEN** 两个请求都进入接口的正常处理

#### Scenario: 多个 Authorization 头
- **WHEN** 使用旧配置模式，请求同时携带 Basic 与 Bearer 两个 `Authorization` 头，或两个 Bearer 头
- **THEN** 返回 401，视为未提供有效凭据，不附 `error="invalid_token"`

#### Scenario: 未启用的方式
- **WHEN** 使用旧配置模式，接口只启用 Basic，请求携带 Bearer 令牌
- **THEN** 返回 401，challenge 只包含 Basic

### Requirement: 按客户端逐接口授权

本条原逐接口规则 SHALL 仅适用于旧配置模式；统一模式 SHALL 按本规格的统一客户端配置、认证授权与挑战要求执行。

通过校验的 Bearer 令牌 SHALL 以 `azp` 作为调用方客户端 ID，且仅当该值在当前接口的允许列表中时获得授权。内省与拉取的允许列表 SHALL 相互独立且不相交，一个客户端 SHALL 不能同时获准调用两个接口。`azp` 不在当前接口列表中（包括属于另一接口的列表）SHALL 返回 403，而不是 401。

#### Scenario: 属于另一接口的客户端
- **WHEN** 使用旧配置模式，有效令牌的 `azp` 只在拉取接口的允许列表中，调用方请求内省
- **THEN** 内省返回 403，不执行任何令牌内省

#### Scenario: audience 误配的管理员令牌
- **WHEN** 使用旧配置模式，人类管理员令牌的 `aud` 被误配为包含服务 audience，其 `azp` 是管理前端的客户端 ID
- **THEN** 返回 403，不授予任何接口的访问

### Requirement: 认证挑战

本条原逐接口规则 SHALL 仅适用于旧配置模式；统一模式 SHALL 按本规格的统一客户端配置、认证授权与挑战要求执行。

401 响应 SHALL 为每个已启用的方式各给出一个 `WWW-Authenticate` 头：`Basic realm="<realm>"` 与 `Bearer realm="<realm>"`，内省的 realm 为 `accountsvc-introspection`，拉取为 `accountsvc-purge-feed`。仅当请求恰好携带一个 Bearer `Authorization` 头且令牌被判定无效时，Bearer challenge SHALL 附 `error="invalid_token"`。403 SHALL 只给出带 `error="insufficient_scope"` 的 Bearer challenge。503 SHALL 不带 challenge。所有响应 SHALL 设置 `Cache-Control: no-store`。

#### Scenario: 无凭据
- **WHEN** 使用旧配置模式，两种方式都启用，请求不带 `Authorization`
- **THEN** 返回 401 和两条 challenge，均不含 `error` 属性

#### Scenario: 无效 Bearer
- **WHEN** 使用旧配置模式，两种方式都启用，请求带一个无效的 Bearer 令牌
- **THEN** 返回 401，Basic challenge 不变，Bearer challenge 含 `error="invalid_token"`

### Requirement: 服务令牌配置校验

本条原逐接口规则 SHALL 仅适用于旧配置模式；统一模式 SHALL 按本规格的统一客户端配置、认证授权与挑战要求执行。

serve SHALL 读取 `ACCOUNTSVC_SERVICE_ISSUER`、`ACCOUNTSVC_SERVICE_AUDIENCE`、`ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS`、`ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`。两个客户端列表为 JSON 字符串数组，元素 SHALL 唯一、非空、不含空白或控制字符且不超过 256 字节；空数组视同未配置。任一列表非空时 SHALL 要求签发方和 audience；签发方或 audience 已设置而两个列表都为空 SHALL 视为配置错误。签发方 SHALL 为不含凭据、查询和片段的绝对 URL：`ACCOUNTSVC_MODE=production` 时 SHALL 只接受 HTTPS；`ACCOUNTSVC_MODE=development` 时 SHALL 另接受 `http://`，此时 discovery 地址与 `jwks_uri` 同样允许 HTTP，serve SHALL 在监听前记录一条指出 `ACCOUNTSVC_SERVICE_ISSUER` 的 WARN。签发方 SHALL 不等于消费者 JWT issuer，可以等于管理员 issuer。audience SHALL 非空且无首尾空白，SHALL 不等于消费者 JWT audience、已启用的管理员 audience、任一已配置的第三方登录应用或客户端 ID，以及任一允许的 `azp`。两个列表 SHALL 不相交。违反任一规则 SHALL 在监听前非零退出并指出变量名，不回显值。migrate SHALL 不解析这些变量。

#### Scenario: 列表相交
- **WHEN** 使用旧配置模式，同一 `azp` 同时出现在两个客户端列表中
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS`

#### Scenario: audience 冲突
- **WHEN** 使用旧配置模式，服务 audience 等于消费者 audience、管理员 audience、某个第三方应用 ID 或某个允许的 `azp`
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_SERVICE_AUDIENCE`

#### Scenario: 半配置
- **WHEN** 使用旧配置模式，设置了客户端列表但缺少签发方或 audience，或设置了签发方/audience 但两个列表都为空
- **THEN** serve 启动失败并指出缺失或多余的变量名

#### Scenario: 生产模式拒绝 HTTP 签发方
- **WHEN** 使用旧配置模式，`ACCOUNTSVC_MODE` 为 production（含未设置）且 `ACCOUNTSVC_SERVICE_ISSUER` 为 `http://` 地址
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_SERVICE_ISSUER`，不回显值

#### Scenario: 开发模式接受 HTTP 签发方
- **WHEN** 使用旧配置模式，`ACCOUNTSVC_MODE=development` 且 `ACCOUNTSVC_SERVICE_ISSUER` 为不含凭据、查询和片段的 `http://` 地址
- **THEN** serve 通过配置校验并记录一条指出 `ACCOUNTSVC_SERVICE_ISSUER` 的 WARN；Bearer 令牌按该 HTTP issuer 完成 discovery、JWKS 加载与 `iss` 精确匹配

## ADDED Requirements

### Requirement: 统一客户端环境变量

serve SHALL 以 ACCOUNTSVC_SERVICE_CLIENTS 的存在性选择统一模式，值为逗号分隔别名；显式空或全空白表示无客户端。每个别名 SHALL 对应 ACCOUNTSVC_SERVICE_CLIENT_<ALIAS>_ID、_AUTH_METHODS、_PERMISSIONS、_BASIC_SECRETS。别名 SHALL 匹配 [A-Z][A-Z0-9_]{0,31}，ID 为 1–256 字节且无冒号、空白或控制字符；别名和 ID 分别唯一。列表元素裁剪两端空白后 SHALL 拒绝空项、重复项；已声明客户端的 ID、方法和权限必填。SHALL 拒绝未声明别名和未知字段，不回显值。

#### Scenario: 独立别名
- **WHEN** SERVICE_CLIENTS=AI_FOOD 且对应 ID=ai-food-server，其他必需字段有效
- **THEN** 配置通过，Basic 用户名和已验签 azp 均匹配 ai-food-server，AI_FOOD 仅用于配置

#### Scenario: 配置拼写或重复
- **WHEN** 存在重复别名、重复 ID、尾随逗号空项、未声明别名字段，或 AUTH_METHOD 拼错
- **THEN** 在监听前失败并只指出相关变量名和错误类别

#### Scenario: 显式空注册表
- **WHEN** SERVICE_CLIENTS 为空且不存在客户端字段、旧非空配置或 issuer/audience
- **THEN** serve 启动，两个服务接口各返回其 NOT_CONFIGURED 503

### Requirement: 统一方法与权限目录

统一模式 AUTH_METHODS SHALL 只接受 basic、bearer；PERMISSIONS SHALL 只接受 tokens.introspect、purged_users.read，均大小写敏感且无通配符。SHALL 允许同一客户端同时启用两种方式并持有两个权限，两种方式共享同一权限集合。SHALL 拒绝空集合和未知值，包括尚未实现的 identities.resolve。未来新增权限 SHALL 随对应端点的明确契约注册，不因配置字符串自动产生能力。

#### Scenario: 同一身份访问两项能力
- **WHEN** 一个客户端启用 basic,bearer 且拥有两个权限，分别以两种有效凭据请求两个接口
- **THEN** 四种组合均进入正常业务处理

#### Scenario: 未知权限
- **WHEN** 配置 identities.resolve、星号、未知权限或空权限列表
- **THEN** 启动失败，不忽略无效条目或默认授予全部权限

### Requirement: 统一 Basic 秘密与轮换

启用 Basic 的客户端 SHALL 配置逗号分隔的 BASIC_SECRETS，每项为规范标准 Base64，解码不少于 32 字节，原 Base64 文本作为密码。SHALL 拒绝重复秘密、跨客户端复用秘密及 Basic 未启用时的非空秘密。密码匹配 SHALL 使用固定长度摘要的常数时间比较并检查所有轮换条目。SHALL 支持同客户端新旧秘密重叠轮换；移除旧秘密并重启后旧秘密失效。秘密 SHALL 不输出到错误、日志或指标。

#### Scenario: 重叠轮换
- **WHEN** 同一客户端配置新旧两份秘密后，移除旧秘密并重启
- **THEN** 重叠期两者获得相同权限，重启后旧秘密返回 401，新秘密继续可用

#### Scenario: 跨客户端复用
- **WHEN** 两个不同 ID 使用相同 Basic 秘密，或仅 bearer 客户端仍配置非空秘密
- **THEN** serve 启动失败，不合并身份或忽略秘密

### Requirement: 统一 Bearer 信任配置

统一模式 SHALL 在至少一个客户端启用 bearer 时要求 SERVICE_ISSUER/AUDIENCE，否则非空信任参数 SHALL 作为半配置拒绝。SHALL 保持本规格 Bearer 签名、claims、Discovery/JWKS 缓存、故障分类及启动规则。issuer URL、production HTTPS、development HTTP 警告及消费者/管理员/第三方 audience 隔离 SHALL 沿用旧模式；audience SHALL 不等于任一启用 bearer 客户端的 ID。SHALL 只从已验证令牌 azp 识别调用方，不从 scope、角色或客户端提交字段授予权限。

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

统一模式 SHALL 先按能力是否配置判定 NOT_CONFIGURED，再校验唯一 Authorization 头和凭据、客户端方法开关、端点权限，最后才解析业务请求。Basic 未知用户名或错误秘密返回 401；有效 Bearer 的未知 azp 返回 403；已登记客户端使用未启用方式返回 401；已认证但缺权限两种方式都返回 403。接口是否配置 SHALL 以至少一个客户端持有该权限判定。SHALL 不自动回退认证方式。消费者和管理员凭据 SHALL 不作为服务凭据接受。

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

统一模式 401 SHALL 以全注册表启用方法的并集输出原接口 realm 的挑战，不按请求中的未验证 ID 改变挑战。仅在单一 Bearer 已尝试校验且令牌无效时附 invalid_token；客户端方式未启用不附该属性。Basic 权限不足的 403 SHALL 不带 WWW-Authenticate，Bearer 权限不足的 403 SHALL 带 Bearer insufficient_scope。503 SHALL 无挑战。所有响应 SHALL no-store；错误体保持原接口协议。

#### Scenario: 两种方式启用时无凭据
- **WHEN** 注册表启用 Basic 与 Bearer，已配置能力收到无凭据请求
- **THEN** 返回 401，两条挑战分别声明 Basic 和 Bearer，均无 error 属性

#### Scenario: 方法不同的权限拒绝
- **WHEN** 有效 Basic 和有效 Bearer 分别请求缺少授权但已配置的能力
- **THEN** 均返回 403；Basic 无挑战，Bearer 带 insufficient_scope，响应均 no-store

### Requirement: 旧配置兼容与模式隔离

SERVICE_CLIENTS 未设置且无客户端分项字段时 SHALL 保留四项旧逐接口配置的解析、互斥约束及 HTTP 行为。本次 SHALL 不移除旧模式或设置自动失效期限，有非空旧配置时启动记录一次仅含变量名的弃用 WARN。统一模式与任一非空白旧逐接口变量同时出现 SHALL 拒绝启动，包括值为 {} 或 []；空白旧变量视为未配置。SHALL 不合并两种配置的身份、秘密或权限。SERVICE_CLIENTS 缺失但存在任一客户端分项字段 SHALL 拒绝启动。

#### Scenario: 旧部署不扩权
- **WHEN** 未设置统一变量，保留原内省与清理的独立凭据及名单
- **THEN** 保持原权限、跨接口 Basic 401/Bearer 403、名单不相交与秘密不复用规则，并提示弃用

#### Scenario: 混合模式
- **WHEN** SERVICE_CLIENTS 已设置且某旧逐接口变量为非空 JSON，即使是空数组或空对象
- **THEN** serve 启动失败，提示冲突变量名，不静默选择模式
