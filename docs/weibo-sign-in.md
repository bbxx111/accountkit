# 微博 App SDK 登录

本指南覆盖 Android/iOS 原生 App 通过微博开放平台移动 SDK 登录，以及 accountkit/accountsvc 的既有第三方登录与绑定接口。库不提供网页授权、回调、取消授权通知或授权码兑换。库的自动验收使用正式 HTTP 入口、实际微博提供方、本地模拟微博接口及真实 PostgreSQL/Redis，不依赖终端设备。本文末尾的真机 SDK、接口字段与头像检查属于宿主上线前接入清单，当前尚未执行。

## 平台准备

在 [微博开放平台](https://open.weibo.com/) 创建移动应用，取得 AppKey。App SDK 校验 access_token 不需要 AppSecret，库不保存任何微博密钥。凭证与配置中的 `app_id` 即该 AppKey，字段名与 QQ、微信、支付宝保持一致。

应用未通过审核前只允许有限的测试账号授权；超出时令牌校验返回 21321，库按服务端配置错误返回固定 500，需要在开放平台添加测试账号或提交审核，而不是让用户重试。

## 客户端流程

1. 集成微博 Android/iOS SDK 并完成授权，取得本次 `access_token`。
2. 仅经 HTTPS 提交统一凭证，不提交 SDK 返回的 uid、refresh_token 或资料：

```json
{"kind":"WEIBO","credential":{"app_id":"1234567890","access_token":"synthetic-sdk-access-token"}}
```

   对宿主挂载的终端用户 API 发起 `POST /users:signInWithIdp`（accountsvc 默认完整路径为 `/v1/users:signInWithIdp`），设备沿用 `X-Device-Id` / `X-Device-Name`。credential 严格只接受非空 `app_id` 和 `access_token`。普通绑定使用既有 `POST /users/me/identities`，解绑/换绑继续要求近期认证。
3. 不要记录、缓存或上报 access_token。微博令牌在有效期内可被重复用于登录（与 QQ、Facebook Classic 相同，不是一次性凭证），泄露即等同于登录凭据泄露。库也不会在日志、审计、错误或响应中输出它。

## 错误

| 情况 | 结果 |
|---|---|
| credential 缺字段或含未知字段（如 uid） | 400 `CREDENTIAL_INCOMPLETE` |
| `app_id` 未登记，或 `get_token_info` 返回的 `appkey` 与 `app_id` 不一致（另一 App 的令牌） | 400 `IDP_APP_NOT_ALLOWED` |
| 令牌已使用、过期、撤销、拒绝、授权收回或无效（21314、21315、21316、21317、21319、21327、21332），或 `expire_in` 为负 | 400 `IDP_CREDENTIAL_INVALID` |
| 需更高授权、应用权限受限、未审核应用超出用户数（10005、10014、21321），或成功响应缺少必需字段 | 固定消息 500 |
| 网络、超时、3xx、429、5xx、未知错误码或无法解析的响应 | 503 `IDP_UNAVAILABLE` |

前端不要把所有失败都当作令牌过期：503 可稍后重试，400 应重新走 SDK 授权，500 需要运维处理。

## Go 装配

根包公开 `WeiboAppIDs` 和测试用 `WeiboAPIBaseURL`：

```go
cfg.WeiboAppIDs = []string{"1234567890"}
cfg.EnabledIdentityKinds = []enum.IdentityKind{enum.IdentityWeibo}
cfg.RequiredIdentityKinds = []enum.IdentityKind{}
```

`New` 只校验和装配，不访问微博，不做 I/O。AppKey 为 1–20 位 ASCII 数字且不得重复；接口地址缺省 `https://api.weibo.com`，HTTP 仅允许数值回环测试地址。启用微博不改变 Required：省略时仍为 PHONE/EMAIL 二选一，微博资料不能当作邮箱/手机证明。纯微博账号须显式 Required 为空并显式只启用 WEIBO，无需短信或邮件发送器；需要邮箱补全时启用 EMAIL、WEIBO，Required 设 EMAIL，并装配邮件发送器。普通绑定/解绑、会话来源、账号状态及重新认证规则见 [Go 集成指南](go-integration.md)。

## accountsvc 配置

库使用 `ConfigFromEnv(prefix)` 的宿主前缀；accountsvc 固定为 `ACCOUNTKIT_`：

```text
ACCOUNTKIT_WEIBO_APP_IDS=1234567890,1234567891
ACCOUNTKIT_WEIBO_API_BASE_URL=https://api.weibo.com
ACCOUNTKIT_ENABLED_IDENTITY_KINDS=WEIBO
ACCOUNTKIT_REQUIRED_IDENTITY_KINDS=[]
```

应用列表缺省/空表示不配置，已配置列表不允许重复或空项。`ACCOUNTKIT_WEIBO_API_BASE_URL` 一般无需设置。serve 显式 Enabled 含 WEIBO 却无应用时启动失败并指出 `ACCOUNTKIT_WEIBO_APP_IDS`，非法值只报告变量名，不输出配置值。migrate 无需配置微博、不调用微博，但显式提供的非法配置仍会被静态校验拒绝。

[本地 Compose](../deploy/accountsvc/compose.yaml) 默认 EMAIL；仅编辑 `.env` 不会自动把任意 IdP 变量传进容器。切换时用 Compose override 或实际部署的环境注入，显式覆盖 Enabled/Required 并传入微博配置。

## 身份、资料与头像

微博 uid 全局唯一，subject 固定为 `uid:<uid>`，不包含 AppKey，也没有标识模式配置；同一用户经任一已登记 App 登录都命中同一身份，无需类似 UnionID 的打通申请。身份元数据不写 unionid 或按应用的 openid。uid 是公开标识（可见于用户主页地址），库只把它当作识别值：主标识只取自服务端令牌校验结果，客户端无法提交 uid，也不按昵称、头像合并账号。pending 指纹覆盖排序后的 AppKey 与接口地址，增删 AppKey 使旧 pending 必须重新开始，但不改变既有身份命中；所有副本保持一致配置。

每次验证先以最多 10 秒 POST `oauth2/get_token_info`，再以独立最多 3 秒 GET `2/users/show.json`，均受调用方更早 deadline 限制；每份响应最多 64 KiB，不跟随重定向，不重试。官方文档对 `uid`、`appkey`、`expire_in` 的类型说明与示例不一致，库同时接受 JSON 字符串与整数并按十进制文本比较，不经浮点转换。`expire_in` 为正时作为凭证剩余期限，会限制 pending 登录补全的最长有效期；为 0 或超出可表示范围时按未知期限处理。资料失败、限流、返回用户与 uid 不符或解析失败不阻断登录，仅记 `weibo_profile_unavailable` 降级。外层取消/超时终止整体操作，不会降级继续建会话。

昵称取 `screen_name`；头像依次取 `avatar_hd`、`avatar_large`、`profile_image_url` 中第一个非空值，只接受 HTTPS，不做协议改写。选中的字段为 HTTP 时丢弃头像，不回退到其他尺寸，也不计降级。资料填充遵循统一规则：新建账号始终填充；已有账号的后续登录在 `PROVIDER_PROFILE_BACKFILL` 开启（默认）时补填空字段。不覆盖非空值；两个字段均非空时不更新资料时间。库不下载头像，不保存微博 token 或 refresh_token，不负责头像持续同步。观测口径见 [指标指南](metrics.md)。

本变更无 SQL/schema 或迁移变化。禁用微博只停止新验证；历史身份仍可按现有规则列出、解绑和匿名化。

## 验收边界

`TestWeiboHTTPLoginContract` 与 `TestWeiboHTTPMultiAppIdentityContract` 经正式门面、终端用户 handler、实际微博 Provider、本地模拟接口和真实 PostgreSQL/Redis 验证契约。它们不证明真实 SDK 行为、接口真实字段类型或头像协议，模拟值不会被描述为真实平台返回。

## 宿主联调清单（未执行）

只保留脱敏结论，不保存 access_token 或用户资料。此清单是宿主责任记录，不计入库的 OpenSpec 实施进度。

- [ ] 真机 Android/iOS SDK 授权后提交令牌，后端登录成功
- [ ] 移动 SDK 令牌调用 `get_token_info` 返回的 `appkey` 等于移动应用 AppKey，记录 `uid`/`appkey`/`expire_in` 实际是字符串还是数字
- [ ] 两个已登记 App 对同一用户返回相同 uid
- [ ] 无效、过期或已撤销令牌的实际 HTTP 状态与 `error_code`（预期 21327 或 21332 等）
- [ ] `users/show` 对新应用可用，记录频率限制下的错误码
- [ ] 实际返回的头像字段均为 HTTPS 且可访问
