# OAuth 端点接受表单请求体 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `POST /token` 与 `POST /revoke` 在保持现有 JSON 行为不变的前提下，按 RFC 6749 / RFC 7009 同时接受 `application/x-www-form-urlencoded` 请求体，并显式拒绝刷新时的 `scope`。

**Architecture:** 在 `httpapi/enduser/oauth.go` 增加一个按 `Content-Type` 媒体类型分派的请求体解码函数；JSON 继续走 `jsonbody.Decode`，表单在同一大小上限下只读 `PostForm`。两个处理器在解码之后的逻辑不变。契约与文档随之更新。

**Tech Stack:** Go 1.26 标准库（`mime`、`net/http`、`net/url`），Chi 路由，现有 `httpapi/jsonbody` 与 `httpapi/apierror`。

**Spec:** `openspec/changes/accept-form-encoded-oauth-requests/`（proposal.md、specs/embedded-auth-package/spec.md、design.md、tasks.md）。本计划的任务编号与 tasks.md 一一对应。

## Global Constraints

- 不改变任何既有 JSON 请求的可观察行为，唯一例外是 JSON 体中出现 `scope` 时错误码由 `invalid_request` 细化为 `invalid_scope`（design.md 决策 4）。
- 表单只对 `/token`、`/revoke` 开放；其余端点仍严格 JSON。
- 表单路径：同一 `MaxBodyBytes` 上限；只读请求体参数；重复参数拒绝；未识别参数忽略。
- 错误体保持 RFC 6749 §5.2 形状，不引入 415。
- Go 文件 LF，gofmt；文档 CRLF 由 `.gitattributes` 规范化。

## Review Focus

- `Content-Type: application/x-www-form-urlencoded; charset=UTF-8` 必须被识别（参数被忽略而不是导致不匹配）。
- 查询串中的 `refresh_token` 不得被采纳。
- 表单超长时错误必须是 `invalid_request`，不能泄露为 500。
- 非法百分号编码（如 `refresh_token=%zz`）归为 `invalid_request`。
- `/revoke` 表单路径对未知 token 仍返回 200 且空 body。

---

### Task 1: 行为测试（tasks 1.1–1.4）

**Files:**
- Modify: `httpapi/enduser/oauth_test.go`

**Interfaces:**
- Consumes: `newHandler`、`newHandlerWithDeps`、`do`、`call`、`fakeService`（`fake_test.go`）。
- Produces: `TestTokenEndpointForm`、`TestTokenEndpointScope`、`TestRevokeEndpointForm`、`TestNonOAuthEndpointRejectsForm`。

- [ ] 1.1 写表单刷新用例（正常、charset、client_id 忽略、重复参数、查询串、无 Content-Type、text/plain、超长）。
- [ ] 1.2 写 scope 用例（表单与 JSON 均 `invalid_scope`，`refresh` 未被调用）；把现有"未知字段 scope → invalid_request"断言改为 `invalid_scope`。
- [ ] 1.3 写 `/revoke` 表单用例。
- [ ] 1.4 写非 OAuth 端点表单回归用例。
- [ ] 运行 `GOWORK=off go test -count=1 -run 'TestTokenEndpoint|TestRevokeEndpoint|TestNonOAuthEndpointRejectsForm' ./httpapi/enduser/`，预期新用例失败、1.4 通过。

### Task 2: 实现（tasks 2.1–2.3）

**Files:**
- Modify: `httpapi/enduser/oauth.go`
- Modify: `httpapi/enduser/response.go:239-240`

- [ ] 2.1 新增 `decodeOAuthBody(w, r, dst any, fields map[string]*string) error`；`tokenRequest` 增加 `Scope`；非空返回 `invalid_scope`；修正文件注释。
- [ ] 2.2 `writeOAuthError` 中 `errMalformedBody` 文案改为 "request body must be application/json (single object, known fields) or application/x-www-form-urlencoded (no repeated parameters)"。
- [ ] 2.3 `GOWORK=off go build ./... && go vet ./... && gofmt -l httpapi/`；`GOWORK=off go test -count=1 ./httpapi/...`。

### Task 3: 契约与文档（tasks 3.1–3.4）

**Files:**
- Modify: `docs/openapi/enduser.json`（`/token`、`/revoke` requestBody；`RefreshRequest.scope`；`OAuthError.error` 枚举）
- Modify: `docs/openapi.md`、`README.md`、`docs/compatibility.md`

- [ ] 3.1 用脚本编辑 JSON（保持 2 空格缩进与键序），`node scripts/check-openapi.cjs` 通过。
- [ ] 3.2–3.4 文档说明；`git diff` 人工核对。

### Task 4: 验收（tasks 4.1–4.3）

- [ ] 4.1 `openspec validate --all --strict`。
- [ ] 4.2 派出独立审查子代理（BASE=分支起点，HEAD=当前），修复 Critical/Important。
- [ ] 4.3 写 `verification.md`。
