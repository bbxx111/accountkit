package idp

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	alipayTokenMethod   = "alipay.system.oauth.token"
	alipayProfileMethod = "alipay.user.info.share"
	alipayTokenNode     = "alipay_system_oauth_token_response"
	alipayProfileNode   = "alipay_user_info_share_response"
)

var alipayTimezone = time.FixedZone("UTC+08:00", 8*60*60)

func (a *Alipay) request(ctx context.Context, appID, method string, params url.Values, budget time.Duration) (map[string]json.RawMessage, error) {
	child, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	params.Set("app_id", appID)
	params.Set("method", method)
	params.Set("format", "json")
	params.Set("charset", "utf-8")
	params.Set("sign_type", "RSA2")
	params.Set("timestamp", a.now().In(alipayTimezone).Format("2006-01-02 15:04:05"))
	params.Set("version", "1.0")
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params.Get(key))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "&")))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.config.apps[appID].privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "request signature")
	}
	params.Set("sign", base64.StdEncoding.EncodeToString(sig))
	req, err := http.NewRequestWithContext(child, http.MethodPost, a.config.gatewayURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "request construction")
	}
	// A consumed authorization code must never become a replayable request body.
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, alipayFailure(ErrUnavailable, "transport")
	}
	defer resp.Body.Close()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 || resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return nil, alipayFailure(ErrUnavailable, "http status")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, alipayFailure(ErrMisconfigured, "http status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAlipayBody+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, alipayFailure(ErrUnavailable, "response read")
	}
	if len(body) > maxAlipayBody {
		return nil, alipayFailure(ErrMisconfigured, "response limit")
	}
	node := alipayTokenNode
	if method == alipayProfileMethod {
		node = alipayProfileNode
	}
	outer, err := alipayObject(body)
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "response structure")
	}
	raw, success := outer[node]
	failure, failed := outer["error_response"]
	if success == failed {
		return nil, alipayFailure(ErrMisconfigured, "response node")
	}
	for key := range outer {
		if key != node && key != "error_response" && key != "sign" && key != "sign_type" {
			return nil, alipayFailure(ErrMisconfigured, "response field")
		}
	}
	if failed {
		raw = failure
	}
	sign, err := alipayString(outer, "sign")
	if err != nil || sign == "" {
		return nil, alipayFailure(ErrMisconfigured, "response signature")
	}
	if kind, err := alipayString(outer, "sign_type"); err != nil || kind != "" && kind != "RSA2" {
		return nil, alipayFailure(ErrMisconfigured, "response signature type")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(sign)
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "response signature")
	}
	digest = sha256.Sum256(raw)
	if rsa.VerifyPKCS1v15(a.config.apps[appID].publicKey, crypto.SHA256, digest[:], signature) != nil {
		return nil, alipayFailure(ErrMisconfigured, "response signature")
	}
	object, err := alipayObject(raw)
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "response node structure")
	}
	code, err := alipayString(object, "code")
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "response code")
	}
	sub, err := alipayString(object, "sub_code")
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "business error structure")
	}
	for _, field := range []string{"msg", "sub_msg"} {
		if _, err := alipayString(object, field); err != nil {
			return nil, alipayFailure(ErrMisconfigured, "business error structure")
		}
	}
	if failed && (code == "" || code == "10000") {
		return nil, alipayFailure(ErrMisconfigured, "error response code")
	}
	if failed || code != "" && code != "10000" {
		return nil, alipayBusinessError(sub, code)
	}
	if sub != "" {
		return nil, alipayFailure(ErrMisconfigured, "contradictory business result")
	}
	if method == alipayProfileMethod && code != "10000" {
		return nil, alipayFailure(ErrMisconfigured, "profile result code")
	}
	return object, nil
}

// Only verified, explicitly recognized codes affect classification. Messages are
// deliberately ignored: they may contain credentials or user supplied content.
func alipayBusinessError(sub, code string) error {
	switch sub {
	case "isv.code-invalid":
		return alipayFailure(ErrInvalidCredential, "authorization code rejected")
	case "isv.unmatched-app-id":
		return alipayFailure(ErrAppNotAllowed, "application mismatch")
	case "isv.grant-type-invalid", "isv.app-id-invalid", "isv.invalid-app-id", "isv.invalid-signature", "isv.invalid-sign-type", "isv.insufficient-isv-permissions", "isv.insufficient-user-permissions":
		return alipayFailure(ErrMisconfigured, "application configuration")
	case "isp.unknow-error":
		return alipayFailure(ErrUnavailable, "provider busy")
	}
	// 20000 is the documented generic service unavailable result.
	if code == "20000" {
		return alipayFailure(ErrUnavailable, "provider busy")
	}
	return alipayFailure(ErrMisconfigured, "unrecognized business error")
}

func alipayFailure(kind error, stage string) error { return fmt.Errorf("%w: alipay %s", kind, stage) }
