# Proposal

## Why

accountkit 尚未支持微博身份，接入微博开放平台移动应用 SDK 的 App 无法通过既有第三方登录接口完成微博登录。本变更按 QQ App 登录的同一边界接入微博 App SDK 登录，复用账号与会话规则，并与支付宝、QQ 一致地在登录时补填空的昵称和头像。

## What Changes

- 新增内置 `WEIBO` 提供方，第三方登录及普通绑定沿用 `kind` / `credential`，凭证为 `{"kind":"WEIBO","credential":{"app_id":"<AppKey>","access_token":"..."}}`。`app_id` 即微博开放平台的 AppKey，字段名与 QQ、微信、支付宝保持一致。客户端不提交 uid 或接口地址；SDK 回调中的 uid 与资料不被采信。
- 仅覆盖移动应用 SDK 登录：客户端/宿主集成微博 Android/iOS SDK 取得 access_token 后提交给后端。服务端调用 `POST api.weibo.com/oauth2/get_token_info` 校验令牌，要求返回的 `appkey` 等于请求的 `app_id` 且在允许列表内，`expire_in` 为负视为令牌已失效；uid 只取自该响应。
- 与 QQ、Facebook Classic 一致，微博 access_token 不做一次性限制，文档明确令牌可重放风险。
- 微博 uid 是全局且公开的用户标识，不区分应用，因此只有一种主标识 `uid:<uid>`，不引入标识模式配置，也不写入 `provider_meta` 标识元数据；跨 App 天然识别为同一账号。文档说明 uid 公开、只作标识不作持有证明。
- 配置为允许的 AppKey 列表与可选接口地址；本流程不需要 AppSecret。新增库配置、宿主可选前缀的环境变量（`WEIBO_APP_IDS`、`WEIBO_API_BASE_URL`）及 accountsvc 装配，默认不启用；`New` 不做 I/O，非法或重复配置启动前拒绝。补全配置指纹覆盖允许应用与接口地址。
- 登录时用同一 access_token 与已验证 uid 调用 `2/users/show.json` 读取 `screen_name` 与头像（依次取 `avatar_hd`、`avatar_large`、`profile_image_url`），映射为现有 name / picture 声明。资料失败、限流或解析失败不阻断已通过验证的登录，记录有界降级。头像只接受 HTTPS，不做 http 改写，通用净化规则不变。
- 登录补填提供方集合由 ALIPAY、QQ 扩展为 ALIPAY、QQ、WEIBO，规则与原子性要求不变。
- 微博响应中 `uid`、`appkey`、`expire_in` 同时接受 JSON 字符串和整数（官方文档类型说明与示例不一致），按十进制文本处理以免精度丢失；其他异常类型、重复键、尾随内容拒绝。
- 错误分类：令牌失效类错误码返回凭证无效；应用权限受限或未审核应用用户数超限按服务端配置错误返回固定消息 500；其他错误码、传输失败与坏响应返回 503。
- 扩展有界认证指标以识别微博及资料降级。微博 access_token 与原始响应不持久化、不记日志、不进入审计或对外响应。

## Non-goals

- 不实现网页、H5、PC 登录，不托管回调或 `redirect_uri`，不实现授权码兑换，不配置 AppSecret。
- 不实现微博"取消授权回调页"等提供方通知入口。
- 不开发客户端 SDK 桥接或客户端 UI；提供接入说明并验证后端契约。
- 不为 access_token 增加 nonce 或一次性登记，不增加第三方重新认证/换绑入口，不保存微博令牌或 refresh_token 做后台同步。
- 不读取邮箱等高级权限接口及性别、地区、简介等其他资料字段；不把微博昵称、头像作为身份识别依据。
- 不下载、托管头像文件，不放宽 HTTPS 头像规则。
- 无数据库结构变更、无新增迁移，不修改 `0001`、历史 SQL、冻结基线或已发布标签。

## Capabilities

### New Capabilities

- `weibo-sign-in`: 微博 App SDK 登录的接入边界、access_token 校验与应用允许列表、全局 uid 主标识、资料查询与降级语义、错误分类和令牌保护。

### Modified Capabilities

- `identity-provider-registry`: 内置类型增加 WEIBO，凭证形状增加 WEIBO（`app_id`、`access_token`），OIDC 不能占用 WEIBO；资料声明需求中的登录补填集合加入 WEIBO。保留 FACEBOOK、ALIPAY、QQ 的既有增量。
- `user-basic-profile`: 登录补填提供方集合扩展为 ALIPAY、QQ、WEIBO，头像补填说明同步。
- `accountsvc-runtime`: 增加微博配置与启动前校验，支持仅启用 WEIBO 且 Required 为空时不依赖 SMTP；保持 migrate 的运行依赖隔离。
- `standardized-metrics`: 增加有界的微博认证方法值及资料查询降级观测，不使用 AppKey 或用户标识作标签。

## Impact

- 代码涉及 `enum`、`user/idp`（新增微博提供方）、`user` 中 `refillsProfileOnSignIn` 集合、`user/identity_policy.go`、`config.go`、`accountkit.go`、`internal/accountsvc` 和 `observability`；公开 HTTP 请求结构保持，新增可选配置与内置身份类型。
- 无外部 Go 依赖新增；复用 `user/idp` 包内既有严格 JSON 解析与接口地址校验思路。
- 实施需要补充测试：凭证形状与允许列表拒绝发生在外部调用之前、`appkey` 不符、`expire_in` 为负、字符串/整数两种响应类型、上游错误码分类、异常响应拒绝、跨 App 同一 uid、资料失败与限流降级、HTTPS 以外头像丢弃、WEIBO 后续补填及并发资料更新、pending/绑定/匿名化与配置指纹。真实 PostgreSQL 验证事务行为；真实 SDK 令牌下 `get_token_info` 的字段类型与 `appkey` 取值、`users/show` 对新应用的权限与限流需要宿主联调，模拟测试不能替代。
- 文档同步 README、Go 集成及 accountsvc 配置、兼容说明和无秘密配置模板，说明 SDK 接入、uid 公开属性与令牌可重放风险。

## References

- [微博开放平台 get_token_info](https://open.weibo.com/wiki/Oauth2/get_token_info)
- [微博开放平台 users/show](https://open.weibo.com/wiki/2/users/show)
- [微博开放平台错误码](https://open.weibo.com/wiki/Error_code)

以上官方资料已在探索阶段核对；文档页面较旧（get_token_info 最后更新于 2020-05-29），真实返回类型、移动 SDK 令牌的 appkey 及资料接口权限尚未联调验证。
