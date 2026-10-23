package observability

// AuthOperation identifies a public authentication operation.
type AuthOperation string

const (
	AuthSignIn         AuthOperation = "sign_in"
	AuthCompleteSignIn AuthOperation = "complete_sign_in"
	AuthRefresh        AuthOperation = "refresh"
	AuthReauthenticate AuthOperation = "reauthenticate"
	AuthAuthenticate   AuthOperation = "authenticate"
)

// Method classifies authentication proof. None applies when no proof is supplied.
type Method string

const (
	MethodNone     Method = "none"
	MethodPhone    Method = "phone"
	MethodEmail    Method = "email"
	MethodWechat   Method = "wechat"
	MethodApple    Method = "apple"
	MethodGoogle   Method = "google"
	MethodFacebook Method = "facebook"
	MethodAlipay   Method = "alipay"
	// MethodOIDC 汇总全部配置的 OIDC 类型；具体类型名不作为标签。
	MethodOIDC Method = "oidc"
	// MethodProvider 汇总宿主登记的其他第三方类型；具体类型名不作为标签。
	MethodProvider Method = "provider"
	MethodUnknown  Method = "unknown"
)

// Result is shared vocabulary; each observation kind permits only its own subset.
type Result string

const (
	ResultSuccess          Result = "success"
	ResultPending          Result = "pending"
	ResultRejected         Result = "rejected"
	ResultDependencyError  Result = "dependency_error"
	ResultInternalError    Result = "internal_error"
	ResultCanceled         Result = "canceled"
	ResultDeadlineExceeded Result = "deadline_exceeded"
	ResultMiss             Result = "miss"
	ResultError            Result = "error"
	ResultFailed           Result = "failed"
	ResultInvalid          Result = "invalid"
	ResultDropped          Result = "dropped"
	ResultWritten          Result = "written"
	ResultDuplicate        Result = "duplicate"
	ResultConflict         Result = "conflict"
	ResultAcquired         Result = "acquired"
	ResultSkipped          Result = "skipped"
	ResultPanic            Result = "panic"
)

// Grant distinguishes normal and restricted user authorization.
type Grant string

const (
	GrantNone     Grant = "none"
	GrantUser     Grant = "user"
	GrantBind     Grant = "bind"
	GrantUndelete Grant = "undelete"
	GrantUnknown  Grant = "unknown"
)

// RefreshMode describes only a successful refresh's final handling.
type RefreshMode string

const (
	RefreshNone     RefreshMode = "none"
	RefreshRotation RefreshMode = "rotation"
	RefreshGrace    RefreshMode = "grace"
)

// Reason is a fixed, non-sensitive business outcome classification.
type Reason string

const (
	ReasonNone                  Reason = "none"
	ReasonInvalidArgument       Reason = "invalid_argument"
	ReasonIdentityKindDisabled  Reason = "identity_kind_disabled"
	ReasonChannelDisabled       Reason = "channel_disabled"
	ReasonInvalidCredential     Reason = "invalid_credential"
	ReasonCodeInvalid           Reason = "code_invalid"
	ReasonCodeExpired           Reason = "code_expired"
	ReasonCodeAttemptsExhausted Reason = "code_attempts_exhausted"
	ReasonCooldown              Reason = "cooldown"
	ReasonTargetLimit           Reason = "target_limit"
	ReasonIPLimit               Reason = "ip_limit"
	ReasonTargetVerifyLimit     Reason = "target_verify_limit"
	ReasonUserFrozen            Reason = "user_frozen"
	ReasonUserPendingDeletion   Reason = "user_pending_deletion"
	ReasonInvalidAccountState   Reason = "invalid_account_state"
	ReasonIdentityConflict      Reason = "identity_conflict"
	ReasonIdentityRequired      Reason = "identity_required"
	ReasonIdentityLimit         Reason = "identity_limit"
	ReasonFlowInvalid           Reason = "flow_invalid"
	ReasonFlowExpired           Reason = "flow_expired"
	ReasonFlowConflict          Reason = "flow_conflict"
	ReasonUnknownToken          Reason = "unknown_token"
	ReasonSessionRevoked        Reason = "session_revoked"
	ReasonSessionExpired        Reason = "session_expired"
	ReasonTokenReuse            Reason = "token_reuse"
	ReasonContainmentFailed     Reason = "containment_failed"
	ReasonGraceReadError        Reason = "grace_read_error"
	ReasonGraceMiss             Reason = "grace_miss"
	ReasonGracePairExpired      Reason = "grace_pair_expired"
	ReasonSendFailed            Reason = "send_failed"
	ReasonDependencyUnavailable Reason = "dependency_unavailable"
	ReasonInternal              Reason = "internal"
	ReasonCanceled              Reason = "canceled"
	ReasonDeadlineExceeded      Reason = "deadline_exceeded"
	ReasonUnknown               Reason = "unknown"
)

