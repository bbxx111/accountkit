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

服务端声明对应 ID、启用 `basic` 并只授 `tokens.introspect`；仅做内省的业务不需要清理权限。Basic-only 不需要 OIDC issuer/audience。原逐接口部署仍可用 `INTROSPECTION_BASIC_CLIENTS` 配置对应凭据；请勿将非空旧配置与统一主列表混用。配置为启动快照，轮换秘密或撤销权限须重启/排空全部服务副本。

Bearer-only 或双方式服务也可以调用内省，但此示例 `Client` 只实现 Basic。Bearer 调用方自行用 IdP client secret 向提供方申请 `client_credentials` 访问令牌，再以 `Authorization: Bearer <access_token>` 调用 accountsvc；令牌的已验证 `azp` 必须对应启用 bearer 且拥有内省权限的服务身份。IdP secret 与 Basic 密码分开管理，服务不自动回退认证方式。完整配置与决策表见[服务认证手册](../../docs/accountsvc.md#服务调用认证内省与已清理用户拉取)。

默认要求 HTTPS 并校验证书。`AllowHTTP` 仅供显式选择的本地开发或受控 TLS 代理/网格保护的应用段；生产调用链仍须提供经过验证的 TLS。服务自身 `TLS_ENABLED=false` 不自动修改调用方设置。网络错误、非 200 或畸形内省响应为 `ErrUnavailable`，应拒绝当前关键业务请求并报告依赖不可用；`active:false` 表示消费者令牌无效，不能将这两种结果混为用户退出。

运行示例测试：`GOWORK=off go test -count=1 ./examples/remoteauth`。Compose 统一模式的 override、旧模式迁移和回退见[部署说明](../../docs/accountsvc.md#构建与运行)。
