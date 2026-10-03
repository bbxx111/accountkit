# accountsvc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付可选 accountsvc，支持 SMTP 邮箱登录、消费者远程内省及默认关闭的 OIDC 管理员认证。

**Architecture:** 单 Go module；服务装配位于 internal/accountsvc，主入口只负责命令和信号。三项独立 adapter 先实现并审查，再装配生命周期和真实依赖验收。领域行为以 OpenSpec 为准。

**Tech Stack:** Go 1.26.5、Chi、pgx/v5、go-redis/v9、golang-jwt/jwt/v5 v5.3.1；SMTP 使用标准库 net/smtp，连接取消由 net.Conn deadline/Close 保障。

**Spec:** [add-accountsvc](../../../openspec/changes/add-accountsvc/design.md)，同时读取该 change 的 proposal、tasks 及四份 specs。

## Global Constraints

- 仅当前任务分支 feat/add-accountsvc；不修改冻结 SQL、原 manifest、生成数据库代码和其他仓库。
- `ACCOUNTKIT_` 保持库配置，服务配置 `ACCOUNTSVC_`；库不得依赖服务。
- 生产双监听器 TLS、SMTP 认证和加密；开发明确选择 development。
- 保留消费者吊销 fail-open、刷新宽限、审计异步、迁移锁；不得以测试方便改变语义。
- 所有行为先写测试，观察预期失败，再最小实现；新能力真实集成测试缺依赖必须失败。
- 用户已经要求提交并实施；沿用仓库默认子代理执行方式，计划和进度可随时审阅，不重复索要实施授权。
- 只提交本任务文件，提交由协调者执行；不推送、不合并、不归档。

## Review Focus

- SMTP DATA 已接受后 QUIT 失败不得造成可重试的假失败；Task 1 测试固定为成功且不重发。
- OIDC 上游错误和未知 kid 混合并发不得无限拉取或把基础设施失败当 401；Task 2 控制时钟和服务器响应验证。
- 内省请求 query/body 重复 token、凭据错误和超限同时出现时必须先认证，再只解析 body；Task 3 协议测试验证。
- 信号与第二监听器失败不得在 HTTP 排空前关闭审计，且总退出预算必须有界；Task 4/5 验证真实进程和资源顺序。
- 从嵌入式迁移至服务不能跳过业务匿名化责任；Task 6 文档和测试矩阵明确禁止混跑有业务回调的维护实例。

## Dependencies

SMTP 复用 Go 标准库 BSD-3-Clause 实现；不使用自动机会式 TLS 的 SendMail 快捷方法，显式 DialContext、TLS、AUTH、Data。JWT 使用仓库现有 v5.3.1（MIT），明确 WithValidMethods、WithIssuer、WithAudience、WithExpirationRequired、WithIssuedAt、WithLeeway；HTTP 获取与 JWKS 缓存由服务 adapter 按规格实现。无新增运行时外部依赖。

