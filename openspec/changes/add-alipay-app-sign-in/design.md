# Design

## Context

动机与范围见 [proposal.md](proposal.md)。现有代码已经具有以下可复用边界：

- `user/idp.Provider` 提供 `Kind/Verify/PolicyFingerprint`，验证发生在 `signInVerified` 的数据库事务之前；`idp.Identity` 不包含第三方 token，适合安全地进入 pending 的加密证明。
- `user/idp/wechat.go`、`policy.go` 已实现实例固定 UNIONID/OPENID 模式、按 AppID 记录 OpenID 和稳定策略指纹。
- `user/service_idp.go` 的 `mergeProviderMeta` 已校验标识矛盾并合并 profile；`seedEmptyProviderProfile` 锁住账号、检查 ACTIVE 和空字段后写入。当前该方法只由 `createProviderIdentity` 调用。
- `user/service_signin_completion.go` 的已有身份登录和 pending 已归属身份分支只更新元数据，尚未补填账号资料；这些是本次新增支付宝补填的落点。
- `config.go`、`accountkit.go` 和 `internal/accountsvc/config.go` 负责静态配置、提供方装配及服务启动。migrate 同样调用 `ConfigFromEnv`，故不要求未启用 IdP 配置，但会拒绝显式提供的非法配置。
- 未归档的 `add-facebook-sign-in` 已在当前源码中实现。注册表 delta 保留其要求和场景；accountsvc/指标使用追加要求，避免覆盖 Facebook 增量。

## Goals / Non-Goals

**Goals:**

- 在现有提供方接口内完成支付宝换码和可选资料查询，保持网络与领域事务分离。
- 用固定身份规则保证 UNIONID 跨 App 关联和 OPENID 应用隔离；保持故障分类、并发一致性与秘密边界。
- 用最小领域改动实现支付宝成功登录后的空字段补填，并覆盖 pending 完成和并发资料更新。

**Non-Goals:**

- 不建立另一套 OAuth 服务、授权发起会话、浏览器回调、客户端 SDK 或支付模块。
- 不给现有 Provider 或公开认证凭证增加访问令牌字段，不引入后台令牌保存和资料同步任务。
- 不修改 SQL schema、迁移历史、JWT/刷新协议或其他提供方的账号资料更新语义。

## Decisions

### 1. 保持一个提供方和一套凭证

新增 `idp.Alipay` 实现现有接口，`Verify` 严格读取 `{app_id, code}`。`Config` 增加 `AlipayApps []AlipayApp`、`AlipaySubjectMode`、`AlipayGatewayURL`；`AlipayApp` 含 `AppID`、`PrivateKey`、`AlipayPublicKey`。模式使用独立类型和 UNIONID/OPENID 常量，不复用名称带 WeChat 的公开类型。

`accountkit.identityProviders` 按配置构造，`providerKinds`、`enum.IsBuiltin`、服务启用校验同步登记 ALIPAY。HTTP handler 无需知道支付宝的网络细节，不新增登录路由或 platform 字段。库本身无法从兑换结果证明客户端操作系统；“仅 App”是本次交付与验收范围，不声称后端能辨认每个 code 的 UI 来源。

备选：把支付宝包装成配置 OIDC。未采用，因为当前凭证是 code，验证是支付宝 OpenAPI 签名协议，不能使用现有 ID Token/JWKS 核心。

### 2. 多应用 PEM 配置和构造时校验

每个 AppID 分别解析并保存应用私钥及支付宝公钥，构造后配置不可变，不保留可由调用方修改的原始切片或 map。以纯函数复用配置校验，禁止在 `New` 或 `ConfigFromEnv` 读取文件、环境引用路径或远端公钥。

| 库配置 | 环境变量后缀 | 规则 |
|---|---|---|
| AlipayApps | ALIPAY_APP_IDS | 逗号分隔 AppID；缺省/空列表禁用，已配置列表不能有空项或重复 |
| PrivateKey | ALIPAY_<APPID>_PRIVATE_KEY | PEM PKCS#1 或 PKCS#8 RSA 私钥，至少 2048 位 |
| AlipayPublicKey | ALIPAY_<APPID>_PUBLIC_KEY | PEM PKCS#1 或 SPKI RSA 公钥，至少 2048 位 |
| AlipaySubjectMode | ALIPAY_SUBJECT_MODE | 未设置使用 UNIONID；显式只接受两个大写值 |
| AlipayGatewayURL | ALIPAY_GATEWAY_URL | 默认生产网关；HTTPS，HTTP 仅允许数值回环测试地址 |

