# SDK 真实服务测试宿主

此目录只提供测试工具，不属于 SDK 运行时。宿主通过公开 `accountkit.New → Migrate → Start → Close` 装配真实库，复用 accountsvc 的 PoolConfig、默认本地审计、管理 OIDC verifier 和两 HTTP 面边界。它是嵌入式宿主；不能将其验证结果标为生产 accountsvc 进程验证。

从仓库根目录运行（PowerShell）：

```powershell
docker compose -p accountkit-sdk-fixture-20261101 -f sdk/testsupport/compose.yaml up -d --wait
$env:GOWORK = 'off'
$env:SDK_FIXTURE_DATABASE_URL = 'postgres://sdk_fixture:synthetic-only@127.0.0.1:15439/sdk_fixture?sslmode=disable'
$env:SDK_FIXTURE_REDIS_ADDR = '127.0.0.1:16389'
go test -count=1 -tags integration ./sdk/testsupport/fixture
go run ./sdk/testsupport/fixture
```

默认监听 `127.0.0.1:18081`；`SDK_FIXTURE_ADDR` 可指定其他 loopback 端口。数据库和 Redis 必填，连接、迁移或管理员发现失败均退出失败。必需集成入口使用 `-tags integration`，没有 skip 路径。默认单元测试不能替代该入口。

控制接口均为测试专用，仅绑定本机，无鉴权、无持久化秘密，不得部署：

| GET 路径 | 返回 |
| --- | --- |
| `/__fixture/ready` | origin、api_prefix、admin_prefix、profile、baseline 和 mode |
| `/__fixture/code?target=...&purpose=SIGN_IN` | 最近一次真实发送器收到的 code；未发送为 404 |
| `/__fixture/admin-token?role=operator` | 合成管理员 access_token；super-admin 同时带 operator 角色 |
| `/__fixture/credential?kind=APPLE&subject=123456789&nonce=unique-value` | `{kind,credential}`，直接交给 SDK 的登录/绑定方法 |
| `/keys` | 管理员和受控提供方 RSA JWKS |

用途为 `SIGN_IN`、`BIND`、`REAUTH`、`SIGN_IN_COMPLETE`。target 必须 URL 编码。nonce 每次验证唯一；Apple fixture 将 token 内 nonce 转为 SHA256，API 中保留原 nonce。

credential kind 支持 `APPLE`、`GOOGLE`、`FACEBOOK`（Limited）、`TEST_OIDC`、`WECHAT`、`WECHAT_MINI`、`QQ`、`WEIBO`、`ALIPAY`、`FACEBOOK_CLASSIC`。后两种微信形状 wire kind 都为 WECHAT，Facebook 两种形状 wire kind 都为 FACEBOOK。所有 provider 都经过生产 verifier；fixture 仅合成远端响应和签名。微博固定合成 uid 123456789。这些结果不是平台真实原生授权证据。

默认 API 前缀 `/v1`、`/admin/v1`，扩展资料未配置，相关调用应保留 503。`SDK_FIXTURE_PROFILE=extended` 启用 `theme` / `nickname` 字符串属性；`custom` 使用 `/custom/account` 和 `/custom/admin`。这些配置不能与默认生产 accountsvc 的能力混写。默认嵌入 profile 使用 sdk_fixture schema 和 Redis 前缀；并行运行时须按下文显式配置独立schema/prefix。重置使用下方销毁/重建整个一次性环境，不手工清业务表。

SDK 契约套件必须通过 SDK 公开方法调用服务；此处 Go 测试只证明测试基础设施就绪、依赖失败和控制接口形状，不能算 SDK 契约测试。

结束后先 Ctrl+C 停宿主，再只清理本任务项目：

```powershell
docker compose -p accountkit-sdk-fixture-20261101 -f sdk/testsupport/compose.yaml down --volumes
```

Compose 数据存于 tmpfs，清理后不可恢复。固定 loopback 端口被占用时启动失败，不应停止其他容器。不要把连接串改为现有产品数据库。

