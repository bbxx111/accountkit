# Design

## Context

动机与范围见 [proposal.md](proposal.md)，行为契约见 `specs/`。现有代码已经具有以下可复用边界：

- `user/idp.Provider` 提供 `Kind/Verify/PolicyFingerprint`，验证发生在领域事务之前；`idp.Identity` 不含第三方令牌，可安全进入 pending 加密证明。
- `user/idp/alipay.go` 已实现"身份校验 → 独立预算的资料查询 → 资料失败记降级但登录继续"的结构，以及 UNIONID/OPENID 固定模式与 `OpenIDs[app_id]` 元数据；`alipay_config.go` 有 HTTPS/回环 HTTP 地址校验。
- `user/idp/facebook.go` 的 Classic 分支是"客户端交 access token、服务端确认令牌属于本 App"的先例，不做一次性限制。
- 支付宝补填目前以三处 `kind == enum.IdentityAlipay` 判断实现：`user/service_idp.go` 中 pending 证明只保留本次净化资料，`user/service_signin_completion.go` 的已有身份登录与 pending 已归属身份两个分支调用 `seedEmptyProviderProfile`。
- 提供方登记点：`config.go` 的 `providerKinds`/配置字段/`ConfigFromEnv`，`accountkit.go` 的提供方装配，`user/identity_policy.go` 的 `builtinKindOrder`，`enum` 内置类型，`user/observability.go` 的方法值与依赖值映射，`internal/accountsvc/config.go` 的显式启用校验与变量名映射。
- 未归档的 `add-facebook-sign-in` 对注册表"身份类型以字符串表示""统一凭证格式"两条需求有 MODIFIED delta，其文本含 ALIPAY 但不含 QQ。

## Goals / Non-Goals

**Goals:**

- 在现有 Provider 接口内完成 QQ 令牌校验与资料查询，网络与领域事务分离。
- 把支付宝专属的补填判断收敛为一个提供方集合，QQ 加入后行为与支付宝完全一致，其他提供方不变。
- 头像改写只在 QQ 提供方内部完成，通用头像净化不放宽。

**Non-Goals:**

- 不把补填集合做成公开配置或 Provider 可选接口，不让宿主/OIDC 提供方加入集合。
- 不重构支付宝实现，仅替换其三处类型判断；不抽取支付宝与 QQ 的公共 HTTP 客户端。

## Decisions

### 1. 单一 QQ 提供方与凭证

新增 `idp.QQ` 实现 Provider，`Verify` 严格解码 `{app_id, access_token}`（沿用 `decodeCredential` 的未知字段/尾随内容拒绝），在任何网络调用前检查允许列表。`enum` 增加 `IdentityQQ = "QQ"`，同步 `builtinKindOrder`、`providerKinds` 与 OIDC 保留名检查。

`Config` 增加 `QQAppIDs []string`、`QQSubjectMode QQSubjectMode`、`QQAPIBaseURL string`。模式使用独立类型及 UNIONID/OPENID 常量，不复用 WeChat/Alipay 类型名，与支付宝的做法一致。

备选：作为配置化 OIDC。未采用，QQ 互联不提供 ID Token/JWKS。

### 2. 令牌校验流程

```
Verify(app_id, access_token)
  |-- 形状/允许列表检查（无 I/O）
  |-- GET {base}/oauth2.0/me?access_token=..&fmt=json[&unionid=1]   预算 10s
  |     error 字段存在 -> 错误码分类（见 4）
  |     client_id != app_id -> ErrAppNotAllowed
  |     模式所需标识缺失 -> ErrMisconfigured
  |-- subject = unionid:<u> | openid:<app_id>:<o>
  |-- GET {base}/user/get_user_info?access_token&oauth_consumer_key=app_id&openid   预算 3s
  |     失败/ret!=0/is_lost=1/解析失败 -> 记降级，资料为空
  |     成功 -> name=nickname, picture=rewrite(figureurl_qq_2 || figureurl_qq_1)
  `-- 返回 Identity（不含令牌）
