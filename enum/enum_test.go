package enum_test

import (
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestUserStateValuesArePinned(t *testing.T) {
	if enum.UserActive != 1 || enum.UserFrozen != 2 || enum.UserPendingDeletion != 3 || enum.UserDeleted != 4 {
		t.Fatalf("UserState values drifted: %d %d %d %d", enum.UserActive, enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted)
	}
	if enum.IdentityPhone != "PHONE" || enum.IdentityEmail != "EMAIL" || enum.IdentityWeChat != "WECHAT" || enum.IdentityApple != "APPLE" || enum.IdentityGoogle != "GOOGLE" || enum.IdentityFacebook != "FACEBOOK" {
		t.Fatal("IdentityKind values drifted")
	}
	if enum.RevokeUserLogout != 1 || enum.RevokeReplacedByRelogin != 6 || enum.RevokeAdmin != 7 {
		t.Fatal("RevokeReason values drifted")
	}
	if enum.PurposeSignIn != 1 || enum.PurposeBind != 2 || enum.PurposeReauth != 3 {
		t.Fatal("CodePurpose values drifted")
	}
	if enum.EventCodeSent != 1 || enum.EventAdminForbidden != 21 {
		t.Fatalf("EventType values drifted: first=%d last=%d", enum.EventCodeSent, enum.EventAdminForbidden)
	}
}

func TestStringAndParseRoundTrip(t *testing.T) {
	for _, s := range []enum.UserState{enum.UserActive, enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted} {
		got, err := enum.ParseUserState(s.String())
		if err != nil || got != s {
			t.Fatalf("UserState %d: %q -> %v, %v", s, s.String(), got, err)
		}
	}
	if enum.UserPendingDeletion.String() != "PENDING_DELETION" || enum.RevokeReplacedByRelogin.String() != "REPLACED_BY_RELOGIN" || enum.EventSignIn.String() != "SIGN_IN" {
		t.Fatal("UPPER_SNAKE spelling mismatch")
	}
	for _, k := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat, enum.IdentityApple, enum.IdentityGoogle, enum.IdentityFacebook} {
		got, err := enum.ParseIdentityKind(k.String())
		if err != nil || got != k {
			t.Fatalf("IdentityKind %s round trip failed", k)
		}
	}
}

func TestParseRejectsLowercaseAndUnknown(t *testing.T) {
	if _, err := enum.ParseUserState("active"); err == nil {
		t.Fatal("lowercase must be rejected")
	}
	if _, err := enum.ParseUserState("UNSPECIFIED"); err == nil {
		t.Fatal("UNSPECIFIED must be rejected as input")
	}
	if _, err := enum.ParseIdentityKind("facebook"); err == nil {
		t.Fatal("lowercase identity kind must be rejected")
	}
	if k, err := enum.ParseIdentityKind("GOOGLE"); err != nil || k != enum.IdentityGoogle {
		t.Fatal("GOOGLE must parse")
	}
}

func TestValidAndUnknownString(t *testing.T) {
	if enum.UserState(0).Valid() || enum.UserState(9).Valid() || !enum.UserActive.Valid() {
		t.Fatal("Valid mismatch")
	}
	if enum.UserState(9).String() != "UNSPECIFIED" {
		t.Fatalf("unknown String = %q", enum.UserState(9).String())
	}
}

func TestIdentityKindHelpers(t *testing.T) {
	if !enum.IdentityPhone.IsAnchor() || !enum.IdentityEmail.IsAnchor() || enum.IdentityWeChat.IsAnchor() || enum.IdentityApple.IsAnchor() || enum.IdentityGoogle.IsAnchor() || enum.IdentityFacebook.IsAnchor() {
		t.Fatal("IsAnchor mismatch")
	}
	if !enum.IdentityPhone.IsChannel() || enum.IdentityWeChat.IsChannel() || enum.IdentityGoogle.IsChannel() || enum.IdentityFacebook.IsChannel() {
		t.Fatal("IsChannel mismatch")
	}
	if enum.PurposeSignIn.Key() != "signin" || enum.PurposeBind.Key() != "bind" || enum.PurposeReauth.Key() != "reauth" {
		t.Fatal("CodePurpose.Key mismatch")
	}
}

