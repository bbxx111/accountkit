# Verification

本文记录 `add-local-access-token-verification` 在分支 `feat/local-access-token-verification` 上实际执行的检查（2026-10-27）。原始输出保存在被忽略的 `.test-output/verify/` 下。

## 执行方式

- 按[实现计划](../../../docs/superpowers/plans/2026-10-27-add-local-access-token-verification.md)在本会话内顺序实施，没有使用逐任务子代理。原因：tokens → 配置 → 全部测试夹具 → 严格认证 → accountsvc 之间接口强依赖，并且共享写入点。
- 每组都先写失败测试，再写实现（TDD）。
- 全部实现完成后，由一个独立审查代理复核整条分支。

## 已运行检查

| 检查 | 结果 |
|---|---|
| `GOWORK=off go build ./...`、`go vet ./...` | 通过 |
| `GOWORK=off go test -count=1 ./...` | 通过。44 个包，0 失败。审查修复后在提交 `76fc203` 上复跑，输出见 `.test-output/verify/go2.log`。未设置 `SERVER_TEST_DB_DSN`，数据库用例 skip，详见下文“未验证项”。 |
| `bash scripts/check-migrations.sh` | 通过：Migration history is immutable |
| `openspec validate --all --strict` | 37 项全部通过 |
| 部署文档中的密钥生成命令 | 已核对：用 `openssl genpkey … \| openssl pkcs8 -topk8 -nocrypt -outform DER \| base64 -w0` 生成的值能被 `tokens.ParseKeyList` 接受（P-256）；`openssl rand -base64 32` 生成的旧 HMAC 值被拒绝，错误信息不回显输入 |
| `examples/remoteauth` | `-count=5` 稳定通过 |

`scripts/required-tests.txt` 新增 8 个必需测试：根包 `TestJWKSHandler`；tokens 包的 `TestParseKeyList*`、`TestJWKS*`；user 包的 `TestAuthenticateStrict*`；authn 包的 `TestRequireScopeStrict`。

## 审查结论

独立审查代理复核了 `ce23c22..78d2d30`，结论如下：

- 无 Critical 问题。
- 确认以下部分与规格一致：ES256 与 `typ` 校验、算法混淆防护、JWKS 定长坐标、`AuthenticateStrict` 语义、`RequireScopeStrict` 的响应一致性与嵌套时重新认证。
- 确认 `Authenticate` 与 `RequireScope` 未变。

各项意见的处理（修复在提交 `76fc203`）：

**Important**

1. 示例 `Verifier` 在拉取 JWKS 时持有全局锁，且失败的拉取不计入节流，随机 `kid` 可以放大请求或造成 DoS。**已修复：**
   - 网络 I/O 期间不持锁，已缓存的密钥照常验签；
   - 并发请求共享一次拉取；
   - 失败的尝试也计入节流；
   - 单个调用方取消不会让共享的拉取失败。

   新增 `TestVerifierThrottlesFailedFetches`、`TestVerifierDoesNotHoldLockDuringFetch`。
2. 示例缓存的密钥被无限期信任。**已修复：**
   - 新增 `MaxCacheAge`（默认 5 分钟）与 `StaleGrace`（默认 10 分钟）；
   - 被移除的密钥在缓存过期并刷新后被拒绝；
   - 拉取持续失败时，超过宽限期 fail-closed。

   新增 `TestVerifierBoundsCacheAge`。
3. `TestAuthenticateStrictDatabaseState` 只在子测试中 skip，无库时父测试会 PASS 并绕过门禁。**已修复：** 父测试在缺少 `SERVER_TEST_DB_DSN` 时自身 skip。
4. 规格与任务缺口：
   - 换绑未覆盖。**已补充：** 在 `TestReplaceIdentityRedisRevocationFailure` 中断言：吊销集写入失败时，严格认证拒绝被撤销的会话，并保留当前会话。
   - “`verifyLocally` 未被调用”不成立：`service_integration_test.go` 第 32 行有调用。
   - “`active:true` 响应体断言丢失”不成立：`introspection/handler_test.go` 的协议测试仍校验完整的最小响应体。

**Minor**

1. 节流期内的未知 `kid` 返回 401。部分采纳：上次拉取失败时改为 503；上次拉取成功但缺少该 `kid` 时仍为 401，`docs/accountsvc.md` 写明了需按轮换顺序等待。
2. 空密钥集清空缓存。已修复：视为拉取失败，新增 `TestVerifierKeepsKeysOnEmptySet`。
3. 依赖错误丢失取消信息。已修复：改用 `%w: %w`。
4. 严格查询没有独立超时。已修复：内省 handler 为校验设置 3 秒上限，新增 `TestLookupIsBounded`；嵌入式宿主的请求超时仍由宿主控制，已记入 design。
5. 不一致的 P-256 密钥对能通过 `Validate`。已修复：`CheckKeys` 校验私钥标量与公钥是否匹配，新增 `mismatched pub` 用例。
6. 指标未区分严格认证与普通认证。接受，作为取舍写入 design 的 Risks。
7. `docs/go-integration.md` 没有指向严格认证。已补充。

