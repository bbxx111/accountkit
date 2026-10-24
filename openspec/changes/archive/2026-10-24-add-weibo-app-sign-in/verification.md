# Verification

记录 `add-weibo-app-sign-in` 在分支 `feat/add-weibo-app-sign-in` 上的实际检查结果（2026-10-24）。原始测试输出保存在被忽略的 `.test-output/weibo-tests.jsonl`。

## 执行方式

按 [实现计划](../../../../docs/superpowers/plans/2026-10-24-add-weibo-app-sign-in.md) 顺序执行，未使用逐任务子代理：各任务共享 `config.go`、`enum`、观测与文档等写入点，且与 QQ 实现逐项同构，顺序实施更少冲突。每组先写失败测试再实现（TDD）；全部实现后由一个独立审查代理复核整条分支。

测试依赖为本机 Docker 中的 PostgreSQL 17.5 与 Redis 7：专为本任务创建的一次性数据库 `accountkit_weibo_test_20261024`，Redis 使用空的 DB 14，HTTP 契约测试使用随机键前缀并只删除自身键。

## 已运行检查

- `GOWORK=off go build ./...`、`go vet ./...`：通过（最终提交 `6d5a1b8` 上复跑）。
- `GOWORK=off go test -count=1 -json ./...`（设置 `SERVER_TEST_DB_DSN` 与 `ACCOUNTSVC_TEST_REDIS_URL`）：**无失败**。44 个包通过，1064 个顶层测试通过，0 fail，14 skip。
  - skip：13 个 `internal/accountsvc` 的 `TestServiceIntegration*` / `TestServiceProcessSIGTERM`（需 `scripts/verify-accountsvc.sh` 的服务环境，本变更未触及其路径）以及预期 skip 的 `TestRecoveryFixture`。
  - 全部微博相关测试均实际执行，无 skip。
  - 该全量运行在审查修复前的提交上完成；修复只改动 `user/idp/weibo.go` 及其测试，之后复跑 `go test ./user/idp/`（整包）与 `-run Weibo` 的根包、`user`（真实数据库）、`internal/accountsvc`、`observability/...`，全部通过。
- `bash scripts/check-generated.sh`：退出码 0，无生成差异（本变更无 SQL/sqlc 改动）。
- `openspec validate --all --strict`：36 项全部通过。
- 新增必需测试 35 个已追加到 `scripts/required-tests.txt`，包括 `TestWeiboHTTPLoginContract` 与 `TestWeiboHTTPMultiAppIdentityContract`。

## 审查结论

独立审查代理复核 `aced7a2..HEAD`：无 Critical / Important 问题，确认规格每条 SHALL 均有实现与测试、令牌不泄露、严格解析、错误分类与 design §5 一致、注册点与 QQ 一一对应且未回退 QQ/支付宝/Facebook。Minor 项处理：

1. `expire_in: 0` 会把 pending 截止时间设为当前时刻导致补全立即失效 → 已修复：0 按未知期限处理。
2. 超大 `expire_in` 在 int64 内外行为不一致（前者成功、后者 500）→ 已修复：凡非负且无法表示的值统一按未知期限，负值（含超范围负值）一律凭证无效；design §3 同步更新。
3. `docs/compatibility.md` 中 QQ 历史段落措辞被改写 → 已恢复原句。
4. 大写 `HTTPS://` 头像原样保留 → 与 QQ 一致，交给下游净化，属预期，不改。

实施期间自查另发现并修复：`expire_in` 超出 `time.Duration` 范围会溢出成过去的截止时间（`TestWeiboHugeExpireDoesNotOverflow`）。

## 设计偏离

- design §2 原称 `ExpireTime` "只作为会话来源的原始期限信息"，实际该值限制 pending 登录补全的截止时间上限；已更正 design 描述，行为与 Apple/Facebook/OIDC 一致，规格无需变化。

## 未验证项

- `bash scripts/verify.sh` **未执行**：需要三个一次性数据库（含恢复源/目标）、CGO 工具链（race）与 PostgreSQL 17 客户端；因此 race、pg_dump/pg_restore 恢复演练、`check-migrations.sh` 与完整必需测试门禁未在本任务运行，不能宣称完整发布验证通过。
- 宿主联调清单（见 [微博接入指南](../../../../docs/weibo-sign-in.md#宿主联调清单未执行)）全部**未执行**：真机 SDK 登录、`get_token_info` 真实字段类型与 `appkey`、令牌失效时的真实状态码与错误码、`users/show` 权限与限流、头像协议。模拟接口测试不代表真实平台行为。
