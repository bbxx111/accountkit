package tokens_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/bbxx111/accountkit/internal/testkeys"
	"github.com/bbxx111/accountkit/tokens"
)

func keys(vs ...int) map[uint16]*ecdsa.PrivateKey {
	m := make(map[uint16]*ecdsa.PrivateKey, len(vs))
	for _, v := range vs {
		m[uint16(v)] = testkeys.JWT(v)
	}
	return m
}

func opts() tokens.Options {
	return tokens.Options{Keys: keys(1, 2), Active: 2, Issuer: "shifang", Audience: "app"}
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
	if hdr.Header["kid"] != "2" || hdr.Header["alg"] != "ES256" || hdr.Header["typ"] != "at+jwt" {
		t.Fatalf("header = %v", hdr.Header)
	}
}

func TestParseAcceptsOldKeyVersionDuringOverlap(t *testing.T) {
	old, _ := tokens.NewSigner(tokens.Options{Keys: keys(1), Active: 1, Issuer: "shifang", Audience: "app"})
	tok, _, _ := old.Sign(claims(), time.Now(), time.Minute)
	cur, _ := tokens.NewSigner(opts()) // active 2，仍配置 1
	if _, err := cur.Parse(tok); err != nil {
		t.Fatalf("token signed with retired-but-configured kid must verify: %v", err)
	}
	only2, _ := tokens.NewSigner(tokens.Options{Keys: keys(2), Active: 2, Issuer: "shifang", Audience: "app"})
	if _, err := only2.Parse(tok); !errors.Is(err, tokens.ErrInvalidToken) {
		t.Fatalf("unknown kid must be ErrInvalidToken: %v", err)
	}
}

