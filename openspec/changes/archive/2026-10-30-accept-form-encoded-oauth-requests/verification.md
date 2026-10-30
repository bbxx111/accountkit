# 验收记录：accept-form-encoded-oauth-requests

分支 `feat/accept-form-encoded-oauth-requests`，基于 develop `9647cc0`。改动：`httpapi/enduser/oauth.go`、`httpapi/enduser/response.go`、`httpapi/enduser/oauth_test.go`、`docs/openapi/enduser.json`、`docs/openapi.md`、`README.md`、`docs/compatibility.md`、`docs/gateway-integration.md`，以及本变更工件与 `docs/superpowers/plans/2026-10-30-accept-form-encoded-oauth-requests.md`。无数据库、配置、依赖或公开 Go API 变化。

## 执行方式

AGENTS.md 默认的 subagent-driven 执行未采用：改动集中在一个 Go 文件及其测试，任务之间共享写入且前后依赖，拆分给子代理的交接成本高于工作本身，因此由主会话按 tasks.md 顺序执行（先写失败用例，再实现，再文档），独立审查仍按 `superpowers:requesting-code-review` 派出子代理完成。

## 实际运行的检查

| 检查 | 结果 |
|---|---|
| 新增用例在实现前运行 | `TestTokenEndpointForm`、`TestTokenEndpointScope`、`TestRevokeEndpointForm` 按预期失败（400 invalid_request，领域层未被调用）；`TestNonOAuthEndpointRejectsForm` 与既有用例通过 |
| `gofmt -l httpapi/` | 无输出 |
| `GOWORK=off go build ./... && go vet ./...` | 通过 |
| `GOWORK=off go test -count=1 ./httpapi/...` | 7 个包全部 ok（审查修复后复跑） |
| `GOWORK=off go test -count=1 ./...`（无 `SERVER_TEST_DB_DSN`，数据库用例按规则跳过） | 审查修复前：52 个包 ok、EXIT 0（`.test-output/form-oauth/go-test-all.log`）；审查修复后复跑：见下方"复跑结果" |
| `node scripts/check-openapi.cjs` | enduser 24 / admin 11 个操作 OK |
| `openspec validate --all --strict` | 41 项通过 |

复跑结果：`GOWORK=off go test -count=1 ./...` 于审查修复后再次执行，52 个包 ok、无 FAIL、EXIT 0（`.test-output/form-oauth/go-test-all-2.log`）。

未安装外部工具：swagger-parser / Spectral 的结构校验需要 npm 安装到 `.test-output/`，本次沙箱拒绝，未执行；契约正确性以 `check-openapi.cjs`、HTTP 测试与人工核对为据。

## 设计偏离（已记入 design.md）

- 原提案把"缺失或其他媒体类型"判为 400。实施时发现现有 JSON 路径从不检查 `Content-Type`（仓库内 HTTP 测试均不发该头），改为只有表单媒体类型走表单解析，其余沿用 JSON 规则；proposal、规格场景、tasks、文档已同步。
- 原设计用 `Request.ParseForm` 读取 `PostForm`。审查指出 `ParseForm` 会同时解析并校验 URL 查询串，查询串畸形时即使请求体合法也会 400，与"忽略查询串"的契约冲突；改为读取请求体后 `url.ParseQuery`（与 accountsvc 内省接口做法一致），并补用例"畸形查询串 + 合法表单体 → 200"。

## 独立审查

子代理审查了未提交的工作树差异与全部工件，自行运行 vet、httpapi 测试与 `check-openapi.cjs`。结论：无 Critical；Important 3 项、Minor 4 项，处理如下。