环境前缀仍由 `ConfigFromEnv(prefix)` 决定，accountsvc 示例用 ACCOUNTKIT_。PEM 使用真实换行，不设计冒号分隔密钥、Base64 套 Base64 或自动展开字面 `\\n` 的第二套格式。库支持宿主预先加载密钥内容，但首版不增加文件路径配置。

`Config.Validate` 与提供方构造均确保必要不变量；accountsvc 的安全错误映射需识别动态 AppID 变量，不把 PEM 解析器原始错误输出。migrate 保持现有共享静态校验：不配置支付宝即可迁移；显式配置不合法则报错。不重构整个配置加载框架。

备选：微信式 `appid:secret,...` 字符串。未采用，PEM 多行内容不适合该格式。证书模式留待独立变更。

### 3. 专用、有界的 OpenAPI V2 客户端

使用 Go 标准库 `crypto/rsa`、`crypto/sha256`、`encoding/pem`、`crypto/x509`、`net/http` 实现这两个固定接口，不引入覆盖支付业务的大型 SDK。

调用 `alipay.system.oauth.token` 时，表单包含 `app_id`、`method`、`format=json`、`charset=utf-8`、`sign_type=RSA2`、`timestamp`、`version=1.0`、`grant_type=authorization_code` 和 `code`。签名前排除 sign，按参数名排序，以原始未 URL 编码的值拼接；使用 SHA-256 + RSA PKCS#1 v1.5，标准 Base64 后加入 sign，最后编码表单。timestamp 按固定 UTC+08:00 格式 `2006-01-02 15:04:05` 生成，使用可替换测试时钟，不引入系统时区数据库依赖。

资料请求仍使用同一网关与签名流程，method 为 `alipay.user.info.share`，把本次支付宝 access_token 放到协议的 `auth_token` 参数；不把它当作 accountkit access_token。两个请求均为 POST 表单，禁止跳转，浅拷贝宿主 HTTPClient 防止修改共享客户端。

兑换请求使用 10 秒上下文上限；资料请求使用独立 3 秒上限，均取调用方更早期限。每份响应最多读取 64 KiB，多余或截断拒绝解析；关闭响应体，不在自动重试中重复兑换 code。3xx 不跟随，429/5xx 分类为依赖不可用。宿主注入 HTTPClient 不得通过关闭 Timeout 解除本地上下文预算。

### 4. 在反序列化身份前验证原始响应

按 method 选择唯一成功节点：`alipay_system_oauth_token_response` 或 `alipay_user_info_share_response`；错误使用 `error_response`。保存其 `json.RawMessage` 原始字节，按支付宝协议验证外层 sign，禁止把解析后的 map 重新序列化再验签。解析器拒绝重复 JSON 键、多个互斥节点、尾随内容及错误字段类型；结构与签名必须同时成立。

OAuth 成功节点不强制要求通用业务接口的 `code=10000`：根据该接口实际成功字段验证身份。签名通过后仍需检查必需标识；空白、控制字符或超出约定边界的标识不得拼入 subject。不凭 HTTP 200 或 SDK 客户端 user_id 判断成功。

已签名业务错误采用小型显式错误码表：凭证失效/过期/已用 -> `ErrInvalidCredential`；配置/权限/签名配置错误 -> `ErrMisconfigured`；服务忙等依赖错误 -> `ErrUnavailable`。实施时从官方两个接口错误列表建立固定合成样本，并核验分类，不对任意 `sub_msg` 做字符串猜测。未签名业务错误或未知协议错误保守返回固定内部错误；它们不能作为判定用户凭证错误的可信依据。

返回错误只保留哨兵和安全阶段分类。尤其不要包装含完整请求 URL、请求体或响应体的错误；日志只记录应用配置索引/安全阶段与固定分类，不打印 code、token、sign、昵称或头像。

### 5. 模式和白名单元数据复用微信语义