依据：[net/smtp](https://pkg.go.dev/net/smtp)、[jwt v5.3.1](https://pkg.go.dev/github.com/golang-jwt/jwt/v5@v5.3.1)。net/smtp 无 context API，故必须在所有 SMTP 读写期间保持 deadline 和取消关闭；不能仅给 Dial 加超时。两项许可证在本地 Go/module 缓存核验。

### Task 1: 验证码发送能力（OpenSpec 2.1–2.4）

**Files:** 新增 user/sender/disabled.go、user/sender/smtp/{smtp.go,smtp_test.go}；修改 user/service_signin.go、httpapi/consumer/response.go；增加 sender、user、consumer 对应测试。

**Interfaces:**
- 产出 `sender.Disabled{}` 实现 SMSSender/EmailSender/Enabled() bool；`sender.ErrDisabled`、`sender.ErrUnavailable`。
- 产出 `smtp.Config{Host string, Port int, From, Username, Password, TLSMode string, Timeout time.Duration, TLSConfig *tls.Config}`；`smtp.New(Config) (*Sender,error)`、`(*Sender).SendEmail(context.Context,string,sender.Message) error`。
- 服务层负责 production 禁止 none/无认证；SMTP adapter 拒绝不安全 TLSConfig（InsecureSkipVerify），支持自定义信任根供隔离 TLS 测试。

- [ ] 写 TestDisabledChannel/旧接口兼容/三种用途/发送失败额度测试，先运行 `go test ./user/... ./httpapi/consumer` 观察新增断言失败。
- [ ] 最小实现禁用前置检查与错误映射；旧普通发送错误行为不变。
- [ ] 写 SMTP TCP fixture 行为测试，覆盖模板、认证、两类 TLS、超时取消、拒绝、头注入、DATA 接受后 QUIT 失败；先失败后实现显式协议调用。
- [ ] 运行 `go test ./user/... ./httpapi/consumer` 与 `go vet ./user/... ./httpapi/consumer`；报告红绿证据、接口及边界。协调者独立审查后提交。

### Task 2: OIDC 管理员验证器（OpenSpec 5.1–5.4 的 adapter）

**Files:** 新增 internal/accountsvc/adminauth/{config.go,verifier.go,jwks.go,claims.go,*_test.go}。

**Interfaces:**
- 产出 `adminauth.Config{Issuer,Audience,RolesClaim,UsernameClaim string}`、`Config.Validate() error`。
- 产出 `adminauth.Options{HTTPClient *http.Client, Now func() time.Time, OnForbidden func(*http.Request,accountkit.AdminPrincipal)}`。
- `adminauth.New(ctx context.Context,cfg Config,opts Options) (*Verifier,error)` 首次发现并加载；Verifier.Middleware(next http.Handler) http.Handler、RequireRole(string) func(http.Handler)http.Handler；`adminauth.PrincipalFrom(context.Context)(accountkit.AdminPrincipal,bool)`。
- 回调通过闭包捕获稍后创建的 Auth；无包级全局，保持测试隔离。

- [ ] 写受控 HTTPS discovery/JWKS 和签名 token 测试，先失败：RS256/RSA 2048、iss/aud/sub/exp/nbf/iat、roles JSON Pointer、无效头、消费者/ID token 拒绝。
- [ ] 实现配置验证、受限 HTTP 下载、JWKS 解析和现有 jwt/v5 验证；拒绝重定向降级及 token 提供的密钥地址。下载上限 256 KiB、默认总超时 10s。
- [ ] 写可控时钟缓存测试：硬过期 1h、未知 kid 刷新 60s 节流、并发合并、成功未知 401/失败未知 503；实现有界缓存。
- [ ] 写 middleware/role/拒绝审计测试并实现，super-admin 包含 operator；运行 `go test ./internal/accountsvc/adminauth`、vet，独立审查后提交。

### Task 3: 消费者内省与接入示例（OpenSpec 4.1–4.4）

**Files:** 新增 internal/accountsvc/introspection/{handler.go,clients.go,*_test.go}；examples/remoteauth/{client.go,client_test.go}。

**Interfaces:**
- `introspection.ParseClients(string)(map[string][]string,error)` 校验 JSON ID→base64 秘密列表，每个解码后至少32字节；拒绝空/重复 ID和空集合。
- `introspection.New(clients map[string][]string, authenticate func(context.Context,string)(user.Principal,error)) (http.Handler,error)`；不创建签名器，使用注入的 Users().Authenticate。
- remoteauth 为示例包，不是必需库依赖，提供可注入 http.Client 的 Client.Introspect 和示例受保护 handler，用户凭据与调用凭据分别传输。

- [ ] 先写 Basic 认证、轮换、重复/空 token、query/body、hint、64KiB、Content-Type、active 最小响应和 no-store 测试；运行确认失败。
- [ ] 实现常量时间摘要比较、严格 body 表单解析、safe OAuth 错误；ErrInvalidToken→active:false，其他意外错误→503，不泄露原错误。
- [ ] 使用真实 tokens/user.Service/miniredis 验证过期、跨实例、吊销和 fail-open 一致性（不查账号状态）。
- [ ] 编写示例测试固定每请求查询、失败拒绝、scope/近期认证/所属检查；实现并运行 `go test ./internal/accountsvc/introspection ./examples/remoteauth` 与 vet，审查后提交。

### Task 4: 服务运行装配（OpenSpec 3.1–3.5、6.1）

**Files:** internal/accountsvc/{config.go,run.go,http.go,metadata.go,shutdown.go,*_test.go}；cmd/accountsvc/{main.go,main_test.go}。

**Interfaces:**
- 消费 Tasks 1–3 的准确接口。
- 产出 `accountsvc.LoadConfig(command string)(Config,error)`，内部使用环境；`accountsvc.Run(ctx context.Context,args []string,logger *slog.Logger) error` 由 main 传 signal.NotifyContext。
- Config 保存库 Config、运行模式、URL/地址/TLS、startup/shutdown 超时、可信 CIDR、SMTP、内省客户端、AdminEnabled/Admin 配置。
- 可测试的 HTTP 路由构造与依赖生命周期 helper 保持 internal，不为测试添加公共库 API。

- [ ] 先写配置表驱动测试：生产默认、migrate 最小配置、非法 URI/秘密/TLS/模式/管理员配置、关闭预算大于15s；实现安全错误和配置校验。
- [ ] 写代理链、请求 ID、两个路由面、64KiB/超时、健康探测测试；实现 defaults 5s/15s/30s/60s，ready 总预算2s。
- [ ] 写资源工厂失败/关闭顺序测试后实现 New→Migrate→admin→listen×2→Start；migrate 使用 Disabled，不启动后台。
- [ ] 实现有界 shutdown：先 no-ready，两个 HTTP 面同时排空，15s 留给库关闭，超限取消请求且非零退出；根库 Close 与连接关闭必须在请求终止后。
- [ ] 运行服务包测试、vet及 `go build ./cmd/accountsvc`；确认无参数=serve、无效参数非零、main日志脱敏；审查后提交。

### Task 5: 真实依赖与部署交付（OpenSpec 6.2–6.5）

**Files:** internal/accountsvc/integration_test.go、tests/accountsvc/Dockerfile（或根服务专用 Dockerfile）、deploy/accountsvc/{compose.yaml,.env.example}、scripts/verify-accountsvc.sh、scripts/accountsvc-required-tests.txt、.github/workflows/ci.yml。

**Interfaces:** 服务测试连接由 `SERVER_TEST_DB_DSN`、`ACCOUNTSVC_TEST_REDIS_URL` 注入；测试启动隔离 SMTP TCP 接收器和 HTTPS OIDC fixture，不需要真实服务商。严格脚本检查外部依赖与测试通过数，smtp fixture 绑定失败必须 fail。

- [ ] 写真实邮件→登录→refresh 宽限→内省→reauth→注销→旧token失效测试；再测试 disabled SMS、admin off、重启和 HTTP/SMTP TLS。
- [ ] 写管理端真实数据库测试覆盖冻结/解冻、角色拒绝、reveal和审计脱敏；多副本/dirty/未知密钥启动测试保持隔离 schema。
- [ ] 实际构建二进制，Linux 子进程验证 SIGTERM 正常关闭与超预算失败；测试进程输出和 SMTP错误不含秘密。
- [ ] 交付非 root 服务镜像、回环开发 Compose、占位变量文件；`docker compose config` 与镜像启动/邮件捕获验证实际通过。
- [ ] 接入严格脚本与 CI：缺DB/Redis即失败，必需测试不允许skip；执行真实 Redis + SMTP fixtures、race及旧库verify/recovery。

### Task 6: 文档、全量审查及证据（OpenSpec 6.6–6.7）

**Files:** docs/accountsvc.md、README.md、AGENTS.md、docs/{compatibility,release-checklist,development}.md，当前 change 的 tasks/design。

- [ ] 以最终接口更新配置表、serve/migrate/Compose命令、远程接入、管理员 RS256/claims、TLS、匿名化及混跑限制；检查命令和链接。
- [ ] 汇总各任务测试与独立审查，记录必要设计细化；只在对应验收全部通过后勾选 OpenSpec。
- [ ] 全量 build/vet/unit/race、sqlc consistency、历史迁移、真实服务/恢复验证、OpenSpec strict；证据记录在 docs，缺外部提供方联调如实列出。
- [ ] 最终整分支独立审查，集中修复并定向复核；用户仅授权提交实施，完成后不自动推送、合并或归档。
