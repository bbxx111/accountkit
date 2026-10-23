# Tasks

## 1. 枚举与观测常量

- [x] 1.1 在 `enum` 中增加内置类型 `FACEBOOK`，纳入内置判定，且不作为锚点或验证码渠道。用枚举测试验证往返、`IsBuiltin`、非锚点；用配置测试验证 Required 含 FACEBOOK 时启动失败，名为 FACEBOOK 的 OIDC 提供方被拒绝。
- [x] 1.2 追加审计事件类型 `PROVIDER_DATA_DELETION_PROCESSED`（27），只追加不重编号。用枚举测试验证名称往返、`Valid` 上界，并确认已有值不变。
- [x] 1.3 在 `observability` 中追加认证方法 `facebook` 与降级场景 `facebook_cached_key_fallback`。用分类测试验证取值为固定常量。

## 2. Facebook 提供方

- [x] 2.1 实现 `idp.Facebook` 的 credential 严格解码与形状分派。用表驱动测试验证：两种合法形状；并列、只有 nonce、空值、未知字段返回 `ErrMalformedCredential`，且测试替身确认未访问 JWKS 或 Graph、未登记 nonce。
- [x] 2.2 Limited 路径复用 `idTokenVerifier`（issuer `https://www.facebook.com`、aud 为 App ID、nonce 作用域 `facebook`、name/picture/email 声明），默认 JWKS 使用 www 域名，降级场景使用独立值。用校验器测试验证：合法 token；错误 iss；他人 aud 返回 `ErrAppNotAllowed`；nonce 重放；无缓存 JWKS 不可用返回 `ErrUnavailable`；缓存密钥降级单独计数。
- [x] 2.3 Classic 路径实现 `debug_token` 与 `/me` 两次调用、两种 `appsecret_proof`、不跟随重定向、去掉 URL 的错误包装与响应体上限，并按 design 的表格映射错误。用 `httptest` 测试验证：成功；`is_valid=false`；`type` 非 USER；`app_id` 不符；两步 id 不一致；5xx 与超时；App token 被拒返回 `ErrMisconfigured`；同一 token 可重复验证。断言错误文本与日志不含 token、Secret 或 proof。
- [x] 2.4 Classic 资料映射：剪影头像视为缺失，email 进入 `HintEmail`，`expires_at` 为 0 时 `ExpireTime` 保持零值。用单元测试覆盖这三项。
- [x] 2.5 实现 `PolicyFingerprint`：包含 App ID、版本、Graph 地址与 JWKS 地址，不含 Secret。用测试验证任一项变化都会改变指纹，且指纹文本不含 Secret。

## 3. 配置与装配

- [x] 3.1 `Config` 增加 Facebook 字段、默认值与校验（App ID 非空时 Secret 必填、版本格式 `vN.N`、NonceTTL 下限 1m、URL 为绝对 http(s)），并支持 `ConfigFromEnv` 的 `FACEBOOK_*`。在 design 记录选定的默认 Graph API 版本。用配置测试验证每个错误都指出字段名或变量名且不含 Secret；App ID 为空时不装配提供方。
- [x] 3.2 在提供方注册与默认 Enabled 推导中接入 FACEBOOK：仅在 App ID 非空时默认启用；显式启用但缺少 App ID 或 nonce 存储时启动失败。用装配测试验证这些规则，并验证补全配置指纹包含 Facebook 指纹。
- [x] 3.3 用服务层测试（替身提供方加真实 PostgreSQL）验证：Limited 注册后 Classic 登录命中同一身份；资料首次填充与再次登录不覆盖；邮箱不合并账号；未启用返回 `IDP_APP_NOT_ALLOWED`。缺少 `SERVER_TEST_DB_DSN` 时如实记为未执行。
- [x] 3.4 用 HTTP 测试验证第三方登录与绑定接受 `{"kind":"FACEBOOK","credential":{...}}` 的两种形状，并列凭证返回 400 `CREDENTIAL_INCOMPLETE`，依赖故障返回 503 `IDP_UNAVAILABLE`。

