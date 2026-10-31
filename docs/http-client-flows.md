# HTTP 客户端调用流程

本文面向直接调用内置终端用户 HTTP 面的客户端（App、Web 或其他服务端代理），说明登录、刷新、重新认证和退出的协议层状态机与时序。凭证存储位置、并发原语、后台任务和平台 API 由各平台自行决定，本文不做规定。路径以宿主挂载前缀为准，示例使用 `/v1`。

示例中的凭证一律使用占位符：`<access>` 为 access token，`<refresh>` 为 refresh token，`<ticket>` 为登录补全票据，`<code_id>`、`<code>` 为验证码轮次与验证码。不要把真实响应复制进文档或日志。每节末尾列出的子测试位于 `examples/httpflows`，用 httptest 挂载内置 HTTP 面并连接真实 PostgreSQL 走完同一流程；它只断言状态码、响应形状和状态迁移，不断言随机凭证值。字段完整定义见 [OpenAPI 契约](openapi.md)。

## 客户端状态机

```
                       sign-in returns TokenPair
  +------------+ ----------------------------------------> +-------------------+
  | SIGNED_OUT |                                           | SIGNED_IN         |
  |            | <----------------------+                  | (access, refresh) |
  +------------+                        |                  +-------------------+
     |      ^                           |                    |    ^     |    ^
     |      | ticket expired            |              401   |    |    reauthenticate 200
     |      | or abandoned              |                    |    |    (new access,
     |      |                           |                    |    |     same refresh)
     |      |                           |                    |    |     |    |
     |      |                           |                    |    |     +----+
     | sign-in returns PendingSignIn    |                    v    | 200 TokenPair
  +------------+                        |                  +-------------------+
  | PENDING    |                        +-- invalid_grant -| REFRESHING        |
  | (ticket)   |                                           +-------------------+
  +------------+
     |
     | completeSignIn returns TokenPair  ---> SIGNED_IN
```

规则：

- 只有 `TokenPair` 响应（登录、补全、`POST /token`）更新整组 access 与 refresh；收到后立即替换，旧 refresh 不再使用。
- `reauthenticate` 只返回新 access，不含 refresh：保存新 access，保留原 refresh 及其期限。
- 业务请求收到 401（带 `WWW-Authenticate: Bearer`）时，先用当前 refresh 刷新一次；刷新成功则用新 access 重放原请求，刷新返回 `invalid_grant` 才清理全部凭据并回到 SIGNED_OUT。刷新后的重放仍 401 时不要循环刷新，直接按退出处理。
- `POST /token` 返回 503 `temporarily_unavailable` 是依赖暂时不可用，保留凭据稍后重试，不要当作退出；403 `invalid_grant` 且 `reason` 为 `USER_FROZEN` 表示账号被冻结，按产品提示处理。
- PENDING 不是登录态：此时没有账号、会话或 token，只有补全票据。
- 403 `INSUFFICIENT_SCOPE`（例如补全后只得到 `user:bind`）和 400 `REAUTHENTICATION_REQUIRED` 都不是认证失效，不触发刷新或退出。

对应测试：`examples/httpflows` 的 `TestHTTPFlows/first_sign_in`、`TestHTTPFlows/refresh_replay_outside_grace`、`TestHTTPFlows/reauthentication_keeps_refresh`。

## 首次登录与 pending 补全

```
client                                   server
  |  POST /users:sendSignInCode              |
  |----------------------------------------->|
  |  200 {code_id, expire_time}              |
  |<-----------------------------------------|
  |  POST /users:signInWithCode              |
  |  X-Device-Id: device-a                   |
  |----------------------------------------->|
  |  200 TokenPair | PendingSignIn           |
  |<-----------------------------------------|

  (PendingSignIn, e.g. from /users:signInWithIdp)
  |  POST /users:sendSignInCompletionCode    |
  |----------------------------------------->|
  |  200 {code_id, expire_time}              |
  |<-----------------------------------------|
  |  POST /users:completeSignIn              |
  |----------------------------------------->|
  |  200 TokenPair                           |
  |<-----------------------------------------|
```

验证码登录：

```http
POST /v1/users:sendSignInCode
Content-Type: application/json

{"channel":"EMAIL","target":"first@example.test"}
```

