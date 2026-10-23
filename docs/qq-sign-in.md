# QQ App SDK 登录

本指南覆盖 Android/iOS 原生 App 通过 QQ 互联移动 SDK 登录，以及 accountkit/accountsvc 的既有第三方登录与绑定接口。库不提供网页授权、回调或授权码兑换。库的自动验收使用正式 HTTP 入口、实际 QQ 提供方、本地模拟 QQ 接口及真实 PostgreSQL/Redis，不依赖终端设备。本文末尾的真机 SDK、UnionID 打通和头像主机检查属于宿主上线前接入清单，当前尚未执行。

## 平台准备

在 [QQ 互联](https://connect.qq.com/) 创建移动应用，取得 AppID。App SDK 校验 access_token 不需要 AppKey，库不保存任何 QQ 密钥。

跨 App 共用账号时，在同一开发者账号下申请 UnionID 打通（[UnionID 介绍](https://wiki.connect.qq.com/unionid%E4%BB%8B%E7%BB%8D/)，同一开发者最多打通 60 个应用），并实测同一用户在各 App 返回相同 unionid。未开通时 QQ 的 `oauth2.0/me` 返回 100048（未设置 companyID）。只有一个 App 且未开通 UnionID 的部署应显式配置 `OPENID`。本地配置校验无法验证 QQ 互联后台的打通状态。

## 客户端流程

1. 集成 QQ 互联 Android/iOS SDK 并完成登录，取得本次 `access_token`。
2. 仅经 HTTPS 提交统一凭证，不提交 SDK 返回的 openid、unionid 或资料：

```json
{"kind":"QQ","credential":{"app_id":"1100000001","access_token":"synthetic-sdk-access-token"}}
```

   对宿主挂载的终端用户 API 发起 `POST /users:signInWithIdp`（accountsvc 默认完整路径为 `/v1/users:signInWithIdp`），设备沿用 `X-Device-Id` / `X-Device-Name`。credential 严格只接受非空 `app_id` 和 `access_token`。普通绑定使用既有 `POST /users/me/identities`，解绑/换绑继续要求近期认证。
3. 不要记录、缓存或上报 access_token。QQ 令牌在有效期内可被重复用于登录（与 Facebook Classic 相同，不是一次性凭证），泄露即等同于登录凭据泄露。库也不会在日志、审计、错误或响应中输出它。

## 错误

`CREDENTIAL_INCOMPLETE`、`IDP_APP_NOT_ALLOWED`（含另一 App 的令牌，`/me` 返回的 `client_id` 与提交的 `app_id` 不一致）、`IDP_CREDENTIAL_INVALID`（`/me` 返回 100013 失效、100014 过期、100015 废除或 100016 验证失败）为 400；网络、5xx、超时或未知上游错误码为 503 `IDP_UNAVAILABLE`；UNIONID 模式下账号未开通 UnionID（100048）或响应缺少所需标识，为固定消息 500。前端不要把所有失败都当作令牌过期，503 可稍后重试，400 应重新走 SDK 登录。

## Go 装配

根包公开 `QQAppIDs`、`QQSubjectMode`（`QQSubjectModeUnionID` / `QQSubjectModeOpenID`）和测试用 `QQAPIBaseURL`：

```go
cfg.QQAppIDs = []string{"1100000001"}
cfg.QQSubjectMode = accountkit.QQSubjectModeUnionID
cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityQQ}
cfg.RequiredIdentityKinds = []enum.IdentityKind{}
```

`New` 只校验和装配，不访问 QQ，不做 I/O。AppID 为 1–20 位 ASCII 数字且不得重复；模式缺省为 UNIONID，只接受 `UNIONID` / `OPENID`；接口地址缺省 `https://graph.qq.com`，HTTP 仅允许数值回环测试地址。启用 QQ 不改变 Required：省略时仍为 PHONE/EMAIL 二选一，QQ 资料不能当作邮箱/手机证明。纯 QQ 账号须显式 Required 为空并显式只启用 QQ，无需短信或邮件发送器；需要邮箱补全时启用 EMAIL、QQ，Required 设 EMAIL，并装配邮件发送器。普通绑定/解绑、会话来源、账号状态及重新认证规则见 [Go 集成指南](go-integration.md)。

## accountsvc 配置

库使用 `ConfigFromEnv(prefix)` 的宿主前缀；accountsvc 固定为 `ACCOUNTKIT_`：

```text
ACCOUNTKIT_QQ_APP_IDS=1100000001,1100000002
ACCOUNTKIT_QQ_SUBJECT_MODE=UNIONID
ACCOUNTKIT_QQ_API_BASE_URL=https://graph.qq.com
ACCOUNTKIT_ENABLED_IDENTITY_KINDS=QQ
ACCOUNTKIT_REQUIRED_IDENTITY_KINDS=[]
```

应用列表缺省/空表示不配置，已配置列表不允许重复或空项。模式未设置时为 UNIONID，显式空白、小写和其他值拒绝。`ACCOUNTKIT_QQ_API_BASE_URL` 一般无需设置。serve 显式 Enabled 含 QQ 却无应用时启动失败并指出 `ACCOUNTKIT_QQ_APP_IDS`，非法值只报告变量名，不输出配置值。migrate 无需配置 QQ、不调用 QQ，但显式提供的非法配置仍会被静态校验拒绝。

[本地 Compose](../deploy/accountsvc/compose.yaml) 默认 EMAIL；仅编辑 `.env` 不会自动把任意 IdP 变量传进容器。切换时用 Compose override 或实际部署的环境注入，显式覆盖 Enabled/Required 并传入每个 QQ 配置。

## 身份、资料与头像

UNIONID → `unionid:<unionid>`；OPENID → `openid:<app_id>:<openid>`，不在模式间回退或合并，缺少所需标识拒绝，也不按昵称、头像合并账号。UNIONID 模式下各 App 的 OpenID 合并记录在身份元数据中。模式或 AppID 分组变更须独立评估，不会自动迁移或合并旧账号，所有副本保持一致配置。pending 指纹覆盖排序后的 AppID、模式与接口地址，配置变化使旧 pending 必须重新开始。

每次验证先以最多 10 秒调用 `oauth2.0/me`，再以独立最多 3 秒调用 `get_user_info`，均受调用方更早 deadline 限制；每份响应最多 64 KiB，不跟随重定向，不重试。资料失败、`ret` 非 0 或 `is_lost=1` 不阻断登录，仅记 `qq_profile_unavailable` 降级。外层取消/超时终止整体操作，不会降级继续建会话。

头像优先 100px `figureurl_qq_2`，缺失时用 40px `figureurl_qq_1`；`thirdqq.qlogo.cn` 的 http 地址升级为 https，其他 http 地址丢弃，名单以内部常量维护，不开放配置。首次及后续成功登录按字段补填仍为空的显示名和头像，不覆盖非空值；两个字段均非空时不更新资料时间。重复绑定已有身份只更新元数据，不触发补填。库不下载头像，不保存 QQ token，不负责头像持续同步。观测口径见 [指标指南](metrics.md)。

本变更无 SQL/schema 或迁移变化。禁用 QQ 只停止新验证；历史身份仍可按现有规则列出、解绑和匿名化。

## 验收边界

`TestQQHTTPLoginContract` 与 `TestQQHTTPMultiAppIdentityContract` 经正式门面、终端用户 handler、实际 QQ Provider、本地模拟接口和真实 PostgreSQL/Redis 验证契约。它们不证明真实 SDK 行为、UnionID 打通返回或头像主机 HTTPS 可用性，模拟值不会被描述为真实平台返回。

## 宿主联调清单（未执行）

只保留脱敏结论，不保存 access_token 或用户资料。此清单是宿主责任记录，不计入库的 OpenSpec 实施进度。

- [ ] 真机 Android/iOS SDK 登录后提交令牌，后端登录成功
- [ ] 移动 SDK 令牌调用 `/me` 返回的 `client_id` 等于移动应用 AppID
- [ ] 两个已打通 App 对同一用户返回相同 unionid
- [ ] QQ `/me` 对无效或过期令牌返回的 HTTP 状态确认为 200 加 JSON 错误体（当前非 200 一律映射为 503 IDP_UNAVAILABLE）
- [ ] 实际 `figureurl_qq_*` 主机均在名单内，且对应 https 地址可访问
