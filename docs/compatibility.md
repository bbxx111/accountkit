# 功能与兼容对照

固定兼容基线包含 130 个文件，原始文件清单与 SHA-256 见 `tests/testdata/source-baseline/manifest.json`；该清单用于追溯冻结快照，不表示当前文件名和内容必须与历史快照相同。当前根包名为 `accountkit`，导入路径为 `github.com/bbxx111/accountkit`。

## 包名迁移

根包已从 `authserver` 重命名为 `accountkit`，module 路径不变。使用默认导入的调用方将 `authserver.Config`、`authserver.New` 等引用改为 `accountkit.Config`、`accountkit.New`：

```go
import "github.com/bbxx111/accountkit"

var cfg accountkit.Config
```

若暂不调整调用点，可显式指定旧别名 `import authserver "github.com/bbxx111/accountkit"`；显式别名不要求实际包声明使用同一名称。`Auth`、`Config`、`Deps` 等公开类型和方法签名保持不变，包级诊断文本前缀改为 `accountkit:`，调用方应使用 `errors.Is` 判断哨兵错误。

此次重命名不改变 HTTP 路由和错误码、环境变量键、数据库结构、Redis 键、令牌或密文格式，无需迁移数据或重新登录。维护任务继续使用历史 advisory lock 标识，以保证新旧版本进程互斥；冻结 SQL 和原始 manifest 保持原样。

accountsvc 是基于本库的可选官方服务；直接嵌入 accountkit 的宿主无需部署或调用该服务。服务配置与匿名化责任见 [服务手册](accountsvc.md)。

## 可选服务与发送器扩展

新增 `cmd/accountsvc`、可复用 SMTP 子包及内部消费者内省。SMTP 采用显式 TLS 与有限投递预算，短信在服务首版中显式禁用。库的 `SMSSender`/`EmailSender` 原接口不变；原发送器默认启用。只有显式禁用发送器的宿主会在发码前得到 400 `CHANNEL_NOT_ENABLED`，该拒绝不消费额度；SMTP 不可用沿用503，已产生的发送额度不退还。已存在验证码的校验及其他认证行为不变。

消费者内省沿用当前吊销查询 fail-open；管理员适配位于服务内部，不给消费者增加管理员身份。无数据库迁移、默认库配置变化或令牌格式变化。独立服务不执行宿主业务匿名化回调，不能与依赖此回调的宿主混跑同一实例的维护任务。

## 同类身份换绑扩展

新增 `user.Service.ReplaceIdentity` 和消费者 `:replace` 自定义操作。已有绑定/解绑签名、数量限制与最后身份保护保持；新操作以软删除旧身份、新建同类身份保留账号连续性，无数据库结构迁移。

