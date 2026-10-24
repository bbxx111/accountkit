# Verification

## Task 6 接入指南与正式 HTTP 契约验收 — 2026-10-24

本节记录任务 6（OpenSpec 6.1–6.4）的实际执行证据。分支 `feat/add-qq-app-sign-in`，工作树基于 `8e35b3e`。本任务只新增文档、HTTP 契约测试和必需测试清单；没有修改 SQL、schema、迁移、生成代码、冻结源或支付宝文件。

### 使用的一次性资源

一次性 PostgreSQL 17 容器与一次性 Redis 7 容器，仅供本变更使用，宿主机 Windows 直接运行 Go 命令（`GOWORK=off`）。不记录连接串。

### 命令与结果

- `go build ./...`：通过。
- `go vet ./...`：通过，无输出。
- `go test -count=1 -json ./...`（设置一次性数据库与 Redis 环境变量）：**无失败**。44 个有测试的包通过，1030 个顶层测试通过，0 fail；原始 JSON 保存在被忽略的 `.test-output/qq-task6-tests.jsonl`。
- QQ 新增 32 个顶层测试（含 `TestQQHTTPLoginContract` 与 `TestQQHTTPMultiAppIdentityContract`）全部为实际 PASS，没有 SKIP。`go test . -run TestQQHTTP -count=1 -v` 单独运行：两个契约测试及 `TestQQHTTPMultiAppIdentityContract` 的 UNIONID/OPENID 两个子测试 PASS。
- 用 `internal/testgate` 对 `scripts/required-tests.txt` 中全部 32 个 QQ 条目与上述 JSON 单独检查：通过（退出码 0）。对完整清单运行时报告 `internal/accountsvc/TestServiceIntegration*` 被跳过，原因见下。
- 跳过项：`TestRecoveryFixture`（普通套件预期 skip，由恢复脚本单独强制）；`internal/accountsvc` 的真实服务集成测试（如 `TestServiceIntegrationStartupSafety`、`TestServiceProcessSIGTERM` 等共 19 个）在 Windows 上因 TLS trust 与实际 SIGTERM 验证要求 Linux 而跳过。**这些服务集成测试未执行，不能当作通过。**
- `sqlc generate`（`scripts/check-generated.sh` 的等价步骤，因工作树隔离限制未直接调用脚本）：退出码 0，`git status` 显示 `user/db`、`audit/db` 无变化，生成结果与已提交一致。
- `openspec validate --all --strict`：**34 项通过，0 失败**（含 `change/add-qq-app-sign-in` 与 `change/add-facebook-sign-in`）。
- `scripts/required-tests.txt`：新增 32 项（任务清单所列 27 项加此前任务新增的 `TestQQClearedProfileUsesOnlyCurrentClaims`、`TestQQExceptionDoesNotBackfillOtherProviders`、`TestQQPendingConcurrentOwnershipRejectsWithoutBackfill`、`TestQQRepeatBindingDoesNotBackfill`、`TestQQProviderObservationsAndFlowFingerprint`）。每个名称都已确认对应实际存在的测试函数，无重复。

### 未执行

- `bash scripts/verify.sh`：**未执行**。它需要三个一次性数据库（普通集成、恢复源、恢复目标）、CGO 工具链（race）与 PostgreSQL 17 客户端；当前 Windows 环境未准备这些条件，也未使用 `tests/Dockerfile` 镜像。因此 race、真实 pg_dump/pg_restore 恢复演练、历史迁移检查（`check-migrations.sh`）与完整必需测试门禁均未在本任务运行，不能宣称完整验证通过。
- `bash scripts/verify-accountsvc.sh` 与 Linux 下的服务集成测试：未执行。

### 契约测试的证明范围

两个 HTTP 契约测试经正式门面、终端用户 handler、实际 QQ Provider、本地模拟 `oauth2.0/me` 与 `get_user_info` 接口、真实 PostgreSQL/Redis 验证：登录、本人资料、刷新轮换、令牌失效 / 跨 App 令牌 / 未配置 AppID 的拒绝（拒绝后账号、身份、会话、pending 行数不变）、资料失败后下次登录补填、http 头像升级为 https，以及 UNIONID 跨 App 同号单账号并合并 OpenID 元数据、OPENID 按 App 隔离。测试编写时发现同一设备的第二次登录会取代第一会话，因此刷新断言使用最新的令牌对，而不是削弱断言。模拟接口不证明真实 SDK、真实 UnionID 打通或头像主机行为。

### 宿主联调清单（均未执行）

- [ ] 真机 Android/iOS SDK 登录后提交令牌，后端登录成功：未执行
- [ ] 移动 SDK 令牌调用 `/me` 返回的 `client_id` 等于移动应用 AppID：未执行
- [ ] 两个已打通 App 对同一用户返回相同 unionid：未执行
- [ ] QQ `/me` 对无效或过期令牌返回的 HTTP 状态确认为 200 加 JSON 错误体（当前非 200 映射为 503 IDP_UNAVAILABLE）：未执行
- [ ] 实际 `figureurl_qq_*` 主机均在名单内，且对应 https 地址可访问：未执行

### Open Questions 现状（design.md）

1. `figureurl_qq_*` 的实际主机集合及 HTTPS 可用性：未解决，需真实账号联调，结果只调整头像名单常量。
2. 移动 SDK 取得的 access_token 调用 `/me` 返回的 `client_id` 是否恒为移动应用 AppID：未解决，预期如此，由宿主联调确认。

### 待处理风险

`add-facebook-sign-in` 已于 2026-10-24 先行归档（规格已同步到主规格），覆盖风险已解除；实现已于 2026-10-24 本地合入 develop（`986a681`），合并结果完整测试通过（真实 PostgreSQL/Redis，44 个包 ok、0 失败）。本变更随后归档并同步主规格，`openspec validate --specs --strict` 35 项通过，主规格中 ALIPAY、FACEBOOK、QQ 文本已核对完整。未推送。

### 最终评审后复跑

最终整分支评审问题修复后复跑：`bash scripts/check-generated.sh` 退出码 0（无生成差异，此前仅以 `sqlc generate` 加无差异核对作等价验证）；`GOWORK=off go build ./...`、`go vet ./...` 通过；带 SERVER_TEST_DB_DSN 与 Redis 的 `go test -count=1 ./user/idp/ ./user/ .` 三个包均 ok，QQ 相关测试 `-v` 下无 SKIP。
