## Context

动机见 proposal.md。与实现相关的现状：

- `tokens.Signer` 只支持 HS256，以 `map[uint16][]byte` 存放版本化密钥，`kid` 为十进制版本号；Parse 按 `kid` 选钥并限定算法。
- 吊销集位于 `session/revocation`，键为 `<prefix>revoked:<sid>`，TTL 为 AccessTTL 加偏差。`user.Service.revokeInSet` 写入失败只记 Warn；`Authenticate` 读取失败时 fail-open。
- `internal/accountsvc/introspection` 只做协议适配，依赖注入的 `auth.Users().Authenticate`：返回 `ErrInvalidToken` 时映射为 `active:false`，其他错误映射为 503 `temporarily_unavailable`。
- 吊销的真相源是 `session.revoke_time`。冻结、注销、换绑、撤销其他设备和单个撤销都通过同一事务写入，提交后才写 Redis。
- 目前没有正式部署，用户确认不考虑 HS256 配置或令牌的迁移。

## Goals / Non-Goals

**Goals：**
- 内省的吊销判定只依赖已提交的数据库状态，不因 Redis 写入或读取失败而放行。
- 普通接口可以完全离线验签，只依赖缓存的 JWKS。
- 只保留一种签名算法，消除算法混淆的攻击面。

**Non-Goals：**
- 不改变 `Authenticate`、消费者中间件和 Redis 吊销集的语义。
- 不提供正式的业务端 Go 校验包，`examples/remoteauth` 仅作示例。
- 不做按业务服务细分 audience、令牌交换（RFC 8693）或 DPoP。
- 内省不实时重算账号状态或 scope。
- 不新增数据库迁移；不提供 HS256 兼容、过渡期或迁移工具。

## Decisions

### D1 只用 ES256，移除 HS256

业务服务可能使用 Go 以外的语言。ES256 在主流 JWT/JWKS 库中支持最广，签名短，`golang-jwt/jwt/v5` 已内置。不选 EdDSA，是因为部分语言生态的 JWKS 支持不完整；不选 RS256，是因为密钥和签名更大，且没有收益。

不保留 HS256 的理由：没有正式部署需要兼容。同时保留两种算法需要“按版本选择算法”、混合版本轮换和防混淆校验，复杂度和攻击面都会增加。ES256 验签开销约为几十微秒，在当前规模下可以忽略。

备选方案是保留 HS256 供嵌入式宿主使用，被否决：两套签名路径都要长期维护，且嵌入式宿主也会失去发布 JWKS 的能力。

### D2 配置：沿用字段名和环境变量名，改变类型和内容

- `Config.JWTKeys` 的类型改为 `map[uint16]*ecdsa.PrivateKey`。Go 宿主会在编译期发现这项变化，比运行期静默误解更安全。
- `JWTActiveKey`、`JWTIssuer`、`JWTAudience` 保持不变。
- 环境变量 `JWT_KEYS` 沿用 `版本:base64` 列表格式，值改为 PKCS#8 DER。旧的 HMAC 随机字节会在 PKCS#8 解析阶段失败，报错指出变量名，不输出值。
- 校验时只接受 P-256 曲线。

备选方案是改名为 `JWT_ES256_KEYS`，被否决：只有一种算法时，名称里带算法属于冗余；而且旧值本来就会被明确拒绝，不会被误用。

### D3 `typ: at+jwt` 全量强制

所有令牌都由 ES256 签发，因此 accountkit 自身的 Parse 也要求 `typ=at+jwt`，与业务端的本地验签规则一致（RFC 9068），防止其他 JWT 被当作 access 使用。`WithValidMethods` 固定为 ES256。

### D4 JWKS 由库输出，服务负责挂载

库在门面 `Auth` 上提供 `JWKSHandler() http.Handler`，与 `EndUserHandler()`、`AdminHandler()` 风格一致，由宿主自行挂载。JWKS 在 `New` 时由配置生成并缓存，不做 I/O。accountsvc 将其挂载在根路径 `/.well-known/jwks.json`，不放在 `/v1` 下：它不是消费者 API，且这个路径是业界约定。响应头为 `Cache-Control: public, max-age=300`；轮换步骤 2 的等待时间以此为下限，再叠加业务端自己的缓存时间。

### D5 严格认证作为 `user.Service` 的新方法

新增导出方法 `AuthenticateStrict(ctx, token) (Principal, error)`，名称在实现时可以微调。流程：

1. 与 `Authenticate` 共用 JWT 解析和 ID 格式校验；
2. 按 `sid` 查询会话；
3. 未命中、`user_id ≠ sub` 或 `revoke_time IS NOT NULL` 时返回 `ErrInvalidToken`；
4. 数据库错误包装为 `user.ErrUnavailable` 后返回，内省和严格中间件都映射为 503。

