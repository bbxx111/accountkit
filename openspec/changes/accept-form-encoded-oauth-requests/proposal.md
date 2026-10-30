## Why

`POST /token` 与 `POST /revoke` 的响应、错误体和字段名遵循 RFC 6749 / RFC 7009，但请求体只接受 JSON。RFC 6749 §4.1.3、§6 与 RFC 7009 §2.1 规定的请求体编码是 `application/x-www-form-urlencoded`，标准 OAuth 客户端库（以及按 OpenAPI 生成的 SDK 里复用这类库的刷新逻辑）无法直接对接这两个端点。在评估 HTTP 客户端 SDK 时确认了这一差距，现在以增量方式补齐，不影响既有 JSON 调用方。

## What Changes

- `POST /token` 与 `POST /revoke` SHALL 在现有 JSON 请求体之外接受 `application/x-www-form-urlencoded` 请求体：`Content-Type` 媒体类型为表单时走表单解析，其他情况（包括缺失 `Content-Type`）沿用现有严格 JSON 规则，既有调用方零变化。
- 表单路径沿用同一请求体大小上限，只读取请求体内的参数（忽略 URL 查询串），同名参数重复（无论是否识别）返回 `invalid_request`，未识别参数按 RFC 6749 §3.2 忽略；JSON 路径继续拒绝未知字段。
- 刷新请求出现非空 `scope` 参数时返回 400 `invalid_scope`：本库不支持刷新时收窄 scope，忽略该参数会签发比请求更宽的授权。部分标准客户端库默认在刷新时回传原 scope，接入时须配置为不发送；接受与原授权相等的 scope 留作后续评估，本次不做。
- 响应格式、`no-store` 头、错误码映射、`grant_type` 判定与领域层调用完全复用；其余 22 个 C 端操作与管理面仍只接受严格 JSON。
- OpenAPI `enduser.json` 为两个操作补充表单 `requestBody` 内容类型并在 `OAuthError.error` 枚举中加入 `invalid_scope`；README、`docs/openapi.md`、`docs/compatibility.md` 同步说明。
- 修正 `httpapi/enduser/oauth.go` 中引用已不存在的 AGENTS.md 章节的注释。

## Capabilities

### New Capabilities

无。

### Modified Capabilities

- `embedded-auth-package`：新增「OAuth 端点请求体编码」要求，规定两个 OAuth 端点的媒体类型分派、表单解析约束与 `scope` 处理。

## Impact

- 代码：`httpapi/enduser/oauth.go`（请求体分派与表单解析）、`httpapi/enduser/response.go`（`errMalformedBody` 在 OAuth 错误里的文案）、`httpapi/enduser/oauth_test.go`（表单用例）。
- 文档：`docs/openapi/enduser.json`、`docs/openapi.md`、`README.md`、`docs/compatibility.md`。
- 无数据库、配置、依赖或 Go 公开 API 变化；向后兼容（只新增可接受的请求编码与一个错误码）。
- 部署注意：表单 POST 属于 CORS 简单请求，不触发预检。端点不依赖 Cookie、凭据随请求体携带，风险不变，但 Web 宿主的 CORS 说明应提及。
