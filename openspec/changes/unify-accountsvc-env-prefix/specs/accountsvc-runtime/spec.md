## MODIFIED Requirements

### Requirement: 配置校验与安全启动

服务 SHALL 只使用 `ACCOUNTSVC_` 前缀读取全部配置：库配置项沿用库定义的名称并以 `ACCOUNTSVC_` 为前缀，服务配置项同样以 `ACCOUNTSVC_` 为前缀；运行模式默认 production。serve 与 migrate SHALL 在读取配置前拒绝任何非空的 `ACCOUNTKIT_` 变量，以非零退出并指出该变量名及对应新名，不回显值。服务配置项名称 SHALL 不与库配置项名称重叠，也不使用库按 `ALIPAY_`、`OIDC_` 动态生成的名称空间。serve SHALL 在监听前完成配置校验；非法配置 SHALL 非零退出并指出配置项名称，错误不得泄露配置值。显式启用 GOOGLE 但没有 client ID 时 SHALL 指出 `ACCOUNTSVC_GOOGLE_CLIENT_IDS`。仅启用 GOOGLE、Required 为空且 client ID 有效时，无 SMTP SHALL 仍可启动。服务 SHALL 分别遵守下述身份策略、HTTP TLS、生产依赖与 migrate 依赖边界。

#### Scenario: 配置缺失
- **WHEN** serve 缺少已启用 EMAIL 所需的 SMTP、数据库、Redis、内省客户端必填配置，或在有效 TLS 配置要求启用时缺少证书/私钥
- **THEN** 在接收请求前以非零退出并指出配置项名称，不泄露秘密

#### Scenario: 生产明文配置
- **WHEN** production 未显式关闭 HTTP TLS 却没有完整有效证书，或在 EMAIL 启用时请求使用明文 SMTP
- **THEN** 启动失败；HTTP TLS 开关不允许生产 SMTP 降级

#### Scenario: 外部终止模式
- **WHEN** production 显式设置 TLS_ENABLED=false，未配置 HTTP 证书项且其他必需配置有效
- **THEN** 单监听器以 HTTP 启动，认证与授权仍生效，启动日志明确本进程未启用 HTTP TLS；部署方负责外部传输保护，服务不自动推断网格存在

#### Scenario: 显式 TLS 与冲突配置
- **WHEN** TLS_ENABLED=true 但证书无效，TLS_ENABLED=false 却配置证书或私钥，或开关不是有效布尔值
- **THEN** 拒绝启动，不忽略冲突、不回退其他模式、不在错误中输出秘密

#### Scenario: 原开发默认与 migrate 保持兼容
- **WHEN** development 未设置开关且未配证书，或运行不读取服务专用设置的 migrate
- **THEN** 前者仍可使用 HTTP；后者不要求或解析监听地址、TLS 开关、证书、SMTP 或内省客户端配置

#### Scenario: 纯第三方服务
- **WHEN** 显式 Enabled 为 WECHAT 或 APPLE、Required 为 []，提供方配置有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动，并完成正常登录及重新登录；不因此新增第三方重新认证或恢复能力；没有发送邮件的运行依赖

#### Scenario: 仅 Google 的服务
- **WHEN** 显式 Enabled 为 GOOGLE、Required 为 []、client ID 有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动并完成 Google 登录，不要求邮件发送

#### Scenario: Google 缺少 client ID
- **WHEN** 显式 Enabled 包含 GOOGLE 但 client ID 为空
- **THEN** 启动失败并指出 `ACCOUNTSVC_GOOGLE_CLIENT_IDS`，错误不泄露其他配置值

#### Scenario: 服务不能凭开关获得短信能力
- **WHEN** serve 显式 Enabled 包含 PHONE
- **THEN** 启动失败并指出缺少服务支持；直接嵌入库的宿主仍可注入真实短信发送器

#### Scenario: migrate 不受认证依赖阻塞
- **WHEN** 执行 migrate，身份集合本身合法而 SMTP 或 IdP 运行配置未提供
- **THEN** 不监听、不构造认证处理器、不要求这些运行依赖；迁移锁及密钥版本检查仍执行

#### Scenario: 旧前缀被拒绝
- **WHEN** serve 或 migrate 的环境中存在非空的 `ACCOUNTKIT_JWT_KEYS` 或其他 `ACCOUNTKIT_` 变量
- **THEN** 启动失败并指出该变量名及应改用的 `ACCOUNTSVC_` 名称，不输出变量值，也不回退读取旧名

#### Scenario: 统一前缀读取库配置
- **WHEN** 部署方以 `ACCOUNTSVC_JWT_KEYS`、`ACCOUNTSVC_AUTH_SCHEMA` 等名称提供库配置
- **THEN** 服务按库的相同规则解析，非法值的错误指出 `ACCOUNTSVC_` 变量名

