# 服务接口 Bearer 认证实现计划

> 执行方式：本会话内顺序实施（任务间接口依赖紧密：oidcjwt → serviceauth → 处理器 → 配置/装配），每步 TDD，结束时一次独立代码审查。

**Goal:** 内省与已清理用户拉取在 Basic 之外接受客户端凭据 Bearer 令牌，按 `azp` 逐接口授权。

**Spec:** `openspec/changes/archive/2026-10-28-add-service-bearer-auth/`（proposal、design、specs、tasks）。本计划只描述实现顺序与接口，需求以该 change 为准。

## Global Constraints

- 只接受 RS256；拒绝 `jku`/`x5u`；必需 `kid`、`exp`、`iat`；固定 30 秒偏差；令牌 ≤16 KiB；`exp - iat` ≤ 1 小时；`azp` 非空。
- 服务刷新冷却 10 秒，管理员保持 1 分钟；冷却语义：任何尝试后窗口内不再拉取，上次成功 401、上次失败 503。
- 依赖失败一律 503，绝不 401。日志不含令牌、秘密、Authorization。
- adminauth 现有测试不改断言；仅 Basic 配置下 introspection/purgefeed 现有测试不改。
- 配置错误只指出变量名；migrate 不解析新变量。

## Review Focus

1. 冷却与单飞：随机 kid 并发不放大 IdP 请求；延迟加载在 discovery 未成功时同样受冷却约束。
2. 503 与 401 的边界：IdP 超时、discovery issuer 不一致、密钥过期后刷新失败都必须 503。
3. challenge：多个 Authorization 头时不读取任何一个、不带 `error`；仅 Basic 启用时携带 Bearer 只回 Basic challenge。
4. 配置隔离：audience 与所有第三方 ID、消费者/管理员 audience、任一 azp 冲突；两 azp 列表不相交；半配置。
5. 管理员行为不回归：启动即加载失败仍阻止启动；首个未知 kid 立即刷新（初始加载不计入冷却）。

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/accountsvc/oidcjwt/{verifier,jwks,issuer}.go` | discovery、JWKS 加载与校验、缓存/单飞/冷却、RS256 解析 |
| `internal/accountsvc/adminauth/*.go` | 改用 oidcjwt；保留 Config、角色、`ADMIN_*` 错误 |
| `internal/accountsvc/basicclients/basicclients.go` | 新增 `Match` |
| `internal/accountsvc/serviceauth/serviceauth.go` | 方案分流、azp 授权、Result、challenge |
| `internal/accountsvc/introspection/handler.go`、`purgefeed/handler.go` | 新增 `NewWithAuthenticator`，原 `New` 作为仅 Basic 包装 |
| `internal/accountsvc/config.go` | 四个新变量与校验；管理员解析抽成函数以便之后校验服务令牌 |
| `internal/accountsvc/run.go` | 共享服务 verifier、启动预加载（失败只 WARN） |

## 接口

```go
// oidcjwt
var ErrInvalid, ErrUnavailable error
type Options struct { HTTPClient *http.Client; Now func() time.Time; RefreshInterval time.Duration; Logger *slog.Logger }
func ValidIssuer(s string) bool
func New(issuer, audience string, opts Options) (*Verifier, error) // 无 I/O
func (v *Verifier) Load(ctx context.Context) error                  // discovery+JWKS，不记录冷却尝试
func (v *Verifier) Parse(ctx context.Context, raw string) (jwt.MapClaims, error)

// serviceauth
type Outcome int // OK, Unauthenticated, Forbidden, Unavailable
type Result struct { Outcome Outcome; ClientID, Method string; BearerAttempted bool }
type Config struct { Realm, Endpoint string; Basic map[string][]string; TokenClients []string; Tokens *oidcjwt.Verifier; Logger *slog.Logger }
func New(cfg Config) (*Authenticator, error)
func (a *Authenticator) Enabled() bool
func (a *Authenticator) Authenticate(r *http.Request) Result
func (a *Authenticator) Challenges(res Result) []string
```

## 任务顺序（对应 tasks.md）

1. **oidcjwt（1.1）**：先写测试（延迟加载首个请求触发 discovery+JWKS；冷却成功→ErrInvalid、失败→ErrUnavailable；1 小时过期；issuer 不一致→ErrUnavailable；单飞；算法/jku/x5u/kid/16 KiB），再从 adminauth 平移实现。
2. **adminauth（1.2）**：改用 oidcjwt（New 内 Load；RefreshInterval 1 分钟），跑现有测试。
3. **basicclients.Match（2.1）**。
4. **serviceauth（2.2、2.3）**：测试矩阵 #1–#20、14b、14c 的判定与 challenge；403 INFO 日志断言不含令牌。
5. **处理器（3.1、3.2）**：两接口各一组 Bearer 测试，断言状态码、错误码、`WWW-Authenticate` 行、`Cache-Control`、`Retry-After`。
6. **配置与装配（4.1、4.2）**：config 测试逐条；run 预加载失败只 WARN。
7. **进程集成（4.3）**：复用 `integrationFixture.oidc()` 的 HTTPS IdP；新增顶层测试并加入必需清单。
8. **文档（5.1、5.2）**与**验收（6.1）**。