// CodeOperation distinguishes public send requests from actual verifications.
type CodeOperation string

const (
	CodeSend   CodeOperation = "send"
	CodeVerify CodeOperation = "verify"
)

// Channel identifies a supported verification-code delivery channel.
type Channel string

const (
	ChannelPhone   Channel = "phone"
	ChannelEmail   Channel = "email"
	ChannelUnknown Channel = "unknown"
)

// Purpose classifies the verification challenge's existing protocol purpose.
type Purpose string

const (
	PurposeSignIn         Purpose = "sign_in"
	PurposeBind           Purpose = "bind"
	PurposeReauth         Purpose = "reauth"
	PurposeSignInComplete Purpose = "sign_in_complete"
	PurposeUnknown        Purpose = "unknown"
)

// Component identifies a logical dependency, rather than its transport.
type Component string

const (
	ComponentCodeStore  Component = "code_store"
	ComponentGraceCache Component = "grace_cache"
	ComponentRevocation Component = "revocation"
	ComponentIDP        Component = "idp"
	ComponentSender     Component = "sender"
)

// DependencyOperation must match its Component's fixed operations.
type DependencyOperation string

const (
	DependencyIssue          DependencyOperation = "issue"
	DependencyVerify         DependencyOperation = "verify"
	DependencyDiscard        DependencyOperation = "discard"
	DependencyGet            DependencyOperation = "get"
	DependencyPut            DependencyOperation = "put"
	DependencyCheck          DependencyOperation = "check"
	DependencyRevoke         DependencyOperation = "revoke"
	DependencyVerifyWechat   DependencyOperation = "verify_wechat"
	DependencyVerifyApple    DependencyOperation = "verify_apple"
	DependencyVerifyGoogle   DependencyOperation = "verify_google"
	DependencyVerifyFacebook DependencyOperation = "verify_facebook"
	DependencyVerifyAlipay   DependencyOperation = "verify_alipay"
	// DependencyVerifyOIDC 汇总全部配置的 OIDC 提供方。
	DependencyVerifyOIDC DependencyOperation = "verify_oidc"
	// DependencyVerifyProvider 汇总宿主登记的其他第三方提供方。
	DependencyVerifyProvider DependencyOperation = "verify_provider"
	DependencySMS            DependencyOperation = "sms"
	DependencyEmail          DependencyOperation = "email"
)

// DegradationScenario enumerates actual degraded behavior, not dependency errors.
type DegradationScenario string

const (
	DegradationRevocationCheckFailOpen   DegradationScenario = "revocation_check_fail_open"
	DegradationRefreshGraceWriteFailed   DegradationScenario = "refresh_grace_write_failed"
	DegradationAppleCachedKeyFallback    DegradationScenario = "apple_cached_key_fallback"
	DegradationGoogleCachedKeyFallback   DegradationScenario = "google_cached_key_fallback"
	DegradationFacebookCachedKeyFallback DegradationScenario = "facebook_cached_key_fallback"
	DegradationOIDCCachedKeyFallback     DegradationScenario = "oidc_cached_key_fallback"
	DegradationAlipayProfileUnavailable  DegradationScenario = "alipay_profile_unavailable"
	DegradationQQProfileUnavailable      DegradationScenario = "qq_profile_unavailable"
)

// AuditKind separates event counts from omitted field counts.
type AuditKind string

const (
	AuditEvents        AuditKind = "events"
	AuditOmittedFields AuditKind = "omitted_fields"
)

// AuditStage identifies processing owned by the library.
type AuditStage string

const (
	AuditMapping   AuditStage = "mapping"
	AuditAdmission AuditStage = "admission"
	AuditWrite     AuditStage = "write"
)

// AuditField identifies only optional field classes, never their values.
type AuditField string

const (
	FieldRequestID   AuditField = "request_id"
	FieldDeviceID    AuditField = "device_id"
	FieldSubjectHint AuditField = "subject_hint"
	FieldActorName   AuditField = "actor_name"
	FieldClientIP    AuditField = "client_ip"
	FieldUnknown     AuditField = "unknown"
)

// MaintenanceKind separates locks, task completions, and actual completed work.
type MaintenanceKind string

const (
	MaintenanceRound MaintenanceKind = "round"
	MaintenanceRun   MaintenanceKind = "run"
	MaintenanceItems MaintenanceKind = "items"
)

// Task identifies a built-in maintenance task. All host-defined names map to Other.
type Task string

const (
	TaskCleanupSignInCompletionFlows Task = "cleanup_sign_in_completion_flows"
	TaskPurgeUsers                   Task = "purge_users"
	TaskCleanupSessions              Task = "cleanup_sessions"
	TaskAuditRetention               Task = "audit_retention"
	TaskRekeyDigests                 Task = "rekey_digests"
	TaskReencryptSubjects            Task = "reencrypt_subjects"
	TaskOther                        Task = "other"
)
