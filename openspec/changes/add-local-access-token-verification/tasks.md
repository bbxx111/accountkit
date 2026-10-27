## 1. 签名与 JWKS（tokens）

- [ ] 1.1 改造 `tokens.Signer`，只支持 ES256（P-256）。
  - 签发：header 写入 `alg=ES256`、`typ=at+jwt`、`kid=<active 版本>`，claims 不变。
  - Parse：固定 `WithValidMethods(ES256)`，要求 `typ=at+jwt`，按 `kid` 选取公钥。
  - NewSigner：拒绝非 P-256 曲线和未配置的 active 版本。
  - 验证：`go test ./tokens/...`。测试须覆盖签发格式与多版本验签，并确认以下令牌均返回 `ErrInvalidToken`：HS256、用公钥作 HMAC 伪造的令牌、`none`、缺少 `typ` 或 `typ` 错误、缺少 `kid` 或 `kid` 非数字或未知、篡改的 payload、过期。
- [ ] 1.2 在 Signer 上实现 JWKS 序列化：输出全部已配置版本的公钥，字段为 `kty=EC`、`crv=P-256`、`x`、`y`、`kid`、`alg=ES256`、`use=sig`；`x`、`y` 是定长 32 字节的 base64url 编码。
  - 验证：单元测试确认输出不含 `d`，多版本按版本号排序，用 JWKS 中的公钥能验证对应版本签发的令牌。
- [ ] 1.3 实现 `JWT_KEYS` 的解析：`版本:base64(PKCS#8 DER)` 列表，转为 `*ecdsa.PrivateKey`。
  - 验证：测试确认合法 P-256 密钥可以解析；HMAC 随机字节、P-384、RSA、损坏的 base64 或 DER 都会失败，且错误信息不包含密钥内容。

## 2. 配置与门面

- [ ] 2.1 将 `Config.JWTKeys` 改为 `map[uint16]*ecdsa.PrivateKey`，同步更新 `Validate`、`ConfigFromEnv` 及 `internal/accountsvc` 中的配置名映射。
  - 验证：`config_test.go` 覆盖合法配置、active 版本缺失、非 P-256 曲线、旧 HMAC 环境变量值被拒绝，以及自定义前缀；错误信息只指出变量名。
- [ ] 2.2 在 `Auth` 上新增 `JWKSHandler()`：JWKS 在 `New` 时生成；GET/HEAD 返回 `application/json` 和 `Cache-Control: public, max-age=300`，其他方法返回 405。
  - 验证：handler 测试确认没有数据库或 Redis 依赖（故障时仍返回 200），且响应内容与 1.2 的输出一致。
- [ ] 2.3 新增共享的 ES256 测试密钥 helper，把所有使用 HS256 测试密钥的测试和夹具改为 ES256，包括根包各 config/isolation/compatibility 测试、`user/` 服务测试和 `internal/accountsvc` 夹具。
  - 验证：`GOWORK=off go test -count=1 ./...` 通过，`rg "JWTKeys.*\[\]byte"` 无残留。

## 3. 严格认证（user）

- [ ] 3.1 在 `user/query.sql` 新增按主键读取会话 `user_id`、`revoke_time` 的查询，然后执行 `sqlc generate`。
  - 验证：`bash scripts/check-generated.sh` 通过，生成差异只包含这一个查询。
- [ ] 3.2 实现导出方法 `user.Service.AuthenticateStrict`。
  - 与 `Authenticate` 共用 JWT 解析和 ID 格式校验。
  - 会话未命中、`user_id ≠ sub` 或已吊销时返回 `ErrInvalidToken`。
  - 数据库错误包装后返回，不得是 `ErrInvalidToken`；不读取 Redis。
  - 数据库访问沿用既有依赖观测包装。
  - 验证：单元测试覆盖签名无效时不访问数据库。
- [ ] 3.3 为严格认证编写真实数据库测试，需要 `SERVER_TEST_DB_DSN`。
  - 已吊销会话返回无效。
  - Redis 吊销集写入失败后：严格认证判为无效，而 `Authenticate` 仍按 fail-open 通过。
  - 会话已清理或 `sub` 不匹配时返回无效。
  - 刷新期限已到但未吊销的会话仍有效。
  - 冻结、注销、换绑、revokeOthers 提交后立即无效。
  - 数据库不可用时返回依赖错误。
  - 测试还须确认不读写来源、客户端描述或会话字段。
- [ ] 3.4 在 `httpapi/authn` 中新增 `StrictAuthenticator` 和 `RequireScopeStrict`，在门面新增 `Auth.RequireScopeStrict`。该中间件始终执行严格认证，不跳过上下文中已有的 Principal。
  - 验证：中间件测试覆盖 401 challenge、403 scope、`ErrUnavailable` 返回 503 且不调用下游、外层 RequireScope 注入后内层仍执行严格认证、与 RequireRecentAuth 串联。
