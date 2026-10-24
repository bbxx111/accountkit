# 微博 App SDK 登录 Implementation Plan

> **For agentic workers:** 本计划按 OpenSpec 任务顺序执行；每完成一个任务在 `tasks.md` 勾选对应编号。

**Goal:** 新增内置 `WEIBO` 第三方提供方：服务端用 `oauth2/get_token_info` 校验 App SDK 的 access_token，以全局 `uid:<uid>` 建立身份，经 `2/users/show.json` 读取昵称/头像，并加入登录补填集合。

**Architecture:** 与 QQ（[计划](2026-10-24-add-qq-app-sign-in.md)）同构：`idp.Weibo` 实现 `Provider`，网络调用在领域事务之前；补填由 `refillsProfileOnSignIn` 控制；配置走根 `Config` / `ConfigFromEnv(prefix)`，accountsvc 只做错误名映射与显式启用校验。分叉点见 design.md：无标识模式与元数据、`expire_in`、非 200 带 `error_code` 的错误体、字符串/整数双形态字段、头像仅 HTTPS。

**Spec:** [`openspec/changes/add-weibo-app-sign-in/`](../../../openspec/changes/add-weibo-app-sign-in/) 的 proposal、design、六份 spec 与 tasks。本计划不复述需求。

## Global Constraints

- `GOWORK=off`；不新增依赖；无 SQL/schema/迁移变更。
- 不修改 QQ、支付宝实现与其测试；只调用包内 `alipayObject` / `alipayString` / `configurationFingerprint`。
- 令牌、上游原文、资料原文不进入日志、错误文本、审计或响应；失败只用 `weiboFailure(哨兵, "阶段")`。
- 预算：`get_token_info` 10 秒，`users/show` 3 秒；64 KiB；禁止重定向；不重试。
- AppKey：1–20 位 ASCII 数字、不重复；默认地址 `https://api.weibo.com`，HTTP 仅数值回环地址。
- 观测：`weibo`、`verify_weibo`、`weibo_profile_unavailable`。
- 示例 AppKey 用 `1234567890` / `1234567891`；修改 Go 后 gofmt。

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `enum/enum.go`、`user/identity_policy.go` | 修改 | 内置 `IdentityWeibo` 与输出顺序 |
| `user/idp/weibo_config.go` | 新建 | `WeiboConfig`、`ValidateWeiboConfig`、地址规范化 |
| `user/idp/weibo.go` | 新建 | 凭证、令牌校验、subject、资料映射 |
| `user/idp/weibo_api.go` | 新建 | 有界请求、错误码分类、`weiboDecimal` |
| `user/idp/policy.go`、`user/idp/idp.go` | 修改 | 指纹与包注释 |
| `observability/classification.go`、`observability/internal/metricdata/project.go` | 修改 | 观测取值 |
| `user/observability.go`、`user/service_idp.go` | 修改 | 映射与补填集合 |
| `config.go`、`accountkit.go`、`internal/accountsvc/config.go` | 修改 | 配置、装配、服务变量映射 |
| 测试 | 新建 | `user/idp/weibo*_test.go`、`config_weibo_test.go`、`accountkit_weibo*_test.go`、`user/service_weibo*_test.go` |
| 文档 | 新建/修改 | `docs/weibo-sign-in.md`、README、`docs/{compatibility,go-integration,accountsvc,metrics}.md`、`.env.example` |

## Steps

1. 任务 1.x：先写枚举/配置/环境变量失败测试，再实现，跑 `go test ./enum/ ./user/idp/ .`。
2. 任务 2.x、3.x：以 `httptest` 模拟 `get_token_info` 与 `users/show` 先写提供方测试，再实现 `weibo*.go`。
3. 任务 4.x：仿 `user/service_qq_test.go` 写数据库测试（需 `SERVER_TEST_DB_DSN`），再改补填集合。
4. 任务 5.x：装配、accountsvc、观测与文档。
5. 任务 6.x：HTTP 契约测试、必需测试清单、全量验证并写 `verification.md`。
