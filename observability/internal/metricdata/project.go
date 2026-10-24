package metricdata

import o "github.com/bbxx111/accountkit/observability"

// Label is a finite label name/value pair created only by Project.
type Label struct{ Name, Value string }

// Measurement is a local immutable-by-convention projection value. Its Labels
// slice belongs exclusively to this measurement; callbacks may retain or mutate it.
// Counters use Count; histograms use Value (seconds) and represent one observation.
type Measurement struct {
	ID     ID
	Count  int64
	Value  float64
	Labels []Label
}

// Project normalizes finite classifications and rejects invalid combinations or
// negative durations/counts. It ignores pointer/unknown observations. It never
// passes through arbitrary strings and never derives labels from context.
func Project(observation o.Observation, emit func(Measurement)) {
	if emit == nil {
		return
	}
	switch v := observation.(type) {
	case o.AuthObservation:
		if v.Duration < 0 || !oneOf(v.Operation, o.AuthSignIn, o.AuthCompleteSignIn, o.AuthRefresh, o.AuthReauthenticate, o.AuthAuthenticate) || !businessResult(v.Result) {
			return
		}
		if v.Result == o.ResultPending && v.Operation != o.AuthSignIn && v.Operation != o.AuthCompleteSignIn {
			return
		}
		grant := normalize(v.Grant, o.GrantUnknown, o.GrantNone, o.GrantUser, o.GrantBind, o.GrantUndelete, o.GrantUnknown)
		if v.Grant == "" {
			grant = o.GrantNone
		}
		mode := normalize(v.Mode, o.RefreshNone, o.RefreshNone, o.RefreshRotation, o.RefreshGrace)
		if v.Result != o.ResultSuccess && (grant != o.GrantNone || mode != o.RefreshNone) {
			return
		}
		if v.Operation != o.AuthRefresh && mode != o.RefreshNone {
			return
		}
		method := normalize(v.Method, o.MethodUnknown, o.MethodPhone, o.MethodEmail, o.MethodWechat, o.MethodApple, o.MethodGoogle, o.MethodFacebook, o.MethodAlipay, o.MethodQQ, o.MethodWeibo, o.MethodOIDC, o.MethodProvider, o.MethodUnknown)
		if v.Operation == o.AuthRefresh || v.Operation == o.AuthAuthenticate {
			method = o.MethodNone
		}
		if (v.Operation == o.AuthCompleteSignIn || v.Operation == o.AuthReauthenticate) && (method == o.MethodWechat || method == o.MethodApple || method == o.MethodGoogle || method == o.MethodFacebook || method == o.MethodAlipay || method == o.MethodQQ || method == o.MethodWeibo || method == o.MethodOIDC || method == o.MethodProvider) {
			return
		}
		labels := []Label{{"operation", string(v.Operation)}, {"method", string(method)}, {"result", string(v.Result)}}
		emit(Measurement{ID: AuthOperations, Count: 1, Labels: append(copyLabels(labels), Label{"grant", string(grant)}, Label{"mode", string(mode)}, Label{"reason", string(reason(v.Reason))})})
		emit(Measurement{ID: AuthDuration, Value: v.Duration.Seconds(), Labels: labels})
	case o.CodeObservation:
		if v.Duration < 0 || !oneOf(v.Operation, o.CodeSend, o.CodeVerify) || !businessResult(v.Result) || v.Result == o.ResultPending {
			return
		}
		channel := normalize(v.Channel, o.ChannelUnknown, o.ChannelPhone, o.ChannelEmail, o.ChannelUnknown)
		purpose := normalize(v.Purpose, o.PurposeUnknown, o.PurposeSignIn, o.PurposeBind, o.PurposeReauth, o.PurposeSignInComplete, o.PurposeUnknown)
		labels := []Label{{"operation", string(v.Operation)}, {"channel", string(channel)}, {"purpose", string(purpose)}, {"result", string(v.Result)}}
		emit(Measurement{ID: CodeOperations, Count: 1, Labels: append(copyLabels(labels), Label{"reason", string(reason(v.Reason))})})
		emit(Measurement{ID: CodeDuration, Value: v.Duration.Seconds(), Labels: labels})
	case o.DependencyObservation:
		if v.Duration < 0 || !dependencyPair(v.Component, v.Operation) || !oneOf(v.Result, o.ResultSuccess, o.ResultMiss, o.ResultRejected, o.ResultError, o.ResultCanceled, o.ResultDeadlineExceeded) {
			return
		}
		if v.Result == o.ResultMiss && !(v.Component == o.ComponentGraceCache && v.Operation == o.DependencyGet) {
			return
		}
		if v.Result == o.ResultRejected && !(v.Component == o.ComponentIDP || (v.Component == o.ComponentCodeStore && oneOf(v.Operation, o.DependencyIssue, o.DependencyVerify))) {
			return
		}
		labels := []Label{{"component", string(v.Component)}, {"operation", string(v.Operation)}, {"result", string(v.Result)}}
		emit(Measurement{ID: DependencyOperations, Count: 1, Labels: copyLabels(labels)})
		emit(Measurement{ID: DependencyDuration, Value: v.Duration.Seconds(), Labels: labels})
	case o.DegradationObservation:
		if v.Count <= 0 || !oneOf(v.Scenario, o.DegradationRevocationCheckFailOpen, o.DegradationRefreshGraceWriteFailed, o.DegradationAppleCachedKeyFallback, o.DegradationGoogleCachedKeyFallback, o.DegradationFacebookCachedKeyFallback, o.DegradationAlipayProfileUnavailable, o.DegradationQQProfileUnavailable, o.DegradationWeiboProfileUnavailable, o.DegradationOIDCCachedKeyFallback) {
			return
		}
		emit(Measurement{ID: Degradations, Count: v.Count, Labels: []Label{{"scenario", string(v.Scenario)}}})
	case o.AuditObservation:
		if v.Count <= 0 {
			return
		}
		switch v.Kind {
		case o.AuditEvents:
			if v.Field != "" || !auditPair(v.Stage, v.Result) {
				return
			}
			emit(Measurement{ID: AuditEvents, Count: v.Count, Labels: []Label{{"stage", string(v.Stage)}, {"result", string(v.Result)}}})
		case o.AuditOmittedFields:
			if v.Stage != "" || v.Result != "" {
				return
			}
			field := normalize(v.Field, o.FieldUnknown, o.FieldRequestID, o.FieldDeviceID, o.FieldSubjectHint, o.FieldActorName, o.FieldClientIP, o.FieldUnknown)
			emit(Measurement{ID: AuditOmittedFields, Count: v.Count, Labels: []Label{{"field", string(field)}}})
		}
	case o.MaintenanceObservation:
		if v.Count < 0 || v.Duration < 0 {
			return
		}
		task := normalize(v.Task, o.TaskOther, o.TaskCleanupSignInCompletionFlows, o.TaskPurgeUsers, o.TaskCleanupSessions, o.TaskAuditRetention, o.TaskRekeyDigests, o.TaskReencryptSubjects, o.TaskOther)
		switch v.Kind {
		case o.MaintenanceRound:
			if v.Task != "" || v.Count != 0 || v.Duration != 0 || !oneOf(v.Result, o.ResultAcquired, o.ResultSkipped, o.ResultError, o.ResultCanceled, o.ResultDeadlineExceeded) {
				return
			}
			emit(Measurement{ID: MaintenanceRounds, Count: 1, Labels: []Label{{"result", string(v.Result)}}})
		case o.MaintenanceRun:
			if v.Count != 0 || !oneOf(v.Result, o.ResultSuccess, o.ResultError, o.ResultCanceled, o.ResultDeadlineExceeded, o.ResultPanic) {
				return
			}
			labels := []Label{{"task", string(task)}, {"result", string(v.Result)}}
			emit(Measurement{ID: MaintenanceRuns, Count: 1, Labels: copyLabels(labels)})
			emit(Measurement{ID: MaintenanceDuration, Value: v.Duration.Seconds(), Labels: labels})
		case o.MaintenanceItems:
			if v.Count == 0 || v.Result != "" || v.Duration != 0 {
				return
			}
			emit(Measurement{ID: MaintenanceItems, Count: v.Count, Labels: []Label{{"task", string(task)}}})
		}
	}
}