```

- `unionid=1` 只在 UNIONID 模式附加：未开通权限时上游整次报 100048，OPENID 部署不能因此失败。
- `client_id` 必须等于请求的 `app_id`，而不仅是"在允许列表内"：同一实例登记多个 AppID 时，另一 App 的令牌不得以本 App 名义登录，OPENID 模式下这直接决定 subject。与 Facebook 一致返回 `ErrAppNotAllowed`。
- 响应严格解析：只接受单个 JSON 对象，拒绝重复键与尾随内容，不对 JSONP 做字符串切分。直接调用（不修改）支付宝 `alipay_json.go` 的包内严格解析函数 `alipayObject` / `alipayString`，避免重复实现同一解析，也不改动支付宝代码。
- 标识校验：openid、unionid 为 1–256 字节且不含空白或控制字符，规则与 `validAlipayIdentifier` 对齐。
- 浅拷贝宿主 HTTPClient 并禁用重定向；请求 URL 含令牌，所有传输错误经 `redactTransportErr` 去除 URL 后再记录或包装。每份响应最多读取 64 KiB。两次请求均不自动重试。

### 3. 资料映射与头像改写

资料在 `Verify` 内完成，与支付宝相同，pending 只保存净化后的声明。

头像改写函数仅在 QQ 包内：解析 URL；`https` 原样返回；`http` 且主机（小写比较）精确命中名单、无端口与 userinfo 时把 scheme 改为 `https`；其他一律返回空。之后仍交给既有 `providerProfileFrom` 净化，因此改写不能绕过长度、字符或主机校验。

初始名单为 `thirdqq.qlogo.cn`（`figureurl_qq_*` 的已知主机），以未导出常量维护，不开放配置：配置化会把"哪些 HTTP 主机可安全升级"的判断交给部署方，而错误配置会把不支持 HTTPS 的地址写入账号。名单的真实性靠宿主联调核验，见 Open Questions。

被名单拒绝的头像不计资料降级：这是可信资料中某字段不可用，与资料缺字段同类。

### 4. 错误分类

| 来源 | 条件 | 哨兵 |
|---|---|---|
| /me | 100013、100014、100015、100016（令牌非法/过期/撤销/校验失败） | `ErrInvalidCredential` |
| /me | 100048（未开通 UnionID） | `ErrMisconfigured` |
| /me | 其他 error 码 | `ErrUnavailable` |
| /me | client_id 与 app_id 不符 | `ErrAppNotAllowed` |
| /me | 无 error 但缺 client_id/模式所需标识，或标识格式非法 | `ErrMisconfigured` |
| 传输 | 网络错误、超时、3xx、429、5xx、非 JSON、超限 | `ErrUnavailable` |
| get_user_info | 任意失败 | 不返回错误，记 `qq_profile_unavailable` |

QQ 互联不对响应签名，可信性依赖 HTTPS 与固定主机，因此与微信一致把未知错误码与坏响应视为依赖不可用（503），而不是支付宝那种"未签名错误按固定 500"。实施时以官方公共返回码页面核对 100013–100016 的含义并固化为合成样本；未列入表的码不做字符串猜测。

调用方上下文已取消时，资料阶段结束后返回 `ctx.Err()`，不吞掉取消继续登录，与支付宝相同。

### 5. 补填集合收敛

在 `user` 包新增未导出判定 `refillsProfileOnSignIn(kind enum.IdentityKind) bool`，集合为 ALIPAY、QQ；替换现有三处 `== enum.IdentityAlipay`。语义不变：pending 证明只保留本次净化资料；已有身份登录与 pending 已归属身份分支在刷新元数据后调用 `seedEmptyProviderProfile`。注释从"Alipay"改为"登录补填提供方"。

备选：Provider 增加可选接口声明是否补填。未采用：扩大公开契约，且宿主提供方无法证明其资料可信度满足本规则；需要时可另开变更。

支付宝现有测试保持原样作为回归；QQ 增加对应的补填、并发 PATCH、pending 与回滚测试，复用支付宝测试的构造方式。

### 6. 配置与装配

| 库配置 | 环境变量后缀 | 规则 |
|---|---|---|
| QQAppIDs | QQ_APP_IDS | 逗号分隔，1–20 位 ASCII 数字，不重复、无空项；空表示禁用 |
| QQSubjectMode | QQ_SUBJECT_MODE | 未设置为 UNIONID；显式只接受两个大写值 |
| QQAPIBaseURL | QQ_API_BASE_URL | 默认 `https://graph.qq.com`；HTTPS，HTTP 仅数值回环地址 |

`Config.Validate` 与 `NewQQ` 共用纯函数校验，不做 I/O，错误只指出配置项。回环地址校验在 QQ 配置文件内按与 `normalizeAlipayGateway` 相同的规则实现，不修改支付宝函数。

`PolicyFingerprint` 覆盖排序去重后的 AppID、模式与规范化接口地址；无秘密可泄露。accountsvc 在显式启用 QQ 而列表为空时以 `ACCOUNTKIT_QQ_APP_IDS` 报错，并把库校验错误映射到 `ACCOUNTKIT_QQ_*` 变量名。

### 7. 观测

`observability` 增加方法值 `qq`、依赖值 `verify_qq`、降级场景 `qq_profile_unavailable`，在 `user/observability.go` 两处映射加 QQ 分支，并同步 metricdata 的有界取值集合与项目测试。QQ 提供方接收 Observer 的方式与支付宝相同。

## Risks / Trade-offs

- [令牌可重放：泄露的 access_token 在约 60 天有效期内可用于登录] → 与 Facebook Classic 一致接受该风险；`client_id == app_id` 防止跨应用令牌；文档要求客户端只经 HTTPS 提交、不记录令牌。
- [`unionid=1` 在 UNIONID 模式下对未开通权限的部署整体失败] → 归为配置错误并在启动文档中显著说明；单 App 部署可显式选 OPENID。
- [头像域名名单可能不完整或 HTTPS 不可用] → 名单外直接丢弃不影响登录；宿主联调清单核验真实返回的主机与 HTTPS 可访问性，结果记入 verification.md 后再调整名单。
- [QQ 不签名响应，信任边界仅为 HTTPS 与固定主机] → 禁止重定向、接口地址只允许 HTTPS（回环测试除外），严格解析响应。
- [补填集合收敛可能改变支付宝行为] → 只替换判断表达式，支付宝既有测试全部保持并必须通过。
- [未归档的 Facebook delta 晚于本变更归档会以不含 QQ 的文本覆盖注册表两条需求] → 本变更归档前 MUST 先归档 `add-facebook-sign-in`；若顺序无法保证，先在 Facebook delta 中补入 QQ 文本。

## Migration Plan

无数据库结构变化，无迁移。部署方按需设置 `QQ_APP_IDS` 与模式后重启；未设置时行为不变。回退到不含 QQ 的版本后，已有 QQ 身份按"未登记的历史类型"规则可列出、解绑、匿名化但不能登录。上线后切换模式会使既有 QQ 身份无法被命中，文档按微信/支付宝同样的风险说明处理。

## Open Questions

- `figureurl_qq_*` 在当前 QQ 互联实际返回的主机集合及其 HTTPS 可用性：需真实账号联调确认，结果只调整名单常量，不改变规格或任务结构。
- 移动 SDK 取得的 access_token 调用 `/me` 时返回的 `client_id` 是否恒为移动应用 AppID：预期如此（官方文档称 SDK 与 Web 接口返回字段一致），由宿主联调确认。