```http
POST /v1/users:signInWithCode
Content-Type: application/json
X-Device-Id: device-a

{"kind":"EMAIL","credential":{"target":"first@example.test","code_id":"<code_id>","code":"<code>"}}
```

```json
{"access_token":"<access>","token_type":"Bearer","expires_in":900,"refresh_token":"<refresh>","refresh_expires_in":2592000,"scope":"user","user_id":"<user_id>","is_new_user":true}
```

第三方登录在身份策略要求补全时返回 pending（同为 HTTP 200、`Cache-Control: no-store`）：

```json
{"next_action":"COMPLETE_IDENTITY","sign_in_completion_token":"<ticket>","expire_time":"2026-01-01T00:05:00Z","enabled_identity_kinds":["EMAIL","WECHAT"],"required_identity_kinds":["EMAIL"]}
```

```http
POST /v1/users:sendSignInCompletionCode
Content-Type: application/json

{"sign_in_completion_token":"<ticket>","channel":"EMAIL","target":"pending@example.test"}
```

```http
POST /v1/users:completeSignIn
Content-Type: application/json
X-Device-Id: device-a

{"sign_in_completion_token":"<ticket>","kind":"EMAIL","credential":{"target":"pending@example.test","code_id":"<code_id>","code":"<code>"}}
```

要点：

- 登录和补全的 200 是 `TokenPair` 与 `PendingSignIn` 的 `oneOf`，没有 discriminator：含 `access_token` 即为 `TokenPair`，否则为 `PendingSignIn`。`next_action` 当前唯一值为 `COMPLETE_IDENTITY`，遇到未知值必须拒绝并提示，不能当作已登录。
- 以响应中的 `scope` 为准：`user` 才有普通业务权限，`user:bind` 只能继续绑定身份。
- 票据只在 `expire_time` 前有效，最多 5 分钟，追加验证码和重试不续期；过期后返回 400 `SIGN_IN_COMPLETION_INVALID`，客户端回到 SIGNED_OUT 重新发起第三方登录。
- 票据只能完成一次登录。完成响应丢失时，在原期限内重放完全相同的 `completeSignIn` 请求，读回的是同一组 pair，不会新建会话；该 pair 的 refresh 一经轮换，再重放返回 400 `SIGN_IN_ALREADY_COMPLETED`。客户端不能缓存票据用于后续登录。
- `X-Device-Id` 在同一设备上保持稳定（`^[A-Za-z0-9._-]{1,64}$`）；同一账号同一设备重新登录会替换该设备的旧会话。补全时设备字段可省略，重复提供须与首次一致。
- 同一目标重复发码受冷却限制，返回 429 时按 `Retry-After` / `retry_after_seconds` 等待后再发。

对应测试：`examples/httpflows` 的 `TestHTTPFlows/first_sign_in`、`TestHTTPFlows/pending_completion`。

## 刷新协调与宽限

```
req-1   req-2   req-N        refresher                    server
  |       |       |              |                           |
  |--401->|--401->|--401-------->|                           |
  |       |       |  (wait)      |  POST /token (refresh R1) |
  |       |       |              |-------------------------->|
  |       |       |              |  200 {access A2,          |
  |       |       |              |       refresh R2}         |
  |       |       |              |<--------------------------|
  |<-- retry with A2 ------------|                           |

  server-side fallback: concurrent POST /token with R1 inside
  RefreshGrace (default 30s) all get the same {A2, R2}
```

```http
POST /v1/token
Content-Type: application/json

{"grant_type":"refresh_token","refresh_token":"<refresh>"}
```

```json
{"access_token":"<access>","token_type":"Bearer","expires_in":900,"refresh_token":"<refresh>","refresh_expires_in":2592000,"scope":"user","user_id":"<user_id>","is_new_user":false}
```

要点：

- 多个请求同时发现 access 失效时，客户端只应由一个刷新者发出 `POST /token`，其他请求等待并复用其结果（单飞/节流）。
- 服务端宽限只是容错：宽限期内用同一旧 refresh 的重复刷新都返回同一组 pair，避免网络重试或多进程竞争把会话判为泄露。它不是协议承诺的并发语义，客户端不应依赖它代替单飞。
- 每次成功刷新都会轮换 refresh；收到新 pair 立即原子替换并丢弃旧 refresh。
- token 响应带 `Cache-Control: no-store` 与 `Pragma: no-cache`，中间层和客户端都不应缓存。
- `/token` 也接受 `application/x-www-form-urlencoded`；请求中的 `scope` 被忽略，签发 scope 以响应为准。

