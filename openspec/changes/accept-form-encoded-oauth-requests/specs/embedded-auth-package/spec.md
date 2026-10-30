## ADDED Requirements

### Requirement: OAuth 端点请求体编码

`POST /token` 与 `POST /revoke` SHALL 按请求 `Content-Type` 的媒体类型（忽略 `charset` 等参数）分派请求体解析：`application/x-www-form-urlencoded` 按 RFC 6749 §3.2 / RFC 7009 §2.1 解析；其他情况（包括缺失或无法解析的 `Content-Type`）SHALL 沿用现有严格 JSON 规则，与新增表单支持之前完全一致，不新增对 `Content-Type` 的要求。表单路径 SHALL 使用与 JSON 相同的请求体大小上限，只读取请求体内的参数而忽略 URL 查询串，同名参数出现多次（无论是否识别）SHALL 返回 400 `invalid_request`，未识别参数 SHALL 被忽略；JSON 路径 SHALL 继续拒绝未知字段。两条路径的必填校验、`grant_type` 判定、错误映射、响应体与 `Cache-Control: no-store` SHALL 完全一致。刷新请求携带非空 `scope` 参数时 SHALL 返回 400 `invalid_scope`，不签发令牌；空值按 RFC 6749 §3.2 视为未提供。其他 C 端操作与管理面 SHALL 不因本要求接受表单请求体。

#### Scenario: 表单刷新
- **WHEN** 客户端以 `application/x-www-form-urlencoded; charset=UTF-8` 提交 `grant_type=refresh_token&refresh_token=<有效值>`
- **THEN** 返回与 JSON 请求相同的 200 token 响应与 `no-store` 头，会话轮换规则不变

#### Scenario: 表单撤销
- **WHEN** 客户端以表单提交 `token=<值>&token_type_hint=refresh_token`
- **THEN** 行为与 JSON 撤销一致：已知 token 被撤销、未知 token 同样返回 200

#### Scenario: 非表单媒体类型沿用 JSON 规则
- **WHEN** 请求缺少 `Content-Type`、媒体类型为 `text/plain` 或 `multipart/form-data`，而请求体是表单编码
- **THEN** 按严格 JSON 规则解析失败，返回 400 `invalid_request`，不读取领域层；同样缺少 `Content-Type` 但请求体是合法 JSON 对象时仍按既有行为成功

#### Scenario: 表单参数约束
- **WHEN** 表单中 `refresh_token` 出现两次，或参数仅出现在 URL 查询串
- **THEN** 前者返回 400 `invalid_request`；后者视为缺少必填参数返回 400 `invalid_request`

#### Scenario: 未识别参数
- **WHEN** 表单附带 `client_id` 等未识别参数
- **THEN** 忽略这些参数并正常处理；同样的未知字段出现在 JSON 请求体中仍返回 400 `invalid_request`

#### Scenario: 刷新时请求 scope
- **WHEN** 刷新请求（任一编码）携带 `scope` 参数
- **THEN** 返回 400 `invalid_scope`，不轮换会话

#### Scenario: 其他端点不受影响
- **WHEN** 以表单编码调用 `/users:signInWithCode` 等非 OAuth 端点
- **THEN** 按既有严格 JSON 规则返回 400 `MALFORMED_BODY`