该方法不读 Redis：数据库已经是真相源，再读 Redis 只增加故障面，不增加信息。数据库故障时不回退到 Redis，选择 fail-closed：此时登录、刷新、撤销都已不可用，关键接口暂时返回 503 是可以接受的代价，换来的语义是 `active:true` 一定经过数据库确认。

查询复用已有的 sqlc 查询 `GetActiveSessionByIDAndUser`（`id = $1 AND user_id = $2 AND revoke_time IS NULL`，走主键，不加锁，不开事务，不判刷新期限），未命中即视为无效。实施时发现该查询语义与需求完全一致，因此不再新增查询，也无需重新生成 sqlc 代码（对原计划的偏离）。观测沿用 `Authenticate` 的认证操作观测（`authenticate` 操作，吊销时记 `session_revoked` 原因）；现有观测体系没有数据库依赖组件，其他数据库查询也不单独观测，本变更不新增组件或指标名。

内省装配从 `auth.Users().Authenticate` 改为 `AuthenticateStrict`，并在内省 handler 中为校验设置 3 秒上限（审查后补充），使数据库挂起时返回 503，而不是一直占用请求直到调用方放弃；嵌入式宿主的请求超时仍由宿主控制。handler 的协议映射不变，因为它已经把非 `ErrInvalidToken` 的错误映射为 503。

备选方案是在内省 handler 中直接查库，被否决：违反“业务规则集中在 `user.Service`”的约定，嵌入式宿主也无法复用。

### D5a 严格认证中间件

嵌入式宿主的需求（来自 ai-food 的反馈）：如果宿主自行组装关键路由的认证，Bearer 解析、challenge、错误体和 scope 检查很容易与 `RequireScope` 不一致。因此 `httpapi/authn` 新增以下内容：

- `StrictAuthenticator` 接口；
- `RequireScopeStrict(a StrictAuthenticator, allowed ...string)`：与 `Bearer` 共用内部实现，区别是始终重新认证，不跳过上下文中已有的 Principal；
- 门面方法 `Auth.RequireScopeStrict(allowed...)`。

`AuthenticateStrict` 把数据库错误包装为 `user.ErrUnavailable`。现有 `Bearer` 已经把该错误映射为 503，因此 503 映射无需新增分支。

如果沿用“已有 Principal 就放行”的逻辑，在外层用 `RequireScope`、内层用严格中间件时，内层不会再认证，严格认证就失效了，所以严格中间件不跳过已有的 Principal。

### D6 不检查刷新期限和账号状态

刷新期限已到但未吊销的会话，其 access 仍然有效。这与 session-expiry 规范和本地验签一致，避免内省与 Authenticate 对同一令牌给出不同的时效结论。冻结、注销等账号状态变更已经在同一事务中吊销了全部会话，只查会话行即可覆盖，不需要 join `user_account`。

## Risks / Trade-offs

- [数据库故障期间，关键接口的内省返回 503] → fail-closed 的预期代价，已写入接入说明；普通接口走本地验签，不受影响。
- [内省每次多一次主键查询] → 只有关键接口调用，调用量远低于原先的“每次请求都内省”；查询不持锁。
- [本地验签的吊销窗口最长为 AccessTokenTTL 加偏差] → 规范中明确写出。需要缩短时，部署方可以调小 AccessTokenTTL（下限 1m），代价是刷新更频繁。
- [业务端漏校验 `alg`、`typ`、`aud`，导致接受伪造或跨实例的令牌] → 规范列出必需的校验项，示例给出参考实现和负面测试。
- [升级瞬间所有已签发的 access 失效] → 客户端收到 401 后用 refresh token 刷新，refresh token 不受影响；当前没有正式部署，影响限于开发环境。
- [ES256 私钥泄露] → 沿用现有的密钥注入方式和不记日志的约束；轮换流程不变。

- [严格认证与普通认证共用 `authenticate` 观测操作] → 同一请求先经 `RequireScope` 再经 `RequireScopeStrict` 会被计数两次，指标上无法区分两者；这是 D5 的有意取舍，不新增指标名，需要区分时另行设计。

## Migration Plan

不提供迁移。升级时宿主或部署方生成 P-256 私钥（PKCS#8），替换原 `JWT_KEYS` 配置后重启；旧 access 失效，客户端刷新即可。回退到旧版本需要恢复旧的 HS256 配置，此时 ES256 签发的 access 失效，客户端同样通过刷新恢复。