func copyLabels(in []Label) []Label { return append([]Label(nil), in...) }
func oneOf[T ~string](value T, allowed ...T) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func normalize[T ~string](value, fallback T, allowed ...T) T {
	if oneOf(value, allowed...) {
		return value
	}
	return fallback
}
func businessResult(result o.Result) bool {
	return oneOf(result, o.ResultSuccess, o.ResultPending, o.ResultRejected, o.ResultDependencyError, o.ResultInternalError, o.ResultCanceled, o.ResultDeadlineExceeded)
}
func reason(value o.Reason) o.Reason {
	if value == "" {
		return o.ReasonNone
	}
	return normalize(value, o.ReasonUnknown, o.ReasonNone, o.ReasonInvalidArgument, o.ReasonIdentityKindDisabled, o.ReasonChannelDisabled,
		o.ReasonInvalidCredential, o.ReasonCodeInvalid, o.ReasonCodeExpired, o.ReasonCodeAttemptsExhausted, o.ReasonCooldown,
		o.ReasonTargetLimit, o.ReasonIPLimit, o.ReasonTargetVerifyLimit, o.ReasonUserFrozen, o.ReasonUserPendingDeletion,
		o.ReasonInvalidAccountState, o.ReasonIdentityConflict, o.ReasonIdentityRequired, o.ReasonIdentityLimit, o.ReasonFlowInvalid,
		o.ReasonFlowExpired, o.ReasonFlowConflict, o.ReasonUnknownToken, o.ReasonSessionRevoked, o.ReasonSessionExpired, o.ReasonTokenReuse,
		o.ReasonContainmentFailed, o.ReasonGraceReadError, o.ReasonGraceMiss, o.ReasonGracePairExpired, o.ReasonSendFailed,
		o.ReasonDependencyUnavailable, o.ReasonInternal, o.ReasonCanceled, o.ReasonDeadlineExceeded, o.ReasonUnknown)
}
func dependencyPair(component o.Component, operation o.DependencyOperation) bool {
	switch component {
	case o.ComponentCodeStore:
		return oneOf(operation, o.DependencyIssue, o.DependencyVerify, o.DependencyDiscard)
	case o.ComponentGraceCache:
		return oneOf(operation, o.DependencyGet, o.DependencyPut)
	case o.ComponentRevocation:
		return oneOf(operation, o.DependencyCheck, o.DependencyRevoke)
	case o.ComponentIDP:
		return oneOf(operation, o.DependencyVerifyWechat, o.DependencyVerifyApple, o.DependencyVerifyGoogle, o.DependencyVerifyFacebook, o.DependencyVerifyAlipay, o.DependencyVerifyQQ, o.DependencyVerifyWeibo, o.DependencyVerifyOIDC, o.DependencyVerifyProvider)
	case o.ComponentSender:
		return oneOf(operation, o.DependencySMS, o.DependencyEmail)
	}
	return false
}
func auditPair(stage o.AuditStage, result o.Result) bool {
	switch stage {
	case o.AuditMapping:
		return result == o.ResultFailed
	case o.AuditAdmission:
		return oneOf(result, o.ResultInvalid, o.ResultDropped)
	case o.AuditWrite:
		return oneOf(result, o.ResultWritten, o.ResultDuplicate, o.ResultFailed, o.ResultInvalid, o.ResultConflict)
	}
	return false
}