### Requirement: 服务身份策略与发送能力

serve SHALL 读取 ACCOUNTSVC_ENABLED_IDENTITY_KINDS 和 ACCOUNTSVC_REQUIRED_IDENTITY_KINDS，对显式策略执行子集与依赖校验；未设置时保留原默认装配。EMAIL 显式禁用时 SHALL 不要求或解析 SMTP 设置。显式启用 PHONE SHALL 要求已配置短信服务商；未配置时 SHALL 拒绝启动，不使用日志或伪发送器替代。省略 ENABLED 时，即使配置了短信服务商，短信渠道也 SHALL 保持显式禁用，不自动启用 PHONE。显式启用 GOOGLE 时 SHALL 要求至少一个 client ID，缺失时指出 `ACCOUNTSVC_GOOGLE_CLIENT_IDS`。

#### Scenario: 身份集合与服务发送能力一致
- **WHEN** serve 读取显式身份策略
- **THEN** 校验集合及运行依赖；EMAIL 禁用时不解析 SMTP；PHONE 启用时须已配置短信服务商，否则拒绝启动；直接嵌入库仍可注入真实发送器

#### Scenario: 配置短信服务商但未显式启用 PHONE
- **WHEN** serve 配置了阿里云短信，但省略 ACCOUNTSVC_ENABLED_IDENTITY_KINDS
- **THEN** 身份策略与短信渠道均保持原默认装配，发送短信验证码返回 `CHANNEL_NOT_ENABLED`

#### Scenario: 启用 Google 但未配置客户端
- **WHEN** serve 的显式 Enabled 包含 GOOGLE，且 Google client ID 为空
- **THEN** 启动失败并指出 `ACCOUNTSVC_GOOGLE_CLIENT_IDS`

### Requirement: 服务 OIDC 提供方配置
serve SHALL 从 `ACCOUNTSVC_OIDC_PROVIDERS` 读取逗号分隔的 OIDC 类型名，并为每个类型读取 `ACCOUNTSVC_OIDC_<KIND>_ISSUER`、`_JWKS_URL`、`_CLIENT_IDS` 和可选的 `_NONCE_TTL`、`_NAME_CLAIMS`、`_PICTURE_CLAIMS`（变量未设置为默认映射，设置为空为不读取）。缺失或非法时 SHALL 在监听前非零退出并指出具体变量名，不泄露配置值。显式 Enabled 包含未在 `ACCOUNTSVC_OIDC_PROVIDERS` 中配置的第三方类型时 SHALL 指出 `ACCOUNTSVC_ENABLED_IDENTITY_KINDS`。仅启用 OIDC 类型且 Required 为空时，无 SMTP SHALL 仍可启动。未设置任何 OIDC 变量时 migrate SHALL 不要求它们；已列出 OIDC 类型但其配置不完整或非法时，migrate 与 serve SHALL 同样启动失败并指出变量名，与其他已提供但格式非法的库配置一致。