`user.Deps.ReauthMaxAge` 的零值默认5分钟，`SensitiveOpVerification` 的 nil 默认true；根门面传入现有 Config 值，不新增环境变量。旧自定义 `consumer.Service` 继续编译，可按需实现可选 `consumer.IdentityReplacer`，未实现时换绑端点返回503。近期认证、冲突、验证码消费/回滚和重试边界见 [README](../README.md#手机号与邮箱换绑)。

撤销原因追加 `IDENTITY_REPLACED`，审计类型追加 `IDENTITY_REPLACE_REJECTED`，既有枚举值不重编号。回退旧二进制前须确认其审计/会话展示对新增值的兼容表现；回退不会恢复旧绑定或被撤销会话。登录及重新认证补充持锁后身份复核，旧身份在等待锁期间被移除时分别返回既有 CODE_INVALID、TARGET_NOT_ANCHOR，避免旧读结果重新进入原账号。

## 功能对照

| 规格能力 | 生产入口/路径 | 验证 |
|---|---|---|
| 独立消费、完整功能 | 根门面、全部子包、sqlc.yaml | 独立构建、临时宿主编译、源文件清单 |
| 手机/邮箱验证码、限流 | user/code、user/service_signin.go、httpapi/consumer | code/store_test.go、service_test.go、signin_test.go、TestConsumerEndToEndAgainstRealDB |
| 微信/Apple | user/idp、service_idp.go | wechat_test.go、apple_test.go、nonce_test.go、service_idp_test.go、TestWeChatSignInEndToEndAgainstRealDB |
| 身份绑定解绑 | user/service_identity.go | service_identity_test.go、identities_test.go、TestIdentityBindingEndToEndAgainstRealDB |
| 同类身份换绑 | user/service_identity_replacement.go、httpapi/consumer/replacement.go | 领域回滚/竞态、嵌入式及服务E2E，见[换绑验收记录](identity-replacement-verification.md) |
| JWT、刷新、会话、重新认证 | tokens、session、service_session.go | tokens_test.go、session/*/*_test.go、service_test.go、TestConsumerEndToEndAgainstRealDB |
| 资料、注销恢复、冻结 | service_me.go、service_lifecycle.go、service_admin.go | service_lifecycle_test.go、service_admin_test.go、TestAccountLifecycleEndToEndAgainstRealDB |
| 管理接口、角色、审计 | httpapi/admin、audit | httpapi/admin/*_test.go、audit/*_test.go、TestAdminSurfaceEndToEndAgainstRealDB |
| 加密、旧密钥、维护 | pii、maintenance、service_rekey.go | pii_test.go、runner_test.go、service_rekey_test.go、TestKeyRotationBackfillEndToEnd |
| 业务同事务匿名化 | anonymize、service_purge.go | service_purge_test.go、TestHostContracts（宿主先写入再失败） |
| 产品隔离 | Config.Schema/KeyPrefix、JWTIssuer/JWTAudience | TestIndependentInstances（故意复用 ID、refresh、目标与 HMAC 密钥暴露缺少命名空间） |
| 包内 schema/版本/重复迁移 | migrations、Auth.Migrate | migrations_test.go、TestSourceDatabaseTakeover |
| 旧存储及凭证接管 | 原样 0001、合成旧格式数据 | TestSourceDatabaseTakeover：四类表和版本快照不变、旧 JWT/refresh 可用、旧身份可解密 |

HTTP 请求响应、状态码、错误码和环境配置约定见 README；下列清单保留提取阶段的入口和测试索引，后续服务与换绑验证分别见对应验收记录。Redis 吊销检查 fail-open、审计失败不阻断、刷新宽限故障拒绝请求等原语义不变。第三方真实发送和设备联调由宿主负责。

## 公开门面与路由

```text
accountkit.go:104:func New(cfg Config, deps Deps) (*Auth, error) {
accountkit.go:253:func (a *Auth) Users() *user.Service { return a.users }
accountkit.go:256:func (a *Auth) ConsumerHandler() http.Handler { return a.consumer.Router() }
accountkit.go:260:func (a *Auth) AdminHandler() http.Handler { return a.adminHandler }
accountkit.go:264:func (a *Auth) RecordAdminForbidden(r *http.Request, p AdminPrincipal) {
accountkit.go:273:func (a *Auth) RequireScope(allowed ...string) func(http.Handler) http.Handler {
accountkit.go:278:func (a *Auth) RequireRecentAuth() func(http.Handler) http.Handler {
accountkit.go:283:func PrincipalFrom(ctx context.Context) (user.Principal, bool) { return authn.PrincipalFrom(ctx) }
accountkit.go:286:func (a *Auth) Config() Config { return a.cfg }
accountkit.go:290:func (a *Auth) Migrate(ctx context.Context) error {
accountkit.go:321:func (a *Auth) Start(ctx context.Context) {
accountkit.go:331:func (a *Auth) Close() {
accountkit.go:343:func (a *Auth) RunMaintenanceOnce(ctx context.Context) bool { return a.runner.RunOnce(ctx) }
accountkit.go:346:func PoolConfig(dsn, schema string) (*pgxpool.Config, error) {
config.go:120:func (c Config) Validate() error {
config.go:228:func ParseWeChatApps(spec string) ([]WeChatApp, error) {
config.go:248:func ConfigFromEnv(prefix string) (Config, error) {
httpapi/admin/handler.go:135:	r.With(op).Get("/users", h.listUsers)
httpapi/admin/handler.go:136:	r.With(op).Get("/users/{user}", h.getUser)
httpapi/admin/handler.go:137:	r.With(su).Delete("/users/{user}", h.deleteUser)
httpapi/admin/handler.go:138:	r.With(op).Post("/users/{user}:freeze", h.freeze)
httpapi/admin/handler.go:139:	r.With(op).Post("/users/{user}:unfreeze", h.unfreeze)
httpapi/admin/handler.go:140:	r.With(su).Post("/users/{user}:undelete", h.undeleteUser)
httpapi/admin/handler.go:141:	r.With(su).Get("/users/{user}/identities/{identity}:reveal", h.revealIdentity)
httpapi/admin/handler.go:142:	r.With(op).Get("/users/{user}/sessions", h.listSessions)
httpapi/admin/handler.go:143:	r.With(op).Delete("/users/{user}/sessions/{session}", h.deleteSession)
httpapi/admin/handler.go:144:	r.With(op).Post("/users/{user}/sessions:revokeAll", h.revokeAllSessions)
httpapi/admin/handler.go:145:	r.With(op).Get("/users/{user}/auditEvents", h.listAuditEvents)
httpapi/consumer/handler.go:124:	r.Post("/users:sendSignInCode", h.sendSignInCode)
httpapi/consumer/handler.go:125:	r.Post("/users:signInWithCode", h.signInWithCode)
httpapi/consumer/handler.go:126:	r.Post("/users:signInWithIdp", h.signInWithIdp)
httpapi/consumer/handler.go:127:	r.Post("/token", h.token)
httpapi/consumer/handler.go:128:	r.Post("/revoke", h.revoke)
httpapi/consumer/handler.go:131:	r.With(anyScope).Get("/users/me", h.getMe)
httpapi/consumer/handler.go:134:		r.Patch("/users/me", h.updateMe)
httpapi/consumer/handler.go:135:		r.Post("/users/me:sendReauthenticationCode", h.sendReauthenticationCode)
httpapi/consumer/handler.go:136:		r.Post("/users/me:reauthenticate", h.reauthenticate)
httpapi/consumer/handler.go:137:		r.Get("/users/me/sessions", h.listSessions)
httpapi/consumer/handler.go:138:		r.Delete("/users/me/sessions/{session}", h.deleteSession)
httpapi/consumer/handler.go:139:		r.Post("/users/me/sessions:revokeOthers", h.revokeOtherSessions)
httpapi/consumer/handler.go:145:	r.With(fullScope, h.RequireRecentAuth()).Delete("/users/me", h.deleteMe)
httpapi/consumer/handler.go:146:	r.With(undeleteScope).Post("/users/me:undelete", h.undelete)
httpapi/consumer/handler.go:151:		r.Post("/users/me:sendBindCode", h.sendBindCode)
httpapi/consumer/handler.go:152:		r.Get("/users/me/identities", h.listIdentities)
httpapi/consumer/handler.go:153:		r.Post("/users/me/identities", h.bindIdentity)
httpapi/consumer/handler.go:155:	r.With(fullScope, h.RequireRecentAuth()).Delete("/users/me/identities/{identity}", h.unbindIdentity)
```

## 错误映射实现

```text
httpapi/consumer/response.go:110:	apierror.WriteJSON(w, http.StatusOK, newTokenResponse(res))
httpapi/consumer/response.go:118:		apierror.Write(w, &apierror.Error{Status: apierror.StatusResourceExhausted, Reason: rl.Dimension, Message: "too many requests, retry later", RetryAfterSeconds: retryAfterSeconds(rl.RetryAfter)})
httpapi/consumer/response.go:120:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_NONCE_REPLAYED", "this sign-in attempt was already used; start again"))
httpapi/consumer/response.go:122:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_APP_NOT_ALLOWED", "this application is not allowed to sign in"))
httpapi/consumer/response.go:124:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "IDP_CREDENTIAL_INVALID", "identity provider credential is invalid or expired"))
httpapi/consumer/response.go:126:		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: "IDP_UNAVAILABLE", Message: "identity provider temporarily unavailable, retry later", RetryAfterSeconds: 1})
httpapi/consumer/response.go:128:		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
httpapi/consumer/response.go:130:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_INVALID", "verification code is incorrect"))
httpapi/consumer/response.go:132:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_EXPIRED", "verification code has expired or was never issued for this purpose"))
httpapi/consumer/response.go:134:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "CODE_ATTEMPTS_EXHAUSTED", "too many incorrect attempts; request a new code"))
httpapi/consumer/response.go:136:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_TARGET", "target is not a valid phone number or email address"))
httpapi/consumer/response.go:142:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", msg))
httpapi/consumer/response.go:144:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "TARGET_NOT_ANCHOR", "target must be a phone or email already bound to this account"))
httpapi/consumer/response.go:146:		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "USER_FROZEN", "this account is frozen"))
httpapi/consumer/response.go:148:		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "USER_PENDING_DELETION", "this account is pending deletion; sign in with its phone or email to restore it"))
httpapi/consumer/response.go:150:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "INVALID_ACCOUNT_STATE", "operation not allowed in the account's current state"))
httpapi/consumer/response.go:152:		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, "IDENTITY_ALREADY_BOUND", "this identity is already bound to another account; sign in with that account instead"))
httpapi/consumer/response.go:154:		apierror.Write(w, apierror.New(apierror.StatusAlreadyExists, "IDENTITY_KIND_LIMIT", "the maximum number of identities of this kind is already bound"))
httpapi/consumer/response.go:156:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "LAST_ANCHOR_IDENTITY", "cannot unbind the last phone or email identity"))
httpapi/consumer/response.go:158:		apierror.Write(w, apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found"))
httpapi/consumer/response.go:160:		apierror.Write(w, &apierror.Error{Status: apierror.StatusUnavailable, Reason: "DEPENDENCY_UNAVAILABLE", Message: "service temporarily unavailable, retry later", RetryAfterSeconds: 1})
httpapi/consumer/response.go:164:		apierror.WriteInternal(w, h.d.Logger, requestIDFrom(r.Context()), err)
httpapi/consumer/response.go:172:		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_request", "request body must be a single JSON object with known fields", nil)
httpapi/consumer/response.go:174:		apierror.WriteOAuth(w, http.StatusForbidden, "invalid_grant", "user is frozen", map[string]any{"reason": "USER_FROZEN"})
httpapi/consumer/response.go:176:		apierror.WriteOAuth(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid, expired, or revoked", nil)
httpapi/consumer/response.go:179:		apierror.WriteOAuth(w, http.StatusServiceUnavailable, "temporarily_unavailable", "service temporarily unavailable, retry later", nil)
httpapi/consumer/response.go:182:		apierror.WriteOAuth(w, http.StatusInternalServerError, "server_error", "internal error", nil)
httpapi/admin/response.go:27:// freezeResource 是冻结快照（用户资源的 freeze 字段；非 FROZEN 时为 null）。
httpapi/admin/response.go:54:		out.Freeze = &freezeResource{FreezeTime: fmtTime(u.Freeze.Time), Reason: u.Freeze.Reason, ActorSubject: u.Freeze.ActorSubject, ActorUsername: u.Freeze.ActorUsername}
httpapi/admin/response.go:130:		Reason: e.Reason, SessionID: e.SessionID, SubjectHint: e.SubjectHint, DeviceID: e.DeviceID, RequestID: e.RequestID,
httpapi/admin/response.go:150:// 目标账号的状态问题一律 400 FAILED_PRECONDITION。
httpapi/admin/response.go:154:		apierror.Write(w, apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found"))
httpapi/admin/response.go:156:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "INVALID_ACCOUNT_STATE", "operation not allowed in the account's current state"))
httpapi/admin/response.go:158:		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "USER_FROZEN", "the account is frozen; unfreeze it first"))
httpapi/admin/response.go:160:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_FILTER", "invalid filter: identity.phone / identity.email value is not a valid phone number or email address"))
httpapi/admin/response.go:166:		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", msg))
httpapi/admin/response.go:168:		apierror.WriteInternal(w, h.d.Logger, reqid.From(r.Context()), err)
```

## 测试索引

| 文件 | 测试 |
|---|---|
| `accountkit_db_test.go` | `TestMigrateStartCloseAgainstRealDB` |
| `accountkit_db_test.go` | `TestAuditEventsPersistedAndExpiredEndToEnd` |
| `accountkit_db_test.go` | `TestConsumerEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestMigrateRejectsPoolWithoutSchemaOnSearchPath` |
| `accountkit_db_test.go` | `TestWeChatSignInEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestIdentityBindingEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestKeyRotationBackfillEndToEnd` |
| `accountkit_db_test.go` | `TestAccountLifecycleEndToEndAgainstRealDB` |
| `accountkit_db_test.go` | `TestAdminSurfaceEndToEndAgainstRealDB` |
| `accountkit_test.go` | `TestNewIsPureAndAppliesDefaults` |
| `accountkit_test.go` | `TestNewRejectsBadConfigAndMissingDeps` |
| `accountkit_test.go` | `TestNewExposesUsersService` |
| `accountkit_test.go` | `TestPoolConfigSetsSearchPath` |
| `accountkit_test.go` | `TestCloseBeforeStartIsSafe` |
| `accountkit_test.go` | `TestNewDefaultsAuditToAsyncStoreUnlessInjected` |
| `accountkit_test.go` | `TestDefaultRequestIDAndClientIP` |
| `accountkit_test.go` | `TestConsumerHandlerAndMiddlewareWiring` |
| `accountkit_test.go` | `TestHostMountShape` |
| `accountkit_test.go` | `TestNewWiresIdPVerifiersOnlyWhenConfigured` |
| `accountkit_test.go` | `TestNewRejectsBadAnonymizersAndAcceptsDistinctOnes` |
| `accountkit_test.go` | `TestLifecycleRoutesMounted` |
| `accountkit_test.go` | `TestNewRequiresBothAdminDepsOrNeither` |
| `accountkit_test.go` | `TestAdminHandlerUnconfiguredIs503` |
| `accountkit_test.go` | `TestAdminHandlerMountsBehindHostVerifier` |
| `accountkit_test.go` | `TestRecordAdminForbidden` |
| `compatibility_test.go` | `TestSourceDatabaseTakeover` |
| `config_test.go` | `TestValidateAppliesDefaults` |
| `config_test.go` | `TestConfigFromEnvOverridesAndParses` |
| `config_test.go` | `TestValidateRejects` |
| `config_test.go` | `TestConfigFromEnvReportsBadValues` |
| `config_test.go` | `TestIdPConfigDefaultsAndValidation` |
| `config_test.go` | `TestParseWeChatApps` |
| `config_test.go` | `TestConfigFromEnvIdP` |
| `host_contract_test.go` | `TestHostContracts` |
| `isolation_test.go` | `TestIndependentInstances` |
| `audit/async_test.go` | `TestAsyncFlushesByBatchSizeAndInterval` |
| `audit/async_test.go` | `TestAsyncCloseFlushesRemainingAndDropsAfterClose` |
| `audit/async_test.go` | `TestAsyncCloseWithoutStartFlushesSynchronously` |
| `audit/async_test.go` | `TestAsyncStartAfterCloseDoesNotLeakWorker` |
| `audit/async_test.go` | `TestAsyncDropsWhenQueueFullAndNeverBlocks` |
| `audit/async_test.go` | `TestAsyncWriteFailureIsCountedNotPropagated` |
| `audit/async_test.go` | `TestAsyncWriteFailureLogIsThrottled` |
| `audit/async_test.go` | `TestAsyncWorkerKeepsRunningAfterAFailedBatch` |
| `audit/async_test.go` | `TestAsyncContextCancelFlushesAndStops` |
| `audit/async_test.go` | `TestAsyncWritesToPostgres` |
| `audit/audit_test.go` | `TestNoopImplementsRecorder` |
| `audit/audit_test.go` | `TestMemoryRecordsInOrderAndIsConcurrencySafe` |
| `audit/audit_test.go` | `TestHint` |
| `audit/store_test.go` | `TestStoreInsertBatchMapsFieldsAndNulls` |
| `audit/store_test.go` | `TestStoreInsertBatchIsAllOrNothing` |
| `audit/store_test.go` | `TestStoreInsertBatchSurvivesSameMillisecondIDCollisions` |
| `audit/store_test.go` | `TestStoreInsertBatchRetriesOncePrimaryKeyConflict` |
| `audit/store_test.go` | `TestStoreDeleteOlderThanBatches` |
| `audit/store_test.go` | `TestStoreListByUserPaginatesByOccurTimeAndID` |
| `email/email_test.go` | `TestNormalize` |
| `email/email_test.go` | `TestNormalizeRejects` |
| `email/email_test.go` | `TestHintsAndMask` |
| `enum/enum_test.go` | `TestUserStateValuesArePinned` |
| `enum/enum_test.go` | `TestStringAndParseRoundTrip` |
| `enum/enum_test.go` | `TestParseRejectsLowercaseAndUnknown` |
| `enum/enum_test.go` | `TestValidAndUnknownString` |
| `enum/enum_test.go` | `TestIdentityKindHelpers` |
| `httpapi/admin/audit_test.go` | `TestListAuditEvents` |
| `httpapi/admin/filter_test.go` | `TestParseFilterAcceptsSupportedGrammar` |
| `httpapi/admin/filter_test.go` | `TestParseFilterRejectsUnsupportedInputWithoutEchoingValues` |
| `httpapi/admin/identities_test.go` | `TestRevealIdentity` |
| `httpapi/admin/page_test.go` | `TestParsePageSize` |
| `httpapi/admin/page_test.go` | `TestCursorRoundTripAndRejection` |
| `httpapi/admin/sessions_test.go` | `TestSessionsListRevokeAndRevokeAll` |
| `httpapi/admin/users_test.go` | `TestRolesGateEveryRoute` |
| `httpapi/admin/users_test.go` | `TestMissingPrincipalIs401` |
| `httpapi/admin/users_test.go` | `TestListUsersParsesQueryAndEncodesNextPage` |
| `httpapi/admin/users_test.go` | `TestGetUserDetail` |
| `httpapi/admin/users_test.go` | `TestFreezeUnfreezeDeleteUndelete` |
| `httpapi/apierror/apierror_test.go` | `TestHTTPStatusMapping` |
| `httpapi/apierror/apierror_test.go` | `TestWriteAIP193Body` |
| `httpapi/apierror/apierror_test.go` | `TestWriteOmitsEmptyOptionalFields` |
| `httpapi/apierror/apierror_test.go` | `TestWriteInternalHidesErrorAndLogs` |
| `httpapi/apierror/apierror_test.go` | `TestWriteOAuth` |
| `httpapi/apierror/apierror_test.go` | `TestWriteRouteNotFoundAndMethodNotAllowed` |
| `httpapi/authn/authn_test.go` | `TestBearerMissingAndInvalid` |
| `httpapi/authn/authn_test.go` | `TestBearerUnavailableIs503AndSkipsWhenAlreadyAuthenticated` |
| `httpapi/authn/authn_test.go` | `TestRequireScope` |
| `httpapi/authn/authn_test.go` | `TestRequireRecentAuth` |
| `httpapi/authn/authn_test.go` | `TestPrincipalFromEmpty` |
| `httpapi/consumer/identities_test.go` | `TestListIdentities` |
| `httpapi/consumer/identities_test.go` | `TestSendBindCode` |
| `httpapi/consumer/identities_test.go` | `TestSendBindCodeEmptyClientIPIs500` |
| `httpapi/consumer/identities_test.go` | `TestBindIdentity` |
| `httpapi/consumer/identities_test.go` | `TestUnbindIdentityRequiresRecentAuth` |
| `httpapi/consumer/me_test.go` | `TestGetMe` |
| `httpapi/consumer/me_test.go` | `TestUpdateMe` |
| `httpapi/consumer/me_test.go` | `TestReauthentication` |
| `httpapi/consumer/me_test.go` | `TestSendReauthenticationCodeEmptyClientIPIs500` |
| `httpapi/consumer/me_test.go` | `TestDeleteMe` |
| `httpapi/consumer/me_test.go` | `TestUndelete` |
| `httpapi/consumer/oauth_test.go` | `TestTokenEndpoint` |
| `httpapi/consumer/oauth_test.go` | `TestRevokeEndpoint` |
| `httpapi/consumer/request_test.go` | `TestRouterSetsRequestIDAndReturns404ForUnknownRoute` |
| `httpapi/consumer/request_test.go` | `TestNewRejectsMissingDeps` |
| `httpapi/consumer/response_test.go` | `TestWriteServiceErrorMapping` |
| `httpapi/consumer/response_test.go` | `TestWriteServiceErrorInvalidArgumentEmptyDetail` |
| `httpapi/consumer/response_test.go` | `TestWriteOAuthErrorMapping` |
| `httpapi/consumer/response_test.go` | `TestDTOShapes` |
| `httpapi/consumer/response_test.go` | `TestDeviceFromHeaders` |
| `httpapi/consumer/response_test.go` | `TestCredentialOneof` |
| `httpapi/consumer/response_test.go` | `TestCredentialExplicitNullIsAbsent` |
| `httpapi/consumer/response_test.go` | `TestCredentialIdp` |
| `httpapi/consumer/response_test.go` | `TestParseChannel` |
| `httpapi/consumer/sessions_test.go` | `TestSessions` |
| `httpapi/consumer/sessions_test.go` | `TestListSessionsNilIsEmptyArray` |
| `httpapi/consumer/signin_test.go` | `TestSendSignInCode` |
| `httpapi/consumer/signin_test.go` | `TestSendSignInCodeEmptyClientIPIs500` |
| `httpapi/consumer/signin_test.go` | `TestSignInWithCode` |
| `httpapi/consumer/signin_test.go` | `TestSignInWithIdp` |
| `httpapi/consumer/signin_test.go` | `TestSignInWithIdpPendingDeletionIs403` |
| `httpapi/jsonbody/jsonbody_test.go` | `TestDecodeAcceptsObjectRejectsEverythingElse` |
| `httpapi/reqid/reqid_test.go` | `TestMiddlewareSetsHeaderAndContext` |
| `ids/ids_test.go` | `TestNewHasPrefixAndLength` |
| `ids/ids_test.go` | `TestValidRejectsWrongKindCaseAndAlphabet` |
| `ids/ids_test.go` | `TestPatternMatchesValidAndIsAnchored` |
| `ids/ids_test.go` | `TestNewIsTimeOrderedAcrossMilliseconds` |
| `ids/ids_test.go` | `TestNewRandomBitsVary` |
| `internal/testgate/main_test.go` | `TestVerifyRequiresEveryTestToPass` |
| `maintenance/runner_test.go` | `TestRunOnceRunsAllTasksAndContinuesPastFailures` |
| `maintenance/runner_test.go` | `TestRunOnceSkipsWhenLockHeld` |
| `maintenance/runner_test.go` | `TestStartTicksAndCloseStops` |
| `maintenance/runner_test.go` | `TestPanicInTaskIsRecovered` |
| `maintenance/runner_test.go` | `TestCloseCancelsInFlightRound` |
| `maintenance/runner_test.go` | `TestPGLockerMutualExclusion` |
| `migrations/migrations_test.go` | `TestValidSchema` |
| `migrations/migrations_test.go` | `TestUpCreatesTablesInsideSchemaOnly` |
| `migrations/migrations_test.go` | `TestTwoSchemasCoexist` |
| `migrations/migrations_test.go` | `TestSchemaPinsInvariants` |
| `phone/phone_test.go` | `TestNormalize` |
| `phone/phone_test.go` | `TestNormalizeRejectsInvalid` |
| `phone/phone_test.go` | `TestHintsAndMask` |
| `pii/pii_test.go` | `TestParseKeyList` |
| `pii/pii_test.go` | `TestParseKeyListRejects` |
| `pii/pii_test.go` | `TestParseKeyListErrorDoesNotEchoSecret` |
| `pii/pii_test.go` | `TestCipherRoundTripAndVersion` |
| `pii/pii_test.go` | `TestNewCipherAndDigesterValidate` |
| `pii/pii_test.go` | `TestDigesterIsKeyedStableAndVersioned` |
| `pii/pii_test.go` | `TestNewCipherRequiresExactly32ByteKeys` |
| `session/grace/cache_test.go` | `TestPutGetRoundTripEncryptedAndExpires` |
| `session/grace/cache_test.go` | `TestGetMissAndTamperedValue` |
| `session/grace/cache_test.go` | `TestRedisDown` |
| `session/grace/cache_test.go` | `TestPutRejectsNonPositiveTTL` |
| `session/revocation/set_test.go` | `TestRevokeThenIsRevokedThenExpires` |
| `session/revocation/set_test.go` | `TestRedisDownReturnsUnavailable` |
| `session/revocation/set_test.go` | `TestRevokeRejectsNonPositiveTTL` |
| `tokens/tokens_test.go` | `TestSignParseRoundTrip` |
| `tokens/tokens_test.go` | `TestParseAcceptsOldKeyVersionDuringOverlap` |
| `tokens/tokens_test.go` | `TestParseRejects` |
| `tokens/tokens_test.go` | `TestNewSignerValidates` |
| `tokens/tokens_test.go` | `TestLeewayDefaultAndOverride` |
| `user/repo_test.go` | `TestRepoRotateIsCAS` |
| `user/repo_test.go` | `TestRepoRevokeSessionsByUserExceptCurrent` |
| `user/repo_test.go` | `TestRepoIdentityDigestsAndUniqueViolation` |
| `user/repo_test.go` | `TestRepoWithTxRollsBackOnError` |
| `user/repo_test.go` | `TestRepoLockUserByIDSerializes` |
| `user/repo_test.go` | `TestRepoProviderIdentityLookupAndMetaMerge` |
| `user/repo_test.go` | `TestRepoGetAndSoftDeleteIdentity` |
| `user/repo_test.go` | `TestRepoSoftDeleteUndeletePurgeUser` |
| `user/repo_test.go` | `TestRepoAnonymizeIdentityKeepsExactlyOneSubjectAndCoversSoftDeleted` |
| `user/repo_test.go` | `TestRepoScrubAuditEventsAndDeleteStaleSessions` |
| `user/repo_test.go` | `TestRepoKeyVersionQueries` |
| `user/repo_test.go` | `TestRepoFreezeUnfreezeAndCountSessions` |
| `user/repo_test.go` | `TestRepoListUsersAdminFiltersAndPaginates` |
| `user/service_admin_test.go` | `TestFreezeRevokesSessionsSnapshotsAndAudits` |
| `user/service_admin_test.go` | `TestAdminDeleteAndUndeleteShareConsumerRules` |
| `user/service_admin_test.go` | `TestAdminSessionsListRevokeOneAndAll` |
| `user/service_admin_test.go` | `TestListUsersFiltersAndPaginates` |
| `user/service_admin_test.go` | `TestGetUserDetailAndRevealIdentity` |
| `user/service_identity_test.go` | `TestListIdentitiesMasksAnchors` |
| `user/service_identity_test.go` | `TestSendBindCodeUsesBindPurposeAndQuota` |
| `user/service_identity_test.go` | `TestBindWithCodeHappyPathIdempotentAndScopeAfterRefresh` |
| `user/service_identity_test.go` | `TestBindWithCodeConflictAndKindLimit` |
| `user/service_identity_test.go` | `TestNewServiceRejectsZeroMaxIdentitiesPerKind` |
| `user/service_identity_test.go` | `TestBindWithIdpHappyIdempotentConflictAndLimit` |
| `user/service_identity_test.go` | `TestBindWithIdpRejectsNonIdpKindAndDisabledProvider` |
| `user/service_identity_test.go` | `TestUnbindIdentityGuardsLastAnchorAndOwnership` |
| `user/service_identity_test.go` | `TestBindWithCodeRejectsNonActiveAccountStates` |
| `user/service_identity_test.go` | `TestBindWithIdpRejectsNonActiveAccountStates` |
| `user/service_identity_test.go` | `TestUnbindIdentityRejectsFrozenPendingDeletionAndDeletedAccounts` |
| `user/service_identity_test.go` | `TestUnbindLastTwoAnchorsConcurrently` |
| `user/service_identity_test.go` | `TestBindSameSubjectConcurrentlyFromTwoAccounts` |
| `user/service_idp_test.go` | `TestSignInWithIdpWeChatRegistersWithBindScopeAndMergesOpenIDs` |
| `user/service_idp_test.go` | `TestSignInWithIdpAppleHintEmailAndReplay` |
| `user/service_idp_test.go` | `TestSignInWithIdpRejections` |
| `user/service_idp_test.go` | `TestSignInWithIdpWeChatIgnoresEmptyOpenID` |
| `user/service_idp_test.go` | `TestSignInWithIdpWeChatConcurrentFirstLoginCreatesOneUser` |
| `user/service_idp_test.go` | `TestSignInWithCodeStillWorksAfterRefactor` |
| `user/service_idp_test.go` | `TestSignInWithIdpRejectsPendingDeletionAccount` |
| `user/service_idp_test.go` | `TestSignInWithCodeFrozenAuditCarriesUserID` |
| `user/service_lifecycle_test.go` | `TestNewServiceRejectsZeroDeletionCoolingPeriod` |
| `user/service_lifecycle_test.go` | `TestDeleteMeSoftDeletesRevokesAllSessionsAndAudits` |
| `user/service_lifecycle_test.go` | `TestDeleteMeRejectsFrozenDeletedAndUnknownUser` |
| `user/service_lifecycle_test.go` | `TestUndeleteRestoresActiveAndRefreshWorksAgain` |
| `user/service_purge_test.go` | `TestNewServiceRejectsBadAnonymizers` |
| `user/service_purge_test.go` | `TestPurgeAnonymizesAccountIdentitiesSessionsAuditAndHostTables` |
| `user/service_purge_test.go` | `TestPurgeRollsBackWhenAnonymizerFailsAndSkipsUndueOrUndeleted` |
| `user/service_purge_test.go` | `TestPurgeIsANoopWhenContextCanceled` |
| `user/service_purge_test.go` | `TestPurgeCoversEveryTableWithUserID` |
| `user/service_rekey_test.go` | `TestRekeyDigestsRecomputesOnlyActiveOldVersionRows` |
| `user/service_rekey_test.go` | `TestReencryptSubjectsRewrapsCiphertextAndKeepsPlaintext` |
| `user/service_rekey_test.go` | `TestBackfillSkipsUndecryptableRowLogsWithoutPIIAndContinues` |
| `user/service_rekey_test.go` | `TestCheckKeyVersionsRejectsUnconfiguredVersions` |
| `user/service_test.go` | `TestSignInCreatesUserThenReusesIt` |
| `user/service_test.go` | `TestSignInWithEmailNormalizes` |
| `user/service_test.go` | `TestSignInWrongCodeAndCrossChannel` |
| `user/service_test.go` | `TestSignInSameDeviceReplacesSession` |
| `user/service_test.go` | `TestSignInFrozenAndPendingDeletion` |
| `user/service_test.go` | `TestSignInDeletedUserWithLiveIdentityIsInvariantViolation` |
| `user/service_test.go` | `TestSignInValidationAndRateLimitAndRedisDown` |
| `user/service_test.go` | `TestSendSignInCodeRequiresIP` |
| `user/service_test.go` | `TestRefreshRotatesAndOldTokenEntersGrace` |
| `user/service_test.go` | `TestRefreshReplayAfterGraceRevokesSession` |
| `user/service_test.go` | `TestRefreshRejectsUnknownRevokedAndExpired` |
| `user/service_test.go` | `TestRefreshFrozenAndPendingDeletionRevoke` |
| `user/service_test.go` | `TestRefreshConcurrentOnlyOneRotatesOthersGetSamePair` |
| `user/service_test.go` | `TestRefreshGraceDegradesWhenRedisDown` |
| `user/service_test.go` | `TestAuthenticateRejectsGarbageAndBadIDs` |
| `user/service_test.go` | `TestSessionsListRevokeOneAndOthers` |
| `user/service_test.go` | `TestRefreshReuseDetectedContainmentFailurePropagatesError` |
| `user/service_test.go` | `TestGetMeAndUpdateDisplayName` |
| `user/service_test.go` | `TestReauthenticateUpdatesAuthTimeOnlyForAnchor` |
| `user/service_test.go` | `TestReauthenticateFrozenUserAudited` |
| `user/service_test.go` | `TestCleanupSessionsDeletesOnlyStaleRows` |
| `user/code/store_test.go` | `TestIssueThenVerifySucceedsOnceAndDeletes` |
| `user/code/store_test.go` | `TestVerifyWrongCodeCountsAndExhausts` |
| `user/code/store_test.go` | `TestVerifyPurposeMismatchIsMissing` |
| `user/code/store_test.go` | `TestIssueCooldownAndReissueReplacesCode` |
| `user/code/store_test.go` | `TestIssueDailyLimits` |
| `user/code/store_test.go` | `TestIssueConcurrentBurstOnlyOneWins` |
| `user/code/store_test.go` | `TestVerifyConcurrentOnlyOneSucceeds` |
| `user/code/store_test.go` | `TestRedisDownIsUnavailable` |
| `user/code/store_test.go` | `TestCodeExpiresWithTTL` |
| `user/idp/apple_test.go` | `TestAppleVerifyHappyPathAndHintEmail` |
| `user/idp/apple_test.go` | `TestAppleVerifyRejections` |
| `user/idp/apple_test.go` | `TestAppleJWKSRefreshOnUnknownKidIsThrottled` |
| `user/idp/apple_test.go` | `TestAppleJWKSDownWithoutCacheIsUnavailable` |
| `user/idp/apple_test.go` | `TestAppleDisabledWhenNoBundleIDs` |
| `user/idp/apple_test.go` | `TestAppleJWKSFetchFailureBeforeFirstSuccessIsThrottled` |
| `user/idp/apple_test.go` | `TestAppleVerifyMissingKidHeaderIsInvalidCredential` |
| `user/idp/apple_test.go` | `TestAppleVerifyNoNonceRegistryIsMisconfigured` |
| `user/idp/apple_test.go` | `TestAppleNonceTTLExtendsToCoverIDTokenLifetime` |
| `user/idp/apple_test.go` | `TestAppleJWKSFetchIgnoresCallerCancellation` |
| `user/idp/apple_test.go` | `TestAppleUnknownKidAfterFailedRefreshIsUnavailable` |
| `user/idp/apple_test.go` | `TestAppleUnknownKidAfterSuccessfulRefreshIsInvalidCredential` |
| `user/idp/apple_test.go` | `TestAppleVerifyRedisDownIsUnavailable` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetValid` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSkipsNonRSA` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetNoRSAKeysIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetUndecodableModulusIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSmallModulusIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetBadExponentIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetLargeModulusIsError` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSkipsNonSigningUse` |
| `user/idp/jwk_test.go` | `TestParseRSAJWKSetSkipsNonRS256Alg` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRegisterOnce` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRegisterHonorsCallerTTL` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRejectsNonPositiveTTL` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRedisDownIsUnavailable` |
| `user/idp/nonce_test.go` | `TestNonceRegistryRejectsEmpty` |
| `user/idp/wechat_test.go` | `TestWeChatVerify` |
| `user/idp/wechat_test.go` | `TestWeChatNetworkErrorIsUnavailable` |
| `user/idp/wechat_test.go` | `TestWeChatNoAppsMeansNotAllowed` |
| `user/idp/wechat_test.go` | `TestWeChatDoesNotFollowRedirects` |
| `user/sender/log_test.go` | `TestLogSenderWritesMaskedTargetAndCode` |