func TestIdentityReplacementEnumValues(t *testing.T) {
	if enum.RevokeAdmin != 7 || enum.RevokeIdentityReplaced != 8 || enum.EventAdminForbidden != 21 || enum.EventIdentityReplaceRejected != 22 {
		t.Fatal("append-only enum values changed")
	}
	r, err := enum.ParseRevokeReason("IDENTITY_REPLACED")
	if err != nil || r != enum.RevokeIdentityReplaced || !r.Valid() || r.String() != "IDENTITY_REPLACED" {
		t.Fatalf("revoke roundtrip: %v %v", r, err)
	}
	e, err := enum.ParseEventType("IDENTITY_REPLACE_REJECTED")
	if err != nil || e != enum.EventIdentityReplaceRejected || !e.Valid() || e.String() != "IDENTITY_REPLACE_REJECTED" {
		t.Fatalf("event roundtrip: %v %v", e, err)
	}
}

func TestIdentityUnboundRevokeReason(t *testing.T) {
	if enum.RevokeAdmin != 7 || enum.RevokeIdentityReplaced != 8 || enum.RevokeIdentityUnbound != 9 {
		t.Fatal("append-only revoke values changed")
	}
	r, err := enum.ParseRevokeReason("IDENTITY_UNBOUND")
	if err != nil || r != enum.RevokeIdentityUnbound || !r.Valid() || r.String() != "IDENTITY_UNBOUND" || enum.RevokeReason(10).Valid() {
		t.Fatalf("unbind revoke reason: %v %v", r, err)
	}
}

func TestIdentityKindFormat(t *testing.T) {
	valid := []string{"AB", "A_1", "CORP_SSO", "A" + strings.Repeat("B", 31)}
	for _, v := range valid {
		k, err := enum.ParseIdentityKind(v)
		if err != nil || !k.Valid() || string(k) != v {
			t.Fatalf("%q must be a valid identity kind: %v", v, err)
		}
	}
	invalid := []string{"", "A", "A" + strings.Repeat("B", 32), "wechat", "1A", "_A", "A-B", "A B", "ÄB", "UNSPECIFIED "}
	for _, v := range invalid {
		if _, err := enum.ParseIdentityKind(v); err == nil || enum.IdentityKind(v).Valid() {
			t.Fatalf("%q must be rejected", v)
		}
	}
	if enum.IdentityKindUnspecified.Valid() || enum.IdentityKindUnspecified.String() != "UNSPECIFIED" {
		t.Fatal("empty identity kind must be unspecified")
	}
}

func TestIdentityKindBuiltin(t *testing.T) {
	for _, k := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat, enum.IdentityApple, enum.IdentityGoogle, enum.IdentityFacebook} {
		if !k.IsBuiltin() {
			t.Fatalf("%s must be builtin", k)
		}
	}
	if enum.IdentityKind("CORP_SSO").IsBuiltin() || enum.IdentityKind("CORP_SSO").IsAnchor() || enum.IdentityKind("CORP_SSO").IsChannel() {
		t.Fatal("configured kinds are neither builtin nor anchors")
	}
}

func TestIdentityFacebookIsBuiltinThirdParty(t *testing.T) {
	k, err := enum.ParseIdentityKind("FACEBOOK")
	if err != nil || k != enum.IdentityFacebook || !k.IsBuiltin() || k.IsAnchor() || k.IsChannel() {
		t.Fatalf("FACEBOOK: %v %v builtin=%v", k, err, k.IsBuiltin())
	}
}

func TestEventProviderDataDeletionProcessedAppended(t *testing.T) {
	if enum.EventProviderDataDeletionProcessed != 27 || enum.EventSignInCompletionRejected != 26 {
		t.Fatal("event values must be append-only")
	}
	e, err := enum.ParseEventType("PROVIDER_DATA_DELETION_PROCESSED")
	if err != nil || e != enum.EventProviderDataDeletionProcessed || !e.Valid() || enum.EventType(28).Valid() {
		t.Fatalf("parse: %v %v", e, err)
	}
}