按配置模式生成 `unionid:<union_id>` 或 `openid:<app_id>:<open_id>`。现有 kind/subject 唯一性用于并发兜底；`WECHAT` 与 `ALIPAY` 即使 subject 文本相同也互不相干。

`idp.Identity.UnionID/OpenIDs` 作为通用的已验证标识声明复用，修正微信专用注释；`mergeProviderMeta` 无需改成支付宝专用存储结构。只把本次响应非空值带入合并；已存非空标识矛盾沿用领域冲突和事务回滚。

指纹对 AppID 排序，包含模式、网关、每个支付宝验签公钥的规范化 SPKI DER SHA-256，不含应用私钥。公钥 PEM 换行不影响指纹。应用分组是支付宝控制台配置，不能通过本地 `New` 验证；部署者必须在首次接入及分组调整时以多 App 真实授权核验。改变模式、分组或验签信任不能当作无损身份迁移。

### 6. 在验证器内同步尝试资料获取

选择每次成功换码后尝试一次资料查询，登录与普通绑定均可复用该验证结果；无需先读数据库判断是否首次登录。UNIONID 足够验证身份但无 OpenID 或访问令牌时，资料不可可信关联/查询，跳过资料并记录降级，仍允许身份流程继续。

资料仅在响应验签成功且其 OpenID 精确等于本次换码 OpenID 时采信。解析白名单只取 `nick_name`、`avatar` 和用于关联的 OpenID，不用 `user_name`、`display_name`、邮箱或手机号兜底；无效资料不得更新已有元数据。字段缺失不等于故障。

`idp.Identity` 始终不加入 token 字段。提供方函数栈内暂存 access_token，查询后丢弃；返回结果只携带已验证标识与资料声明。`user.verifyIdp` 对 ALIPAY 返回的声明使用既有 `providerProfileFrom` 净化后再进入 `completionProof`，防止 pending 保存未经净化的资料；其他提供方行为保持。

资料请求的自身超时、权限错误、畸形或坏签名可降级；如果 `ctx.Err()` 已非空则向外传播取消/超时，不拿已验证身份继续写库。资料降级与调用方取消分开记录。

备选一：先查身份和账号空字段，再通过另一个接口取资料。它可以减少重复资料请求，但需要两阶段 Provider 契约、跨调用 token 生命周期及额外读取；首版不采用。备选二：登录后后台补资料，需要持有第三方 token 和新的生命周期责任；不采用。当前方案最多增加一次有界网络调用，换取更小、可回滚的实现范围。

### 7. 仅在成功领域事务内补填

继续使用 `seedEmptyProviderProfile` 的锁和空值判断，避免增加 SQL/schema。新增调用仅发生在 ALIPAY 的已有身份成功登录分支、pending 完成且该证明已归属目标账号的分支；新身份创建继续由 `createProviderIdentity` 填充。对已有身份重复绑定只保留原有元数据更新行为，不新增借绑定反复修改账号资料的入口。

补填使用本次声明而不是历史 provider_meta。当账号两个字段都非空，helper 不发 UPDATE，也不改变资料 update_time。账号主动清空后被视作可补填，没有来源标记或永久关闭开关。

在账号既有行锁内复核字段；不把事务前快照当作是否写入的依据。登录后段、状态或唯一冲突导致失败时资料与会话一同回滚。事务有界重试复用已经验证的内存证明，不重放 code 或资料请求。pending 仅保存净化声明，完成与结果重放不再访问支付宝。

替代方案是在 `refreshProviderMeta` 内对所有提供方自动补资料；未采用，因为该函数也用于绑定，且会改变 Google/Facebook/OIDC 等的既有契约。

### 8. 观测与回归边界

追加固定 `MethodAlipay` / `DependencyVerifyAlipay` / `DegradationAlipayProfileUnavailable`，更新 `user/observability.go` 及 `observability/internal/metricdata/project.go` 的允许列表和投影测试。一次 Verify 为一次依赖逻辑调用；资料请求失败但身份成立时该调用成功并额外计降级，认证最终结果仍由领域方法决定。pending、冲突和取消不能记为最终登录成功。