## 设计偏离

**严格认证没有新增 sqlc 查询**，改为复用已有的 `GetActiveSessionByIDAndUser`（`id`、`user_id`，`revoke_time IS NULL`，不加锁、不判刷新期限），语义与需求一致。因此 `user/db/` 无变化。design.md 的 D5 已更新。

## 数据库与完整验证（Docker 启动后补充）

**环境**

- 专为本任务创建的一次性 Docker 资源：PostgreSQL 17.4 容器 `accountkit-verify-pg-20261027`，以及 Redis 7 容器 `accountkit-verify-redis-20261027`，使用合成凭据，只绑定回环端口。
- 库内建了 `accountkit_strict`（集成测试）、`accountkit_recsrc` 和 `accountkit_recdst`（恢复源与恢复目标，均为空库）。
- 没有使用本机其他项目的数据库容器。
- Linux 验证镜像由 `tests/Dockerfile` 构建（golang:1.26.5，PostgreSQL 17.11 客户端）。

**Windows 本机全量测试**

`SERVER_TEST_DB_DSN` 指向真实库，执行 `go test -count=1 -json ./...`。结果：1086 通过，0 失败，14 skip。skip 包括 13 个需在 Linux 上由 `verify-accountsvc.sh` 运行的进程测试，以及预期 skip 的 `TestRecoveryFixture`。`TestAuthenticateStrictDatabaseState` 的全部子测试均实际执行并通过，包括：

- 吊销集写入失败时严格认证拒绝，而 `Authenticate` 仍 fail-open；
- 刷新期限已到但未吊销的会话仍有效；
- `sub` 不符，或会话已被清理；
- revokeOthers、冻结、注销后立即失效；
- 严格认证不写库。

换绑断言（`TestReplaceIdentityRedisRevocationFailure`）与既有的 Redis fail-open 回归测试也全部通过。

**首轮 Linux 运行发现两个问题，均已修复**

1. `TestServiceIntegrationDependencyFailure` 仍要求“Redis 故障时内省日志中出现 fail-open 警告”。这是旧契约，内省已不读 Redis。测试改为：
   - 用普通 Bearer 请求（`GET /v1/users/me`）断言 fail-open 警告；
   - 在 Redis 故障期间调用 `POST /v1/revoke` 后，断言内省立即返回 `active:false`。

   修复提交为 `c4523c6`，并在端到端层面验证了核心保证。
2. 恢复演练的 verify 阶段失败（`restored durable result replay`）。原因是 `internal/testkeys` 在每个进程中随机生成签名密钥，而演练的 seed、verify、source-unchanged 是三个独立进程，需要相同的密钥。修复提交为 `4f63610`：改为按序号经 SHA-256 确定性派生，并新增 `TestJWTDeterministicAndDistinct`，已列为必需测试。

**最终提交 `4f63610` 上的 Linux 运行**（重建了空的恢复库）

- `bash scripts/verify.sh`：退出码 0。依次通过：
  - 迁移历史检查；
  - build 与 vet；
  - `go test -race`：1100 通过，0 失败，无数据竞争。2 个 skip，分别为预期 skip 的 `TestRecoveryFixture`，以及 `TestProfileAttributesUnreadableFile`（容器以 root 运行，文件权限无法制造不可读）；
  - 必需测试门禁；
  - pg_dump/pg_restore 恢复演练三阶段（“Recovery drill passed”）。
- `bash scripts/verify-accountsvc.sh`：退出码 0。`internal/accountsvc`、`examples/remoteauth` 与 SMTP 共 114 个测试在 race 下通过，同样只 skip `TestProfileAttributesUnreadableFile`，必需测试门禁通过。其中进程级集成测试验证了：
  - 生产模式 serve 加载 PKCS#8 ES256 密钥；
  - 通过真实的 `/.well-known/jwks.json` 本地验签登录所得 access（`verifyLocally`）；
  - 撤销后内省返回 `active:false`。

原始输出位于 `.test-output/verify/`、`.test-output/tests.jsonl`、`.test-output/accountsvc-tests.jsonl` 和 `.test-output/recovery.1ERavt/`。

## 生成代码检查

用户在本机安装 sqlc v1.31.1 后，`bash scripts/check-generated.sh` 退出码为 0，生成结果无差异，工作区保持干净。

## 未验证项

无。本记录中的全部检查均已在本地实际执行。