// signRaw 用任意 header/方法/密钥签发，构造各种畸形令牌。
func signRaw(t *testing.T, method jwt.SigningMethod, header map[string]any, c jwt.MapClaims, key any) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, c)
	delete(tok.Header, "typ")
	for k, v := range header {
		tok.Header[k] = v
	}
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseRejects(t *testing.T) {
	s, _ := tokens.NewSigner(opts())
	now := time.Now()
	good, _, _ := s.Sign(claims(), now, time.Minute)

	// 篡改载荷（payload 段被破坏，JSON 都无法解码）
	parts := strings.Split(good, ".")
	tampered := parts[0] + "." + parts[1][:len(parts[1])-2] + "xx." + parts[2]
	// 只改签名段：header 与 payload 保持有效，必须由 ECDSA 校验拒绝
	sig := []byte(parts[2])
	if sig[0] == 'A' {
		sig[0] = 'B'
	} else {
		sig[0] = 'A'
	}
	badSig := parts[0] + "." + parts[1] + "." + string(sig)
	// 同 kid、不同密钥签发：模拟攻击者持有错误密钥
	forgedKey, _ := tokens.NewSigner(tokens.Options{Keys: map[uint16]*ecdsa.PrivateKey{2: testkeys.JWT(9)}, Active: 2, Issuer: "shifang", Audience: "app"})
	wrongKeyTok, _, _ := forgedKey.Sign(claims(), now, time.Minute)
	// 过期（超出 30s 漂移）
	expired, _, _ := s.Sign(claims(), now.Add(-2*time.Minute), time.Minute)
	// 漂移内过期：应通过
	graced, _, _ := s.Sign(claims(), now.Add(-70*time.Second), time.Minute)
	// 错误 aud / iss
	other, _ := tokens.NewSigner(tokens.Options{Keys: keys(2), Active: 2, Issuer: "shifang", Audience: "other"})
	wrongAud, _, _ := other.Sign(claims(), now, time.Minute)
	otherIss, _ := tokens.NewSigner(tokens.Options{Keys: keys(2), Active: 2, Issuer: "evil", Audience: "app"})
	wrongIss, _, _ := otherIss.Sign(claims(), now, time.Minute)

	valid := jwt.MapClaims{"iss": "shifang", "aud": "app", "sub": "u_0123456789abc", "sid": "s_0123456789abc", "scope": "user", "exp": now.Add(time.Minute).Unix()}
	k2 := testkeys.JWT(2)
	at := map[string]any{"kid": "2", "typ": "at+jwt"}
	// 无 exp
	noExp := signRaw(t, jwt.SigningMethodES256, at, jwt.MapClaims{"iss": "shifang", "aud": "app", "sub": "u_x", "sid": "s_x", "scope": "user"}, k2)
	// alg=none
	noneTok := signRaw(t, jwt.SigningMethodNone, at, valid, jwt.UnsafeAllowNoneSignatureType)
	// 缺 kid / 非数字 kid
	noKid := signRaw(t, jwt.SigningMethodES256, map[string]any{"typ": "at+jwt"}, valid, k2)
	badKid := signRaw(t, jwt.SigningMethodES256, map[string]any{"kid": "abc", "typ": "at+jwt"}, valid, k2)
	// typ 缺失或不是 at+jwt
	noTyp := signRaw(t, jwt.SigningMethodES256, map[string]any{"kid": "2"}, valid, k2)
	wrongTyp := signRaw(t, jwt.SigningMethodES256, map[string]any{"kid": "2", "typ": "JWT"}, valid, k2)
	// 算法混淆：以公钥字节作 HMAC 密钥签 HS256
	pubDER, err := x509.MarshalPKIXPublicKey(&k2.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	hsWithPub := signRaw(t, jwt.SigningMethodHS256, at, valid, pubDER)
	// 以非压缩点字节作 HMAC 密钥的变体
	pubRaw, _ := k2.PublicKey.Bytes()
	hsWithRaw := signRaw(t, jwt.SigningMethodHS256, at, valid, pubRaw)
	// ES384 / RS 等其他算法
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	es384 := signRaw(t, jwt.SigningMethodES384, at, valid, p384)

	if _, err := s.Parse(graced); err != nil {
		t.Fatalf("within leeway must pass: %v", err)
	}
	for name, tok := range map[string]string{
		"corrupt payload": tampered, "forged signature": badSig, "wrong key same kid": wrongKeyTok,
		"expired": expired, "wrong aud": wrongAud, "wrong iss": wrongIss,
		"no exp": noExp, "alg none": noneTok, "no kid": noKid, "non-numeric kid": badKid,
		"no typ": noTyp, "wrong typ": wrongTyp, "hs256 with public key": hsWithPub, "hs256 with raw public key": hsWithRaw,
		"es384": es384, "garbage": "a.b.c", "empty": "",
	} {
		if _, err := s.Parse(tok); !errors.Is(err, tokens.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", name, err)
		}
	}
	// 防御性：HMAC 构造本身确实生成了"有效形状"的令牌，而非因签名失败被提前拒绝
	mac := hmac.New(sha256.New, pubDER)
	hp := strings.Split(hsWithPub, ".")
	mac.Write([]byte(hp[0] + "." + hp[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != hp[2] {
		t.Fatal("hs256 forgery fixture is malformed")
	}
}

func TestNewSignerValidates(t *testing.T) {
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	bad := map[string]tokens.Options{
		"no keys":        {Keys: nil, Active: 1, Issuer: "i", Audience: "a"},
		"active missing": {Keys: keys(1), Active: 2, Issuer: "i", Audience: "a"},
		"nil key":        {Keys: map[uint16]*ecdsa.PrivateKey{1: nil}, Active: 1, Issuer: "i", Audience: "a"},
		"p384":           {Keys: map[uint16]*ecdsa.PrivateKey{1: p384}, Active: 1, Issuer: "i", Audience: "a"},
		"no issuer":      {Keys: keys(1), Active: 1, Issuer: "", Audience: "a"},
		"no audience":    {Keys: keys(1), Active: 1, Issuer: "i", Audience: ""},
	}
	for name, o := range bad {
		if _, err := tokens.NewSigner(o); err == nil {
			t.Errorf("%s: expected error", name)
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
	custom, err := tokens.NewSigner(tokens.Options{Keys: keys(1), Active: 1, Issuer: "i", Audience: "a", Leeway: 5 * time.Second})
	if err != nil || custom.Leeway() != 5*time.Second {
		t.Fatalf("configured leeway must be honored: %v %v", custom.Leeway(), err)
	}
}

type jwk struct {
	Kty, Crv, X, Y, Kid, Alg, Use string
	D                             *string `json:"d"`
}

func TestJWKS(t *testing.T) {
	s, err := tokens.NewSigner(opts())
	if err != nil {
		t.Fatal(err)
	}
	raw := s.JWKS()
	if strings.Contains(string(raw), `"d"`) {
		t.Fatalf("JWKS leaks private material: %s", raw)
	}
	var set struct{ Keys []jwk }
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	if len(set.Keys) != 2 || set.Keys[0].Kid != "1" || set.Keys[1].Kid != "2" {
		t.Fatalf("keys = %+v", set.Keys)
	}
	for _, k := range set.Keys {
		if k.Kty != "EC" || k.Crv != "P-256" || k.Alg != "ES256" || k.Use != "sig" || k.D != nil {
			t.Fatalf("jwk = %+v", k)
		}
	}
	// 用 JWKS 中的公钥验证对应 kid 的令牌
	tok, _, _ := s.Sign(claims(), time.Now(), time.Minute)
	pub := pubFromJWK(t, set.Keys[1])
	if _, err := jwt.Parse(tok, func(*jwt.Token) (any, error) { return pub, nil }, jwt.WithValidMethods([]string{"ES256"})); err != nil {
		t.Fatalf("JWKS key must verify active token: %v", err)
	}
	// 返回副本：调用方修改不影响后续结果
	raw[0] = 'X'
	if s.JWKS()[0] != '{' {
		t.Fatal("JWKS must return a copy")
	}
}

func TestJWKSFixedWidthCoordinates(t *testing.T) {
	for i := 0; i < 64; i++ {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		s, err := tokens.NewSigner(tokens.Options{Keys: map[uint16]*ecdsa.PrivateKey{1: k}, Active: 1, Issuer: "i", Audience: "a"})
		if err != nil {
			t.Fatal(err)
		}
		var set struct{ Keys []jwk }
		_ = json.Unmarshal(s.JWKS(), &set)
		x, _ := base64.RawURLEncoding.DecodeString(set.Keys[0].X)
		y, _ := base64.RawURLEncoding.DecodeString(set.Keys[0].Y)
		if len(x) != 32 || len(y) != 32 {
			t.Fatalf("coordinates must be 32 bytes: x=%d y=%d", len(x), len(y))
		}
	}
}

func pubFromJWK(t *testing.T, k jwk) *ecdsa.PublicKey {
	t.Helper()
	x, err1 := base64.RawURLEncoding.DecodeString(k.X)
	y, err2 := base64.RawURLEncoding.DecodeString(k.Y)
	if err1 != nil || err2 != nil {
		t.Fatal("bad coordinates")
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
	if err != nil {
		t.Fatal(err)
	}
	return pub
}
