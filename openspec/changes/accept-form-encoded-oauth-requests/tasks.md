## 1. 行为测试（先写失败用例）

- [x] 1.1 在 `httpapi/enduser/oauth_test.go` 为 `/token` 增加表单用例：带 `charset=UTF-8` 的正常刷新返回 200 与 `no-store`；重复 `refresh_token` 参数、参数仅在查询串、表单体配合缺失 `Content-Type` 或 `text/plain`（走 JSON 规则）均返回 400 `invalid_request`；附带 `client_id` 被忽略仍 200；超过 `MaxBodyBytes` 返回 `invalid_request`；既有不带 `Content-Type` 的 JSON 用例保持通过。验证：新增用例在实现前失败。
- [x] 1.2 增加 `scope` 用例：表单与 JSON 两种编码携带 `scope` 均返回 400 `invalid_scope`，且领域层 `Refresh` 未被调用。验证：实现前失败。
- [x] 1.3 为 `/revoke` 增加表单用例：已知 token 撤销成功、未知 token 返回 200；`token` 重复返回 `invalid_request`。验证：实现前失败。
- [x] 1.4 增加非 OAuth 端点回归用例：以表单编码调用 `/users:signInWithCode` 仍返回 400 `MALFORMED_BODY`。验证：实现前后均通过。

## 2. 实现

- [x] 2.1 在 `httpapi/enduser/oauth.go` 新增 OAuth 请求体解码函数：`mime.ParseMediaType` 分派；表单媒体类型先 `http.MaxBytesReader` 再 `ParseForm`，只读 `PostForm`，重复参数返回 `errMalformedBody`，未识别参数忽略；其他情况（含缺失 `Content-Type`）走现有 `decodeJSON`。`tokenRequest` 增加 `scope` 字段并在非空时返回 `invalid_scope`。修正文件头注释中对 AGENTS.md 不存在章节的引用。验证：1.1–1.3 通过。
- [x] 2.2 更新 `httpapi/enduser/response.go` 中 `errMalformedBody` 在 OAuth 错误里的描述文案，覆盖两种编码。验证：相关断言通过。
- [x] 2.3 运行 `GOWORK=off go build ./... && go vet ./... && gofmt -l httpapi/` 与 `GOWORK=off go test -count=1 ./httpapi/...`。验证：全部通过、gofmt 无输出。

## 3. 契约与文档

- [x] 3.1 `docs/openapi/enduser.json`：`/token`、`/revoke` 的 `requestBody.content` 增加 `application/x-www-form-urlencoded`（schema 复用 `RefreshRequest` / `RevokeRequest`）；`RefreshRequest` 增加可选 `scope` 并注明被拒绝；`OAuthError.error` 枚举加入 `invalid_scope`；操作描述写明媒体类型分派、未识别参数与查询串规则。验证：`node scripts/check-openapi.cjs` 通过。
- [x] 3.2 `docs/openapi.md`：在约定段补充两个 OAuth 端点的双编码说明与 JSON/表单的未知字段差异。验证：人工核对。
- [x] 3.3 `README.md`：第 267 行附近"严格 JSON"约定加例外说明；C 端路由表中 `/token`、`/revoke` 行注明两种编码与 `invalid_scope`；网关/CORS 说明处补一句表单 POST 不触发预检的提示。验证：人工核对。
- [x] 3.4 `docs/compatibility.md`：新增增量兼容条目（新增可接受编码与 `invalid_scope`，JSON 中 `scope` 的错误码由 `invalid_request` 细化为 `invalid_scope`）。验证：人工核对。

## 4. 验收

- [x] 4.1 运行 `openspec validate --all --strict`。验证：无错误。
- [x] 4.2 按 `superpowers:requesting-code-review` 提交独立审查，处理 Critical/Important 问题。验证：审查记录写入 verification.md。
- [x] 4.3 编写 `openspec/changes/accept-form-encoded-oauth-requests/verification.md`：列出实际运行的命令与结果、审查结论、未验证项（真实 OAuth 客户端库对接）。验证：文件存在且与实际一致。