1. Important：`ParseForm` 耦合查询串 → 已改为 `url.ParseQuery`，补测试。
2. Important：一律拒绝非空 `scope` 比 RFC 6749 §6 更严格，部分标准客户端库默认回传原 scope → 首版保持拒绝并要求客户端不发送 scope；随后用户决定改为按 RFC 6749 §3.3 忽略请求 scope（携带与否结果相同、不报错，签发 scope 以响应为准），移除 `invalid_scope`，proposal、design、spec、README、openapi.md、compatibility.md、enduser.json 同步修订，见下方"修订：忽略请求 scope"。
3. Important：enduser.json `/token` 描述仍写"仅接受 JSON refresh_token grant" → 改为"仅接受 refresh_token grant"。
4. Minor：design.md 决策 3 把拒绝重复参数类比为"JSON 拒绝重复键"，而 `encoding/json` 不拒绝重复键 → 改为以 RFC 6749 §3.2 为依据。
5. Minor：重复的未识别参数也被拒绝，文档未说明 → README、openapi.md、compatibility.md、enduser.json、规格与提案均改为"同名参数重复（无论是否识别）"，并补测试。
6. Minor：计划文档写"把既有 JSON 未知字段断言由 scope 改为 invalid_scope"，实际做法是把该断言换成 `client_id`（保留 JSON 未知字段拒绝覆盖），JSON/表单携带 `scope` 的行为在 `TestTokenEndpointScope` 单独覆盖；覆盖优于计划，此处记录差异。
7. Minor：`docs/openapi.md` 的 `info.version` 仍为 v0.7.2，按仓库惯例在发布准备时更新，本次不动。

## 规格场景与测试对应

| 场景 | 测试 |
|---|---|
| 表单刷新 | `TestTokenEndpointForm`：`ok("form")`、`ok("form with charset")` |
| 表单撤销 | `TestRevokeEndpointForm` |
| 非表单媒体类型沿用 JSON 规则 | `bad("no content type"/"text/plain"/"multipart")`；既有 `TestTokenEndpoint` 不带 `Content-Type` 的 JSON 仍成功 |
| 表单参数约束 | `bad("duplicate …")` ×3、`bad("query string …")` ×2、`ok("malformed query string is ignored …")`、超长与 `%zz`、`;` 分隔 |
| 未识别参数 | `ok("unknown form params ignored …")`；JSON `client_id` 仍 `invalid_request` |
| 刷新时携带 scope | `TestTokenEndpointScope`（form / json、已知/未知/多值/空均 200 且 `scope` 为推导值；重复 `scope` 参数为 `invalid_request`） |
| 其他端点不受影响 | `TestNonOAuthEndpointRejectsForm` |

## 未验证项

- 真实 OAuth 客户端库（如 requests-oauthlib、Spring Security 等）的端到端对接未在本仓库验证；回传原 scope 的库现在与不传一致；若客户端库本地断言响应 scope 等于请求 scope，在账号状态变化（绑定/解绑锚点、注销）后的首次刷新会在客户端侧报警，属客户端配置。
- accountsvc 进程级与真实依赖的完整验证脚本（`scripts/verify.sh`、`verify-accountsvc.sh`）本次未运行，留待发布前置流程；本变更不触及这些脚本覆盖的数据库、迁移与进程行为。
- swagger-parser / Spectral 结构校验未执行（见上）。

## 修订：忽略请求 scope（提交 2）

用户评估等值 scope 方案后决定：刷新请求中的 `scope` 按 RFC 6749 §3.3 忽略，逻辑与不传一致，不报错。改动：`oauth.go` 删除 `invalid_scope` 分支与表单 `scope` 映射（`tokenRequest.Scope` 保留只为严格 JSON 不拒绝该字段）；`TestTokenEndpointScope` 改为断言已知/未知/多值/空 scope 在两种编码下均 200、`Refresh` 调用一次、响应 `scope` 为推导值，重复 `scope` 参数仍 `invalid_request`；`enduser.json` 移除 `invalid_scope` 枚举值并改写 `RefreshRequest.scope` 与操作描述；proposal、design（决策 4 记录三种被否决的等值方案）、spec 要求与场景、tasks、README、openapi.md、compatibility.md、计划文档同步。

| 检查 | 结果 |
|---|---|
| `gofmt -l httpapi/` | 无输出 |
| `GOWORK=off go build ./... && go vet ./...` | 通过 |
| `GOWORK=off go test -count=1 ./httpapi/...` | 7 个包 ok |
| `GOWORK=off go test -count=1 ./...` | 52 个包 ok、无 FAIL、EXIT 0（`.test-output/form-oauth/go-test-all-3.log`） |
| `node scripts/check-openapi.cjs` | 通过 |
| `openspec validate --all --strict` | 41 项通过 |
| `invalid_scope` 残留 | 代码、契约与文档中仅 verification.md 的历史记录提及 |

本修订为对提交 1 未发布行为的放宽：不传 `scope` 的既有客户端零变化；传 `scope` 的请求由 400 改为与不传一致。
