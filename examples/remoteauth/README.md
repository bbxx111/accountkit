# 业务服务远程鉴权示例

普通路由通过 `Verifier` 本地验证消费者 ES256 access token；关键路由由 `ProtectedHandler` 每次调用内省，以数据库已提交的会话吊销状态为准。业务服务仍负责资源归属、scope 与近期认证校验。此示例不缓存内省的 active 结果。

`Client` 使用 HTTP Basic 调用 `POST /v1/introspect`，消费者 access token 只放入表单 `token`，不能作为调用方 Authorization。统一服务客户端部署可选择 [.env.example](../../deploy/accountsvc/.env.example) 的 Basic-only 示例：

```go
client, err := remoteauth.NewClient(remoteauth.Config{
    Endpoint: "https://accounts.example.test/v1/introspect",
    ClientID: "business-service",
    ClientSecret: basicSecret, // 从调用方密钥系统读取的 Base64 原文本。
})
```

服务端声明对应 ID、启用 `basic` 并只授 `tokens.introspect`；仅做内省的业务不需要清理权限。Basic-only 不需要 OIDC issuer/audience。旧逐接口配置已移除，非空白旧变量会阻止启动；升级前须显式转换身份、方法与权限。配置为启动快照，轮换秘密或撤销权限须重启/排空全部服务副本。

Bearer-only 或双方式服务也可以调用内省，但此示例 `Client` 只实现 Basic。Bearer 调用方自行用 IdP client secret 向提供方申请 `client_credentials` 访问令牌，再以 `Authorization: Bearer <access_token>` 调用 accountsvc；令牌须按 profile 对应启用 bearer 的客户端，绑定机器 sub，且注册权限和 scope 都包含 tokens.introspect。IdP secret 与 Basic 密码分开管理，服务不自动回退认证方式。完整配置与决策表见[服务认证手册](../../docs/accountsvc.md#服务调用认证内省与已清理用户拉取)。

默认要求 HTTPS 并校验证书。`AllowHTTP` 仅供显式选择的本地开发或受控 TLS 代理/网格保护的应用段；生产调用链仍须提供经过验证的 TLS。服务自身 `TLS_ENABLED=false` 不自动修改调用方设置。网络错误、非 200 或畸形内省响应为 `ErrUnavailable`，应拒绝当前关键业务请求并报告依赖不可用；`active:false` 表示消费者令牌无效，不能将这两种结果混为用户退出。

运行示例测试：`GOWORK=off go test -count=1 ./examples/remoteauth`。单文件 Compose、旧配置迁移和旧二进制回退见[部署说明](../../docs/accountsvc.md#构建与运行)。

服务权限目录包含 `tokens.introspect`、`purged_users.read`、`identities.resolve`，三项互不隐含。Basic 仅使用注册权限 P；Bearer 无条件使用 P∩S（S 为 token scope），缺失/空 scope 不授权，没有旧兼容模式。`ACCOUNTSVC_SERVICE_TOKEN_PROFILE` 选择 `default`（默认，以 `azp` 识别客户端）或 `rfc9068`（以 `client_id` 识别客户端，要求 `at+jwt`/`application/at+jwt` 及 `iss/aud/exp/sub/client_id/iat/jti`），不猜测或回退。二者都须将已验证 `sub` 精确绑定客户端 `BEARER_SUBJECTS` JSON 字符串数组，不裁剪字符串。提供方须保证主体专用于机器并关闭客户端人类登录；profile、客户端 ID 或自报 `grant_type` 均不证明机器身份，不假设 `sub=client_id`。需要所有请求均受令牌权限收窄时只启用 bearer：同一客户端持有 Basic 秘密仍可获得 P。

认证与授权先于方法、内容类型、请求体和查询参数处理。缺失/无效凭据、profile/机器主体不匹配、scope 类型或语法错误、已登记客户端未启用请求方式返回 401；有效 Bearer 客户端未登记，或已认证客户端缺少注册权限/所需 scope 返回 403；能力未配置、签发方或业务依赖不可用返回 503。全部服务响应 `Cache-Control: no-store`。401 按注册表启用方式分别给出 challenge，仅单一无效 Bearer 附 `invalid_token`；Bearer 403 只有 Bearer `insufficient_scope`，Basic 403 与所有 503 无 challenge。
