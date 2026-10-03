package enum_test

import (
	"testing"

	"github.com/bbxx111/accountkit/enum"
)

func TestUserStateValuesArePinned(t *testing.T) {
	if enum.UserActive != 1 || enum.UserFrozen != 2 || enum.UserPendingDeletion != 3 || enum.UserDeleted != 4 {
		t.Fatalf("UserState values drifted: %d %d %d %d", enum.UserActive, enum.UserFrozen, enum.UserPendingDeletion, enum.UserDeleted)
	}
	if enum.IdentityPhone != 1 || enum.IdentityEmail != 2 || enum.IdentityWeChat != 3 || enum.IdentityApple != 4 {
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
	for _, k := range []enum.IdentityKind{enum.IdentityPhone, enum.IdentityEmail, enum.IdentityWeChat, enum.IdentityApple} {
		got, err := enum.ParseIdentityKind(k.String())
		if err != nil || got != k {
			t.Fatalf("IdentityKind %d round trip failed", k)
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
	if _, err := enum.ParseIdentityKind("GOOGLE"); err == nil {
		t.Fatal("unknown must be rejected")
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
	if !enum.IdentityPhone.IsAnchor() || !enum.IdentityEmail.IsAnchor() || enum.IdentityWeChat.IsAnchor() || enum.IdentityApple.IsAnchor() {
		t.Fatal("IsAnchor mismatch")
	}
	if !enum.IdentityPhone.IsChannel() || enum.IdentityWeChat.IsChannel() {
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