- [ ] 3.5 回归 `Authenticate` 与消费者中间件。
  - 验证：既有 Redis fail-open 测试保持不变且通过，普通认证路径不新增数据库查询。

## 4. accountsvc

- [ ] 4.1 将内省装配从 `Users().Authenticate` 改为 `AuthenticateStrict`，同步更新 handler 注释。
  - 验证：introspection 测试覆盖数据库错误返回 503 `temporarily_unavailable` 并带 no-store、已吊销返回 `active:false`、有效时返回最小上下文；同步更新 `compatibility_test.go` 中关于 Redis fail-open 的断言。
- [ ] 4.2 在单监听器上挂载 `/.well-known/jwks.json`。
  - 验证：`http_test.go` 确认无需认证即可访问、内省仍要求 Basic、旧内部路径仍返回 404；gateway/transport 集成测试覆盖 JWKS 路由。
- [ ] 4.3 更新 `deploy/accountsvc` 的 Compose 和配置模板：用 PKCS#8 base64 生成 JWT 密钥（给出 `openssl genpkey` 示例），不提交真实密钥。
  - 验证：按模板生成的密钥能让 serve 在本地启动，并且 JWKS 返回该公钥。

## 5. 业务端示例

- [ ] 5.1 在 `examples/remoteauth` 中增加本地验签器。
  - 拉取 JWKS 并缓存。
  - 遇到未知 `kid` 时节流刷新；刷新失败时拒绝，并返回 `ErrUnavailable`。
  - 校验 `alg=ES256`、`typ=at+jwt`、`iss`、`aud`、`exp` 加偏差，以及 `u_`/`s_` 格式。
  - 默认要求 HTTPS，并禁止重定向。
  - 验证：负面测试覆盖 HS256、`none`、`typ` 错误、`aud` 不符、过期、未知 `kid` 刷新成功与失败。
- [ ] 5.2 在示例 handler 中演示分级：普通路由只做本地验签，关键路由先本地验签再内省，且内省结果不缓存。
  - 验证：示例测试确认会话撤销后普通路由仍可放行，关键路由立即拒绝；内省返回 503 时关键路由按依赖故障拒绝。

## 6. 文档

- [ ] 6.1 更新 README。
  - `JWT_KEYS` 配置说明改为 ES256 PKCS#8，并给出生成方法。
  - 令牌格式加入 `typ`。
  - 补充 `JWKSHandler`、`AuthenticateStrict`、`RequireScopeStrict` 的 API 说明。
  - 为嵌入式宿主单列一段：Redis 正常时 RequireScope 的吊销已是即时的，严格认证只在 Redis 读写失败时多一层保证；关键写接口本来就要写库，fail-closed 的额外代价很小。
  - Redis 失败模式表区分 Authenticate 与严格认证。
  - 会话过期一节修正“内省按 Redis 判断”的表述。
- [ ] 6.2 更新 `docs/accountsvc.md`：内省语义（以数据库为准、fail-closed）、分级接入方式、JWKS 路由、密钥生成与五步轮换顺序，并删除“每个请求都内省”和“不发放签名密钥”的旧表述。
- [ ] 6.3 更新 `docs/gateway-integration.md`，加入 JWKS 入口策略；更新 `docs/compatibility.md` 的破坏性变更说明：移除 HS256、`Config.JWTKeys` 类型变化、旧 access 失效需要刷新、内省 fail-closed，以及不提供迁移。写明完整的密钥生成命令，输出须为 PKCS#8 DER 的 base64，例如 `openssl genpkey -algorithm EC -pkeyopt ec_paramgen_curve:P-256 | openssl pkcs8 -topk8 -nocrypt -outform DER | base64 -w0`。
- [ ] 6.4 同步 AGENTS.md 中“Redis 失败模式”一条，说明严格认证和内省以数据库为准，并检查文档中的相对链接。
  - 验证：`rg "HS256|每次受保护业务请求查询内省"` 只在历史记录中出现。

## 7. 验证与记录

- [ ] 7.1 运行 `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./...`、`bash scripts/check-generated.sh`、`bash scripts/check-migrations.sh` 和 `openspec validate --all --strict`，确认全部通过。
- [ ] 7.2 提供三个一次性数据库，运行 `bash scripts/verify.sh`（Windows 下用 `tests/Dockerfile` 镜像跑 race）；新增的必需顶层测试同步写入 `scripts/required-tests.txt`。
- [ ] 7.3 编写 `verification.md`，记录实际运行的检查、结果和未验证项。