#### Scenario: 缺少 issuer
- **WHEN** `ACCOUNTSVC_OIDC_PROVIDERS=CORP_SSO` 而未设置 `ACCOUNTSVC_OIDC_CORP_SSO_ISSUER`
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_OIDC_CORP_SSO_ISSUER`

#### Scenario: 仅 OIDC 的服务
- **WHEN** 显式 Enabled 为 CORP_SSO、Required 为 []、该类型配置有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动并完成 CORP_SSO 登录

#### Scenario: migrate 不需要 OIDC 配置
- **WHEN** 执行 migrate 且未设置任何 OIDC 变量
- **THEN** 迁移照常执行，不因 OIDC 配置失败

#### Scenario: migrate 遇到不完整的 OIDC 配置
- **WHEN** 执行 migrate，`ACCOUNTSVC_OIDC_PROVIDERS` 列出 CORP_SSO 但缺少其 issuer
- **THEN** 启动失败并指出 `ACCOUNTSVC_OIDC_CORP_SSO_ISSUER`

### Requirement: 服务支付宝配置与装配
serve SHALL 读取 ACCOUNTSVC_ALIPAY_APP_IDS 及每个 AppID 的 ACCOUNTSVC_ALIPAY_<APPID>_PRIVATE_KEY / _PUBLIC_KEY，和 ACCOUNTSVC_ALIPAY_SUBJECT_MODE、ACCOUNTSVC_ALIPAY_GATEWAY_URL。配置 SHALL 遵循 alipay-sign-in 的校验要求；模式缺省 UNIONID，应用列表为空不启用。显式启用 ALIPAY 而无应用配置时 SHALL 启动失败并指出 ACCOUNTSVC_ALIPAY_APP_IDS，缺少密钥时指出对应变量名，不输出值。服务 SHALL 复用既有登录和绑定路由，不提供 authInfo 或授权回调入口。

#### Scenario: 多应用服务
- **WHEN** 配置两个有效 AppID 及各自密钥，其他服务配置合法
- **THEN** serve 正常装配两个允许应用，均使用实例固定身份模式

#### Scenario: 应用缺少公钥
- **WHEN** APP_IDS 含某 AppID，但对应 PUBLIC_KEY 未设置
- **THEN** 监听前拒绝并指出该变量名，不输出私钥或其他配置内容

#### Scenario: 非法列表和多行 PEM
- **WHEN** APP_IDS 有重复项或空项，或者密钥内容不是可解析 PEM
- **THEN** 拒绝启动；合法包含真实换行的 PEM 可读取，不将冒号、逗号或文本反斜杠转义作为密钥分隔协议

#### Scenario: 无授权发起端点
- **WHEN** App 请求支付宝登录
- **THEN** 按既有第三方登录协议提交 code，不要求调用新的服务端授权发起或签名端点

### Requirement: 服务 Facebook 配置
serve SHALL 读取 `ACCOUNTSVC_FACEBOOK_APP_ID`、`_APP_SECRET` 及可选的 `_GRAPH_API_VERSION`、`_NONCE_TTL`、`_GRAPH_API_BASE_URL`、`_JWKS_URL`。App ID 非空而 Secret 缺失或其他项非法时 SHALL 在监听前非零退出并指出变量名，不泄露配置值。显式 Enabled 包含 FACEBOOK 而 App ID 为空时 SHALL 指出 `ACCOUNTSVC_FACEBOOK_APP_ID`。仅启用 FACEBOOK 且 Required 为空时，无 SMTP SHALL 仍可启动。未设置 Facebook 变量时 migrate SHALL 不要求它们。服务 SHALL 不提供数据删除回调路由。

#### Scenario: 缺少 App Secret
- **WHEN** 设置了 `ACCOUNTSVC_FACEBOOK_APP_ID` 而未设置 `ACCOUNTSVC_FACEBOOK_APP_SECRET`
- **THEN** serve 启动失败并指出 `ACCOUNTSVC_FACEBOOK_APP_SECRET`，错误不含其他配置值

#### Scenario: 启用 Facebook 但未配置 App
- **WHEN** 显式 Enabled 包含 FACEBOOK 且 `ACCOUNTSVC_FACEBOOK_APP_ID` 为空
- **THEN** 启动失败并指出 `ACCOUNTSVC_FACEBOOK_APP_ID`

#### Scenario: 仅 Facebook 的服务
- **WHEN** 显式 Enabled 为 FACEBOOK、Required 为 []、App ID 与 Secret 有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动并完成 Facebook 登录，不要求邮件发送

#### Scenario: migrate 不需要 Facebook 配置
- **WHEN** 执行 migrate 且未设置任何 Facebook 变量
- **THEN** 迁移照常执行，不因 Facebook 配置失败

### Requirement: 服务 QQ 配置与装配
serve SHALL 读取 ACCOUNTSVC_QQ_APP_IDS、ACCOUNTSVC_QQ_SUBJECT_MODE 和 ACCOUNTSVC_QQ_API_BASE_URL。配置 SHALL 遵循 qq-sign-in 的校验要求；模式缺省 UNIONID，应用列表为空不启用。显式启用 QQ 而无应用配置时 SHALL 启动失败并指出 ACCOUNTSVC_QQ_APP_IDS，不输出配置值。服务 SHALL 复用既有登录和绑定路由，不提供授权发起或回调入口，也不要求 QQ AppKey。

#### Scenario: 多应用服务
- **WHEN** 配置两个有效 QQ AppID，其他服务配置合法
- **THEN** serve 正常装配两个允许应用，均使用实例固定身份模式

#### Scenario: 启用但未配置应用
- **WHEN** Enabled 含 QQ，而 ACCOUNTSVC_QQ_APP_IDS 未设置或为空
- **THEN** 监听前拒绝并指出 ACCOUNTSVC_QQ_APP_IDS

#### Scenario: 非法列表或模式
- **WHEN** APP_IDS 有重复项、空项或非数字项，或 SUBJECT_MODE 为 AUTO
- **THEN** 拒绝启动并指出对应变量名，不输出变量内容

### Requirement: QQ 身份策略与迁移隔离
仅启用 QQ、Required 显式为空且 QQ 配置完整时，serve SHALL 可在未配置 SMTP 时启动；Required 缺省与非空时 SHALL 保持既有手机号/邮箱补全要求。migrate SHALL 不要求配置 QQ 或访问 QQ；已提供的 QQ 配置 SHALL 接受静态格式校验，非法时拒绝，不为迁移静默忽略。

#### Scenario: 纯 QQ 服务
- **WHEN** Enabled 为 QQ，Required 为 []，应用与其他服务配置有效且未配置 SMTP
- **THEN** serve 可启动并完成纯第三方登录，不需要邮件发送器

#### Scenario: 登录仍需邮箱补全
- **WHEN** Enabled 含 EMAIL、QQ，Required 为 EMAIL，新 QQ 身份没有邮箱证明
- **THEN** 按既有 pending 流程补全，不能因 QQ 资料查询成功绕过 Required

#### Scenario: migrate 不需要 QQ 配置
- **WHEN** 运行 migrate，QQ 变量未设置，身份策略与迁移配置有效
- **THEN** 迁移只按原边界校验，不调用 QQ

#### Scenario: migrate 遇到非法已配置项
- **WHEN** 运行 migrate，ACCOUNTSVC_QQ_APP_IDS 含非数字项
- **THEN** 共享静态配置校验拒绝并指出该配置项

### Requirement: 服务微博配置与装配
serve SHALL 读取 ACCOUNTSVC_WEIBO_APP_IDS 和 ACCOUNTSVC_WEIBO_API_BASE_URL。配置 SHALL 遵循 weibo-sign-in 的校验要求；应用列表为空不启用。显式启用 WEIBO 而无应用配置时 SHALL 启动失败并指出 ACCOUNTSVC_WEIBO_APP_IDS，不输出配置值。服务 SHALL 复用既有登录和绑定路由，不提供授权发起、回调或取消授权通知入口，也不要求微博 AppSecret。

#### Scenario: 多应用服务
- **WHEN** 配置两个有效微博 AppKey，其他服务配置合法
- **THEN** serve 正常装配两个允许应用，两者的同一 uid 命中同一身份

#### Scenario: 启用但未配置应用
- **WHEN** Enabled 含 WEIBO，而 ACCOUNTSVC_WEIBO_APP_IDS 未设置或为空
- **THEN** 监听前拒绝并指出 ACCOUNTSVC_WEIBO_APP_IDS

#### Scenario: 非法列表或地址
- **WHEN** APP_IDS 有重复项、空项或非数字项，或 API_BASE_URL 为 HTTP 非回环地址
- **THEN** 拒绝启动并指出对应变量名，不输出变量内容

### Requirement: 微博身份策略与迁移隔离
仅启用 WEIBO、Required 显式为空且微博配置完整时，serve SHALL 可在未配置 SMTP 时启动；Required 缺省与非空时 SHALL 保持既有手机号/邮箱补全要求。migrate SHALL 不要求配置微博或访问微博；已提供的微博配置 SHALL 接受静态格式校验，非法时拒绝，不为迁移静默忽略。

#### Scenario: 纯微博服务
- **WHEN** Enabled 为 WEIBO，Required 为 []，应用与其他服务配置有效且未配置 SMTP
- **THEN** serve 可启动并完成纯第三方登录，不需要邮件发送器

#### Scenario: 登录仍需邮箱补全
- **WHEN** Enabled 含 EMAIL、WEIBO，Required 为 EMAIL，新微博身份没有邮箱证明
- **THEN** 按既有 pending 流程补全，不能因微博资料查询成功绕过 Required

#### Scenario: migrate 不需要微博配置
- **WHEN** 运行 migrate，微博变量未设置，身份策略与迁移配置有效
- **THEN** 迁移只按原边界校验，不调用微博

#### Scenario: migrate 遇到非法已配置项
- **WHEN** 运行 migrate，ACCOUNTSVC_WEIBO_APP_IDS 含非数字项
- **THEN** 共享静态配置校验拒绝并指出该配置项

### Requirement: 服务资料补填配置
服务 SHALL 通过 `ACCOUNTSVC_PROVIDER_PROFILE_BACKFILL` 接受资料补填配置，未设置时视为开启。非法布尔值 SHALL 使 serve 与 migrate 在启动前以非零退出并指出该配置名，不泄露其他配置值。无秘密配置模板 SHALL 列出该项及其默认值。

#### Scenario: 关闭补填
- **WHEN** serve 设置 `ACCOUNTSVC_PROVIDER_PROFILE_BACKFILL=false`，其他配置有效
- **THEN** 服务正常启动，已有账号的第三方登录不补填空资料，新账号仍按通用规则填充

#### Scenario: 非法值
- **WHEN** `ACCOUNTSVC_PROVIDER_PROFILE_BACKFILL` 不是有效布尔值
- **THEN** 启动失败并指出 `ACCOUNTSVC_PROVIDER_PROFILE_BACKFILL`