## 4. 数据删除

- [x] 4.1 实现 `(*Facebook).ParseSignedRequest` 与 `ErrInvalidSignedRequest`：base64url 无填充解码、算法为 HMAC-SHA256、常量时间比较，返回用户 id 与 `issued_at`。用测试验证合法请求、错误签名、错误算法、段数错误、填充和非法 JSON，且错误不含 Secret。
- [x] 4.2 把 `softDelete` 的事务体抽成可在已有事务内调用的内部函数，`DeleteMe` 与 `AdminDeleteUser` 改为调用它。用现有注销测试全部通过来证明行为不变。
- [x] 4.3 实现 `user.Service.DeleteProviderIdentityData`：在账号行锁内按 design 的分支判定，S2 调用 `AnonymizeIdentity` 与 `RevokeSessionsByIdentity`，S1 调用 4.2 的函数，并返回确认码、状态与 purge_time。用真实数据库测试覆盖 spec 的全部场景：无身份；重复请求；有手机号；剩余身份不满足 Required 时得到 user:bind；来源会话吊销；冻结账号有其他身份；只有 FB；宿主阻止；冻结账号只有 FB；已在冷静期；请求签发后重新绑定；`BeforeDelete` 返回普通错误时返回错误。
- [x] 4.4 并发测试：数据删除与同账号绑定 PHONE、普通解绑、注销并发时，结果等同于某一串行顺序。用 `-race` 及真实数据库的并发用例验证。
- [x] 4.5 审计：确认码等于数据删除事件 ID，操作者为服务，扩展数据只有 kind、status 与 hint；S2 与 S1 的既有事件操作者同为服务。用审计测试验证事件内容，确认不含完整 ASID；BLOCKED 时不出现解绑或注销事件；审计写入失败不改变返回结果。
- [ ] 4.6 门面 `Auth.HandleFacebookDataDeletion` 串联签名校验与领域方法，未启用 Facebook 时返回可区分的错误。用门面测试验证签名错误不触达数据库，成功时返回状态与确认码。

## 5. 服务与文档

- [ ] 5.1 `internal/accountsvc` 读取 `ACCOUNTKIT_FACEBOOK_*` 并校验，migrate 不要求这些变量。用服务配置测试覆盖 accountsvc-runtime delta 的四个场景。
- [ ] 5.2 更新 README 配置表、`docs/go-integration.md`（两种凭证、数据删除回调示例 handler 与状态页职责、确认码依赖尽力审计时宿主须自行持久化）、`docs/accountsvc.md`（服务不提供删除回调，部署方须配置 Meta 的数据删除说明页或另行实现）、`docs/compatibility.md` 与 `deploy/accountsvc/.env.example`（不含真实 Secret）。逐一核对文档中的路径与变量名与代码一致。
- [ ] 5.3 文档写明 Classic token 的重放限制并建议开启 Require App Secret，写明 email 只作提示，以及回退到旧版本后 FACEBOOK 身份无法登录。核对 design 的 Risks 都已在文档中体现。
- [ ] 5.4 在 `scripts/required-tests.txt` 登记新增的必需顶层测试，用 `internal/testgate` 的检查验证登记完整。

## 6. 集成核对

- [ ] 6.1 在 `GOWORK=off` 下运行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`、`bash scripts/check-generated.sh`、`bash scripts/check-migrations.sh` 与 `openspec validate --all --strict`，确认没有新增迁移，`0001` 与冻结源字节不变。有数据库与 CGO 条件时运行 `bash scripts/verify.sh`。把实际执行的命令、结果与未验证项写入本变更的 `verification.md`。

## Workflow follow-up

- 实现和审查完成后，单独执行归档。
- 归档时把 delta spec 同步到主规格，并修正验收记录中的相对链接。
