## Context

见 proposal.md「Why」。现状：`httpapi/enduser/oauth.go` 的 `token` 与 `revoke` 处理器直接调用 `h.decodeJSON`（`httpapi/jsonbody.Decode`：单个 JSON 对象、拒绝未知字段、`MaxBodyBytes` 上限、拒绝尾随内容），解码失败经 `writeOAuthError` 映射为 400 `invalid_request`。响应统一由 `writeToken` / `apierror.WriteOAuth` 写出。仓库内 accountsvc 的内省接口已按 RFC 7662 接受表单请求，是表单解析的既有先例。约束：不改变 JSON 路径的任何可观察行为；不把表单能力扩散到其他端点。

## Goals / Non-Goals

**Goals:**
- 两个 OAuth 端点按 `Content-Type` 分派，表单路径与 JSON 路径在必填校验、错误映射、响应上完全一致。
- 表单路径的严格程度与 JSON 对齐可对齐的部分（大小上限、重复参数、只读请求体），在 RFC 明确规定处（忽略未识别参数）遵循 RFC。
- 对 `scope` 做显式、安全的处理。

**Non-Goals:**
- 不支持按请求 scope 签发（收窄或扩大）；请求 scope 被忽略。
- 不支持客户端认证（`client_id`/`client_secret`、HTTP Basic）；本库面向公共客户端，相关参数忽略。
- 不支持 `GET` 或查询串传参。
- 不为其他 C 端或管理面端点开放表单。
- 不改 `/token` 与 `/revoke` 的响应、错误体结构、`token_type_hint` 语义（仍只作提示）。

## Decisions

1. **按媒体类型分派，而不是嗅探内容；非表单一律沿用 JSON 路径**。用 `mime.ParseMediaType` 取媒体类型并忽略参数（`charset`）。只有 `application/x-www-form-urlencoded` 走表单；其他情况（包括缺失或无法解析的 `Content-Type`、`text/plain`）走现有 JSON 解码。原设计把"缺失或其他媒体类型"判为 400，实施时发现现有 JSON 路径从不检查 `Content-Type`（仓库内全部 HTTP 测试都不发该头），那样会破坏既有调用方，因此改为本条；这是对 proposal 初稿的明确偏离，规格场景已同步。表单编码的请求体在 JSON 路径下自然解析失败，仍得到 400 `invalid_request`，不引入 415 这种第二种错误形状。
2. **表单只解析请求体，不用 `Request.ParseForm`**。`ParseForm` 会同时解析并校验 URL 查询串（查询串畸形时即使请求体合法也返回错误），与"忽略查询串"的契约冲突；因此按 accountsvc 内省接口的既有做法，用 `http.MaxBytesReader(w, r.Body, h.d.MaxBodyBytes)` 读取请求体后 `url.ParseQuery`，查询串既不参与取值也不参与校验。超长与非法编码（含 `;` 分隔）归为 `invalid_request`。
3. **重复参数拒绝，未识别参数忽略**。RFC 6749 §3.2 规定参数不得重复出现，因此同名参数重复（无论是否识别）拒绝；`encoding/json` 不拒绝重复键，这里不以 JSON 侧作类比。未识别参数按 RFC 6749 §3.2 "MUST ignore" 处理，这样标准 OAuth 客户端库附带的 `client_id` 不会导致失败。JSON 路径保持 `DisallowUnknownFields`，这是两条路径唯一的刻意差异，README 与 OpenAPI 明确写出。
4. **请求 `scope` 按 RFC 6749 §3.3 忽略（两种编码一致）**。本库签发 scope 由刷新事务按账号状态与身份完整性推导，从不依据请求；§3.3 允许服务端完全忽略请求 scope，只要签发 scope 与请求不同时在响应返回 `scope`，而本库响应始终带 `scope`。因此 `tokenRequest` 保留 `scope` 字段仅为让严格 JSON 不把它当未知字段，表单映射不含 `scope`（未识别参数本就忽略），携带与否结果相同、不报错。JSON 中的 `scope` 此前作为未知字段返回 `invalid_request`，现与不传一致，属放宽。考虑过的替代：一律拒绝（首版实现，审查指出回传原 scope 的标准客户端库会每次刷新失败）、只接受等于本次推导值（账号状态变化后客户端卡死）、只接受等于原授权（需在会话存储 scope，即首个 0002 迁移），均不如忽略；等值校验若将来需要，可在处理器层增量加入而不改接口。
5. **解析与业务分离**。新增一个只负责"把请求体填进 `tokenRequest` / `revokeRequest`"的解码函数，两个处理器在解码后的逻辑零改动；`writeOAuthError` 对 `errMalformedBody` 的文案改为同时描述两种编码。
6. **契约表达**。OpenAPI 在两个操作的 `requestBody.content` 增加 `application/x-www-form-urlencoded`，schema 复用 `RefreshRequest` / `RevokeRequest`（表单字段名与 JSON 相同）；`RefreshRequest` 增加可选 `scope` 并注明被忽略；`OAuthError.error` 枚举不变。

## Risks / Trade-offs

- **CORS 简单请求**：表单 POST 不触发预检，任意源页面可直接向 `/token` 发请求。端点不依赖 Cookie，凭据在请求体内，无 CORS 头时响应不可读，因此不构成新的凭据泄露面；在 README 网关/CORS 说明处加一句提示即可。
- **解析差异**：`ParseForm` 对非法百分号编码返回错误，归为 `invalid_request`；对 `;` 分隔符 Go 已不再接受，与 WHATWG 一致，无需特殊处理。
- **文档漂移**：README 第 267 行"严格 JSON"约定需加例外说明，`scripts/check-openapi.cjs` 只核对路径/响应，不校验 requestBody 内容类型，契约正确性靠本次 HTTP 测试与人工核对。