对应测试：`examples/httpflows` 的 `TestHTTPFlows/refresh_coordination`。

## 宽限外重放与退出

```
client                                   server
  |  POST /token (refresh R1)                |
  |----------------------------------------->|
  |  200 {access A2, refresh R2}             |
  |<-----------------------------------------|
  |        ... RefreshGrace elapses ...      |
  |  POST /token (refresh R1 again)          |
  |----------------------------------------->|  reuse detected:
  |  400 {"error":"invalid_grant"}           |  session revoked
  |<-----------------------------------------|
  |  GET /users/me (Bearer A2)               |
  |----------------------------------------->|
  |  401 WWW-Authenticate: Bearer ...        |
  |<-----------------------------------------|
  |  POST /token (refresh R2)                |
  |----------------------------------------->|
  |  400 {"error":"invalid_grant"}           |
  |<-----------------------------------------|
```

```json
{"error":"invalid_grant","error_description":"refresh token is invalid, expired, or revoked"}
```

要点：

- 已轮换的旧 refresh 在宽限外再次提交被视为凭证泄露信号：服务端吊销整个会话，刚换到的 access 和 refresh 一并失效。
- 客户端收到 `invalid_grant` 必须清理该会话的全部凭据并回到 SIGNED_OUT，不能用另一份缓存的 refresh 再试。
- 同样的 `invalid_grant` 也出现在 refresh 自然到期、会话被撤销等情况；客户端无需区分，一律重新登录。到期语义见 [README 会话过期语义](../README.md#会话过期语义)。

对应测试：`examples/httpflows` 的 `TestHTTPFlows/refresh_replay_outside_grace`。

## 重新认证

```
client                                   server
  |  POST /users/me:sendReauthenticationCode |
  |  Authorization: Bearer A1                |
  |----------------------------------------->|
  |  200 {code_id, expire_time}              |
  |<-----------------------------------------|
  |  POST /users/me:reauthenticate           |
  |  Authorization: Bearer A1                |
  |----------------------------------------->|
  |  200 {access A2}   (no refresh)          |
  |<-----------------------------------------|
  |  keep refresh R1, store A2               |
```

```http
POST /v1/users/me:reauthenticate
Authorization: Bearer <access>
Content-Type: application/json

{"kind":"EMAIL","credential":{"target":"reauth@example.test","code_id":"<code_id>","code":"<code>"}}
```

```json
{"access_token":"<access>","token_type":"Bearer","expires_in":900,"scope":"user","user_id":"<user_id>","is_new_user":false}
```

要点：

- 敏感操作（如注销账号、解绑身份）在登录时间超出窗口时返回 400 `FAILED_PRECONDITION`、`reason` 为 `REAUTHENTICATION_REQUIRED`。它不是 401，不触发刷新；客户端引导用户完成重新认证后重试原操作。
- 重新认证只接受账号已绑定的手机号或邮箱，目标不是本账号身份时返回 400 `TARGET_NOT_ANCHOR`。
- 成功后保存新 access、保留原 refresh 与其期限；原 refresh 仍可正常刷新。重新认证不延长会话期限，会话已吊销或到期时返回 401。
- 同一目标刚收到登录验证码时，重新认证发码同样受冷却限制（冷却按渠道与目标计，不区分用途），按 `Retry-After` 等待。

对应测试：`examples/httpflows` 的 `TestHTTPFlows/reauthentication_keeps_refresh`。

## 注销、撤销与解绑导致的退出

```
device-a (current)                       server              device-b / device-c
  |  GET /users/me/sessions                  |                        |
  |----------------------------------------->|                        |
  |  200 {sessions:[{name, is_current}...]}  |                        |
  |<-----------------------------------------|                        |
  |  DELETE /users/me/sessions/{session}     |                        |
  |----------------------------------------->|                        |
  |  204                                     |                        |
  |<-----------------------------------------|                        |
  |  POST /users/me/sessions:revokeOthers    |                        |
  |----------------------------------------->|                        |
  |  200 {}                                  |                        |
  |<-----------------------------------------|                        |
  |                                          |<-- GET (Bearer) -------|
  |                                          |--- 401 --------------->|
  |                                          |<-- POST /token --------|
  |                                          |--- 400 invalid_grant ->|
  |                                          |        -> SIGNED_OUT   |

device-a, unbinding the identity it signed in with:
  |  GET /users/me/sessions/current          |
  |----------------------------------------->|
  |  200 {authentication_identities:[...]}   |
  |<-----------------------------------------|
  |  DELETE /users/me/identities/{identity}  |
  |----------------------------------------->|
  |  204                                     |  sessions sourced from
  |<-----------------------------------------|  this identity revoked
  |  GET /users/me (same access)             |
  |----------------------------------------->|
  |  401  -> refresh -> invalid_grant        |
  |<-----------------------------------------|
  |  -> SIGNED_OUT                           |
```

要点：

- 会话列表中的 `name` 形如 `users/<user_id>/sessions/<session_id>`，单个撤销用其末段 `s_` ID 作为路径参数，成功返回 204；`revokeOthers` 保留当前会话、撤销本账号其他会话，返回 200 `{}`。
- 被撤销的设备不会收到推送通知；它在下一次请求时得到 401，按状态机先刷新，刷新得到 `invalid_grant` 后退出。
- 解绑前可用 `GET /users/me/sessions/current` 查看当前会话的 `authentication_identities`，按完整 `name` 与拟解绑身份比对，不能只比较 `kind`。解绑会撤销同账号中以该身份为登录来源的所有会话，包括当前会话。
- 解绑后仍须保留可用身份并满足部署的身份策略。示例启用 EMAIL/WECHAT、要求 EMAIL，且设置 `MaxIdentitiesPerKind=2`：先绑定第二个邮箱，再解绑作为当前来源的第一个邮箱。只有一个邮箱时直接解绑会返回 400 `LAST_ANCHOR_IDENTITY`；默认每种身份上限为 1，未调整上限就绑定第二个邮箱会返回 409 `IDENTITY_KIND_LIMIT`。客户端须依据实际部署策略准备剩余身份，不能直接照抄成功片段。
- 解绑成功的 204 表示操作已生效。随后出现的 401 是预期结果：清理凭据并重新登录，不能把已成功的解绑当成失败重试。
- 退出登录时用 `POST /v1/revoke`（`{"token":"<refresh>"}`，也接受表单编码）注销本设备的 refresh，返回 200 无内容；未知 token 同样返回 200。之后本地清理凭据。该端点不在本节子测试中走查，根包 `TestConsumerEndToEndAgainstRealDB` 覆盖其吊销效果。

对应测试：`examples/httpflows` 的 `TestHTTPFlows/revoke_others_and_delete_session`、`TestHTTPFlows/unbind_source_logs_out`。

## 错误处理约定

```
response
   |
   +-- /token or /revoke ?  -- yes --> OAuth body {"error", "error_description"}
   |                                    branch on "error"
   |
   +-- otherwise ----------------------> {"error":{"code","message","status","reason"}}
                                         branch on "reason"; unknown reason -> fallback
```

完整约定以 [OpenAPI 契约「客户端需要区分的响应」](openapi.md#客户端需要区分的响应) 为准，客户端至少需要：

- 普通端点按 `error.reason` 分支，`code` 只是数字 HTTP 状态；为未知 `reason` 保留兜底分支。例如补全重放返回 `{"error":{"code":400,"status":"FAILED_PRECONDITION","reason":"SIGN_IN_ALREADY_COMPLETED",...}}`。
- `/token`、`/revoke` 使用 RFC 6749 OAuth 错误体，按顶层 `error` 分支，不能按普通错误体解析。
- 401 只表示 Bearer 缺失、无效或会话已吊销，带 `WWW-Authenticate`；验证码错误为 400，scope 不足为 403，依赖不可用为 503，都不应触发退出。
- 429 带 `Retry-After` 头与 `retry_after_seconds`，按其等待后重试，不要立即重发。
- 所有凭证响应带 `Cache-Control: no-store`；不要把 access、refresh、票据、验证码或完整目标写入日志、URL 或分析埋点。

对应测试：`examples/httpflows` 的 `TestHTTPFlows/pending_completion`（普通错误体 `reason`）、`TestHTTPFlows/refresh_replay_outside_grace`（OAuth 错误体与 Bearer 401）。示例测试在同一目标重复发码前快进 Redis 时钟以模拟等待冷却，不单独断言 429 响应；限流细节见 [README](../README.md#验证码轮次)。
