## 1. 公共 JWT 层（管理员行为不变）

- [x] 1.1 新建 `internal/accountsvc/oidcjwt`，从 adminauth 平移 discovery、JWKS 加载与校验、按 kid 缓存、单飞刷新及 RS256 解析（拒绝 jku/x5u、必需 kid、exp+iat、30 秒偏差、16 KiB 上限）；刷新间隔与初始化模式（启动即加载/延迟加载）按实例配置；冷却语义保持「任何尝试后窗口内不再拉取，上次成功 401、上次失败 503」。验证：oidcjwt 单元测试覆盖两种模式、冷却两种分支、密钥 1 小时过期、discovery issuer 不一致
- [x] 1.2 adminauth 改用 oidcjwt（启动即加载、1 分钟冷却），角色规则与 `ADMIN_*` 错误保留在 adminauth。验证：adminauth 现有测试不改断言全部通过，`go vet ./internal/accountsvc/...` 通过

## 2. 服务调用认证层

- [x] 2.1 `basicclients.Matcher` 新增 `Match(r) (clientID, ok)`，`Authorized` 改为其包装。验证：basicclients 测试覆盖返回的客户端 ID，introspection/purgefeed 现有测试通过
- [x] 2.2 新建 `internal/accountsvc/serviceauth`：按 Authorization 方案分流 Basic/Bearer，Bearer 额外校验 `exp-iat≤1h`、非空 `azp` 与本接口允许列表，返回 Outcome（OK/Unauthenticated/Forbidden/Unavailable）、ClientID、Method、BearerAttempted；认证阶段 3 秒上限；共享的 challenge 生成函数实现 D6 规则；服务 verifier 延迟加载、10 秒冷却。验证：serviceauth 单元测试覆盖 design 测试矩阵 #1–#20 及 14b/14c 的判定与 challenge（含随机 kid 并发时测试 IdP 请求次数上限）
- [x] 2.3 日志：403 记 INFO（client_id、endpoint、auth_method），刷新失败记 WARN（issuer），discovery issuer 不一致记 ERROR；不记录令牌或 Authorization。验证：日志断言测试检查不含令牌与秘密

## 3. 接口接入

- [x] 3.1 introspection 改用 serviceauth：401 `invalid_client`/`invalid_token`、403 `insufficient_scope`、503 `temporarily_unavailable`，challenge 与 no-store 按规范；仅 Basic 配置时行为与现在一致。验证：introspection 处理器测试覆盖矩阵在内省上的期望，现有测试不改通过
- [x] 3.2 purgefeed 改用 serviceauth：401 `INVALID_CLIENT`/`TOKEN_INVALID`、403 `CLIENT_NOT_AUTHORIZED`、503 `DEPENDENCY_UNAVAILABLE`+`Retry-After: 1`；两种都未配置才 503 `PURGE_FEED_NOT_CONFIGURED`。验证：purgefeed 处理器测试覆盖矩阵在拉取上的期望，现有测试不改通过

## 4. 配置与启动

- [x] 4.1 config.go 解析 D9 四个变量及校验：数组格式、两列表不相交、半配置、issuer HTTPS 且≠消费者 issuer、audience ≠ 消费者/管理员 audience/全部第三方应用与客户端 ID/任一 azp；内省改为至少一种认证；错误只指出变量名；migrate 不解析。验证：config 测试逐条覆盖并断言不回显值
- [ ] 4.2 run.go 装配：两个接口共享一个服务 verifier，启动时尝试预加载、失败只记 WARN，不影响启动与 `/readyz`。验证：run 测试在签发方不可达时服务就绪、Bearer 503、Basic 可用
- [ ] 4.3 进程级集成测试（Linux 真实二进制 + 本地 HTTPS 测试 IdP）：两个接口 Bearer 端到端各一条；签发方关闭时启动成功、Bearer 503、Basic 正常；日志不含令牌。新增顶层测试加入 `scripts/accountsvc-required-tests.txt`。验证：`scripts/verify-accountsvc.sh` 通过

## 5. 文档

- [ ] 5.1 `docs/accountsvc.md`：「业务服务错误与配置契约」增加服务调用认证小节（两种方式、错误表、challenge、调用方处理建议、签发方故障与停用客户端延迟、签名密钥轮换先发布后启用）；配置表加四个变量；Keycloak 示例（仅服务账号机密客户端、audience mapper 只勾选 "Add to access token"、令牌寿命建议 ≤5 分钟）。验证：人工核对与 specs 一致、链接有效
- [ ] 5.2 `docs/gateway-integration.md`（内部入口原样透传 Bearer，网关不得用自身令牌调用）、`docs/compatibility.md`（内省放宽为至少一种认证）、`deploy/accountsvc/.env.example`（空模板）、`examples/remoteauth` 说明。验证：人工核对，`git diff --check` 无误

## 6. 验收

- [ ] 6.1 运行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`（GOWORK=off）、`bash scripts/verify-accountsvc.sh`、`openspec validate --all --strict`，独立代码审查无 Critical；结果与未验证项（真实 Keycloak 未验收，由接入方负责）写入本变更 `verification.md`