`SDK_FIXTURE_PROFILE=accountsvc` 启动真实 `internal/accountsvc.Run`（development 模式，EMAIL 通道，默认本地审计与未启用扩展资料）。控制宿主仍在 18081，真实 accountsvc 在 `127.0.0.1:18082`（可用 `SDK_FIXTURE_SERVICE_ADDR` 改端口），`/__fixture/ready.origin` 指向真实服务，`control_origin` 指向控制接口。该模式使用独立 `sdk_fixture_service` schema/Redis前缀和纯本机 SMTP 捕获；控制宿主的可选 IdP 配置不代表 accountsvc 启用了这些提供方。管理员 token 由控制接口发放，真实 accountsvc 通过 discovery/JWKS 验证。

该模式按进程设置 `ACCOUNTSVC_` 测试环境变量，仅在专用测试进程执行；不要在宿主业务进程内调用。运行时还包含一个供控制接口使用的嵌入实例，其 HTTP 数据不与 accountsvc 数据混用。后续 SDK 场景必须使用 ready 中的 origin。SMTP 控制码按内置中文/英文模板识别用途，定制邮件模板不在本工具范围内。

## 独立嵌入实例与身份策略

嵌入 profile 支持以下显式环境变量，省略时保留原默认值：

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `SDK_FIXTURE_SCHEMA` | `sdk_fixture` | 库表所在的独立 PostgreSQL schema |
| `SDK_FIXTURE_REDIS_PREFIX` | `sdk_fixture:` | 独立 Redis key prefix，必须以冒号结尾 |
| `SDK_FIXTURE_REQUIRED_KINDS` | 未指定（库默认 PHONE/EMAIL） | `EMAIL`、`PHONE`、`PHONE,EMAIL`，或 `[]` 显式取消 required kinds |
| `SDK_FIXTURE_PROFILE` | 默认嵌入 | `custom-extended` 同时开启自定义前缀和 theme/nickname 扩展属性 |

这些变量仅用于嵌入 profile；accountsvc profile 收到这些显式参数会失败，防止静默忽略。未知 profile、非法/重复 required kind、非法 schema 和无冒号 Redis prefix 也会启动失败。

给第三个隔离实例使用的 PowerShell 环境（保留18081/18082）：

```powershell
$env:GOWORK = 'off'
$env:SDK_FIXTURE_DATABASE_URL = 'postgres://sdk_fixture:synthetic-only@127.0.0.1:15439/sdk_fixture?sslmode=disable'
$env:SDK_FIXTURE_REDIS_ADDR = '127.0.0.1:16389'
$env:SDK_FIXTURE_ADDR = '127.0.0.1:18084'
$env:SDK_FIXTURE_SCHEMA = 'sdk_fixture_extended'
$env:SDK_FIXTURE_REDIS_PREFIX = 'sdk_fixture_extended:'
$env:SDK_FIXTURE_PROFILE = 'custom-extended'
$env:SDK_FIXTURE_REQUIRED_KINDS = '[]'
go run ./sdk/testsupport/fixture
```

该实例真实API为 `http://127.0.0.1:18084/custom/account`、`/custom/admin`，控制接口仍在同origin的 `/__fixture/*`。扩展 JSON Schema 声明 Draft 2020-12。

NeedsBinding 测试先在 `[]` 策略下通过公开SDK登录第三方，保存合成subject；正常停止此实例后，只将 `SDK_FIXTURE_REQUIRED_KINDS` 改为 `EMAIL` 并在相同地址/schema/prefix重启。重新取得新nonce和credential，以同kind/subject登录现有账号，预期 `user:bind` scope。绑定新EMAIL后通过refresh同步为 `user`。重启会更换测试JWT签名密钥，必须重新登录，旧access不能作为重启后的凭据。

Pending 测试在 EMAIL 策略下使用全新subject登录，预期pending；调用发送补全验证码与完成补全，purpose为 `SIGN_IN_COMPLETE`，预期得到 `user` scope。这两种流程与状态不可互换。新增 `TestEmbeddedPolicyFixturesRequired` 通过公开Go领域方法证明fixture能生成这些情形；仍需SDK真实请求单独验收。

多个实例必须同时区分监听地址、schema及Redis prefix；不要仅修改端口。默认值为兼容保留，平行测试应始终显式设置隔离参数。共享的一次性Compose数据库退出前保留，后续按项目整体销毁；测试工具不自动DROP schema或清空Redis。