延用审计事件与资源脱敏，不为支付宝添加无业务意义的事件或记录第三方原始内容。观测注入仍尽力、无配置时无额外依赖。

## Risks / Trade-offs

- 每次验证可能增加一次资料请求 → 独立 3 秒预算、失败放行、明确观测；不承诺零额外延迟，未来可单独优化为按需查询。
- UNIONID 分组错误会拒绝登录或产生不同 subject → 默认固定模式，不自动回退；真实多应用验收作为上线前置证据。
- 授权码一次性、网络超时可能已经消费 → 不盲目重试，不在事务重试中重新换码；客户端重新授权。
- 空字段自动补填也会恢复用户主动清空的资料 → 文档明确该语义，不覆盖非空字段，不声称识别用户主动清空意图。
- 支付宝权限未开通、未安装客户端或资料字段本身缺失 → 客户端记录取消/未安装处理，后端资料失败不阻断认证；不能把测试 stub 当作开通能力证据。
- 未签名的上游错误不能可信归类 → 固定内部错误及安全告警，保留失败而非为了错误文案接受未验签身份。
- 未归档 Facebook 与注册表修改交叉 → delta 保留 Facebook 完整场景；归档顺序建议 Facebook 在先，支付宝在后，禁止反向覆盖类型清单。

## Migration Plan

1. 增量加入代码与文档，无 schema/迁移变更；保留全部历史 SQL、当前初始哈希和 source-baseline。
2. 配置普通公钥与 AppID，在开放平台配置应用公钥、相应授权权限及应用分组；宿主更新 App 极简 SDK 回跳与 state 管理。
3. 在隔离环境完成已签名 mock、真实数据库并发及真实 App 联调；无真实 App 证据时明确标记未验证，不宣称端到端上线可用。
4. 使用一致配置部署全部副本；按已有 Enabled/Required 策略开启 ALIPAY。更新配置指纹会使旧 pending 重新开始，不延长或迁移其有效期。
5. 应用回退前先禁用 ALIPAY 登录并复核回退版本对未知历史 kind 的列出、解绑及匿名化兼容。回退不改已有 subject、资料、版本/dirty 或 Redis；更换模式、分组造成的身份迁移不在本变更内。

## Validation

- IdP 单元测试使用独立合成 RSA key pair 和本地 HTTP 服务，验证出站签名、原始响应字节验签、表单编码、错误节点/重复字段、重定向、大小/超时、取消和日志脱敏；不能只用生产签名辅助函数自签自验来证明正确。
- 领域测试覆盖两个 App 同 UnionID 的首次并发、OpenID 隔离、元数据冲突、首次失败后补填、用户并发 PATCH、状态/匿名化竞争、会话后段失败回滚、pending 并发归属与结果重放。保留其他 IdP 的仅首次填充回归。
- 配置和服务测试覆盖自定义前缀、多应用 PEM、显式空/非法模式、纯 ALIPAY 无 SMTP、已提供非法配置及 migrate 无运行依赖的边界。
- `GOWORK=off` 下运行 build、vet、测试；事务验证需要真实 PostgreSQL，发布前按项目完整入口验证。代码变更若需要 SQL 调整只能改手写查询并生成，不能手改 sqlc 结果。
- 真实 App 验收记录平台/SDK、权限及应用分组检查结果、是否取到预期标识、资料失败处理与登录补填，不保存真实授权码、token 或用户资料。

## Open Questions

无影响规格或任务拆分的待决策项。真实应用的权限开通、SDK 集成细节与分组返回结果在联调任务中核验；当前尚无真实联调证据。

## Implementation Notes

2026-10-24 的额外非 race 全量验证暴露既有 `TestReplaceIdentityConcurrent/same-old-identity` 的断言遗漏：赢家可能在输家读取有效会话与旧身份之间提交，输家合法返回 `ErrNotFound`，原测试只接受 `ErrInvalidToken`。[身份换绑规格](../../specs/identity-replacement/spec.md) 已明确允许并发输家按时序返回 404 或认证失效。本次仅让该测试接受这两个结果，保留恰好一个成功及身份数量检查；换绑生产代码与规格均不改变。原始失败日志保留，修复后进行真实数据库重复测试、定向 race 与全量非 race 回归，结果记入 verification.md。
