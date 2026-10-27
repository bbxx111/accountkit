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

## 未验证项

本机没有可用的 PostgreSQL（Docker Desktop 报告 “unable to start”，WSL 中未安装 PostgreSQL），也没有 sqlc 和 cgo 工具链。以下各项**未实际执行**，对应任务保持未勾选：

- **任务 3.3、3.5**：`TestAuthenticateStrictDatabaseState` 全部子测试，涵盖以下场景：
  - Redis 写失败时严格认证拒绝，而 `Authenticate` 仍 fail-open；
  - 刷新期限已到但未吊销的会话仍有效；
  - `sub` 不符或会话已清理；
  - revokeOthers、冻结、注销后立即失效；
  - 严格认证不写库。

  另外还有换绑断言，以及既有的 Redis fail-open 数据库回归测试。
- **任务 4.2、4.3**：accountsvc 进程集成测试未执行，包括：JWKS 拉取与本地验签、真实内省撤销后返回 `active:false`、按模板密钥启动 serve。
- **任务 7.1**：`bash scripts/check-generated.sh` 未运行。sqlc 未安装，下载发布二进制被权限策略拒绝。本变更未改动 SQL 或 sqlc 配置，`user/db/` 无差异。
- **任务 7.2**：`bash scripts/verify.sh`（race、必需测试门禁、pg_dump/pg_restore 恢复演练）未运行。Windows 缺少 cgo 工具链，`tests/Dockerfile` 镜像也依赖 Docker。

补跑方法：启动 Docker Desktop 后提供三个一次性数据库，在 `tests/Dockerfile` 镜像中运行 `bash scripts/verify.sh` 与 `bash scripts/verify-accountsvc.sh`，再回填本记录并勾选对应任务。
