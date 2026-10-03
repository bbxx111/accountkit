package tokens_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/tokens"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func opts() tokens.Options {
	return tokens.Options{Keys: map[uint16][]byte{1: key(1), 2: key(2)}, Active: 2, Issuer: "shifang", Audience: "app"}
}

func claims() tokens.Claims {
	return tokens.Claims{UserID: "u_0123456789abc", SessionID: "s_0123456789abc", Scope: "user", AuthTime: time.Now().Add(-time.Minute).Truncate(time.Second)}
}

func TestSignParseRoundTrip(t *testing.T) {
	s, err := tokens.NewSigner(opts())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	tok, jti, err := s.Sign(claims(), now, 15*time.Minute)
	if err != nil || len(jti) != 32 {
		t.Fatalf("sign: jti=%q err=%v", jti, err)
	}
	got, err := s.Parse(tok)
	if err != nil {
		t.Fatal(err)
	}
	want := claims()
	if got.UserID != want.UserID || got.SessionID != want.SessionID || got.Scope != "user" || got.JTI != jti {
		t.Fatalf("claims = %+v", got)
	}
	if !got.AuthTime.Equal(want.AuthTime) || !got.IssuedAt.Equal(now) || !got.ExpiresAt.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("times = %+v", got)
	}
	hdr, _, _ := jwt.NewParser().ParseUnverified(tok, jwt.MapClaims{})
	if hdr.Header["kid"] != "2" || hdr.Header["alg"] != "HS256" {
		t.Fatalf("header = %v", hdr.Header)
	}
}

func TestParseAcceptsOldKeyVersionDuringOverlap(t *testing.T) {
	old, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{1: key(1)}, Active: 1, Issuer: "shifang", Audience: "app"})
	tok, _, _ := old.Sign(claims(), time.Now(), time.Minute)
	cur, _ := tokens.NewSigner(opts()) // active 2，仍配置 1
	if _, err := cur.Parse(tok); err != nil {
		t.Fatalf("token signed with retired-but-configured kid must verify: %v", err)
	}
	only2, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{2: key(2)}, Active: 2, Issuer: "shifang", Audience: "app"})
	if _, err := only2.Parse(tok); !errors.Is(err, tokens.ErrInvalidToken) {
		t.Fatalf("unknown kid must be ErrInvalidToken: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	s, _ := tokens.NewSigner(opts())
	now := time.Now()
	good, _, _ := s.Sign(claims(), now, time.Minute)

	// 篡改载荷（payload 段被破坏，JSON 都无法解码）
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + parts[1][:len(parts[1])-2] + "xx." + parts[2]
	// 只改签名段：header 与 payload 保持有效，必须由 HMAC 校验拒绝
	sigParts := strings.Split(good, ".")
	sig := []byte(sigParts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	badSig := sigParts[0] + "." + sigParts[1] + "." + string(sig)
	// 同 kid、不同密钥签发：模拟攻击者持有错误密钥
	forgedKey, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{2: key(9)}, Active: 2, Issuer: "shifang", Audience: "app"})
	wrongKeyTok, _, _ := forgedKey.Sign(claims(), now, time.Minute)
	// 过期（超出 30s 漂移）
	expired, _, _ := s.Sign(claims(), now.Add(-2*time.Minute), time.Minute)
	// 漂移内过期：应通过
	graced, _, _ := s.Sign(claims(), now.Add(-70*time.Second), time.Minute)
	// 错误 aud / iss
	other, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{2: key(2)}, Active: 2, Issuer: "shifang", Audience: "other"})
	wrongAud, _, _ := other.Sign(claims(), now, time.Minute)
	otherIss, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{2: key(2)}, Active: 2, Issuer: "evil", Audience: "app"})
	wrongIss, _, _ := otherIss.Sign(claims(), now, time.Minute)
	// 无 exp
	noExp := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "shifang", "aud": "app", "sub": "u_x", "sid": "s_x", "scope": "user"})
	noExp.Header["kid"] = "2"
	noExpTok, _ := noExp.SignedString(key(2))
	// alg=none
	none := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"iss": "shifang", "aud": "app", "exp": now.Add(time.Minute).Unix()})
	none.Header["kid"] = "2"
	noneTok, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	// 缺 kid
	noKid := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "shifang", "aud": "app", "exp": now.Add(time.Minute).Unix()})
	noKidTok, _ := noKid.SignedString(key(2))
	// 非数字 kid
	badKid := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "shifang", "aud": "app", "exp": now.Add(time.Minute).Unix()})
	badKid.Header["kid"] = "abc"
	badKidTok, _ := badKid.SignedString(key(2))

	if _, err := s.Parse(graced); err != nil {
		t.Fatalf("within leeway must pass: %v", err)
	}
	for name, tok := range map[string]string{
		"corrupt payload": tampered, "forged signature": badSig, "wrong key same kid": wrongKeyTok,
		"expired": expired, "wrong aud": wrongAud, "wrong iss": wrongIss,
		"no exp": noExpTok, "alg none": noneTok, "no kid": noKidTok, "non-numeric kid": badKidTok,
		"garbage": "a.b.c", "empty": "",
	} {
		if _, err := s.Parse(tok); !errors.Is(err, tokens.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
}

func TestNewSignerValidates(t *testing.T) {
	bad := []tokens.Options{
		{Keys: nil, Active: 1, Issuer: "i", Audience: "a"},
		{Keys: map[uint16][]byte{1: key(1)}, Active: 2, Issuer: "i", Audience: "a"},
		{Keys: map[uint16][]byte{1: key(1)[:31]}, Active: 1, Issuer: "i", Audience: "a"},
		{Keys: map[uint16][]byte{1: key(1)}, Active: 1, Issuer: "", Audience: "a"},
		{Keys: map[uint16][]byte{1: key(1)}, Active: 1, Issuer: "i", Audience: ""},
	}
	for i, o := range bad {
		if _, err := tokens.NewSigner(o); err == nil {
			t.Errorf("case %d: expected error", i)
		}
	}
	s, err := tokens.NewSigner(opts())
	if err != nil || s.Active() != 2 {
		t.Fatalf("valid options: %v", err)
	}
}

func TestLeewayDefaultAndOverride(t *testing.T) {
	def, err := tokens.NewSigner(opts())
	if err != nil || def.Leeway() != 30*time.Second {
		t.Fatalf("default leeway must be 30s: %v %v", def.Leeway(), err)
	}
	custom, err := tokens.NewSigner(tokens.Options{Keys: map[uint16][]byte{1: key(1)}, Active: 1, Issuer: "i", Audience: "a", Leeway: 5 * time.Second})
	if err != nil || custom.Leeway() != 5*time.Second {
		t.Fatalf("configured leeway must be honored: %v %v", custom.Leeway(), err)
	}
}
