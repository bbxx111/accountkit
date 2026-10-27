package oidcjwt

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
)

const (
	maxResponseSize = 256 * 1024
	maxKeys         = 128
)

var errIssuerMismatch = errors.New("oidcjwt: discovered issuer mismatch")

func (v *Verifier) fetchJSON(ctx context.Context, address string, dest any) error {
	if err := validateHTTPS(address); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("oidcjwt: provider returned non-200 response")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseSize {
		return errors.New("oidcjwt: provider response exceeds limit")
	}
	return json.Unmarshal(data, dest)
}

// discover resolves the JWKS URI from the issuer's OpenID configuration.
func (v *Verifier) discover(ctx context.Context) (string, error) {
	var discovery struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := v.fetchJSON(ctx, strings.TrimRight(v.issuer, "/")+"/.well-known/openid-configuration", &discovery); err != nil {
		return "", err
	}
	if discovery.Issuer != v.issuer {
		return "", errIssuerMismatch
	}
	if err := validateHTTPS(discovery.JWKSURI); err != nil {
		return "", errors.New("oidcjwt: invalid JWKS URI")
	}
	return discovery.JWKSURI, nil
}

func (v *Verifier) loadKeys(ctx context.Context, jwksURL string) (map[string]*rsa.PublicKey, error) {
	var document struct {
		Keys []struct {
			Kid    string   `json:"kid"`
			Kty    string   `json:"kty"`
			Use    string   `json:"use"`
			Alg    string   `json:"alg"`
			N      string   `json:"n"`
			E      string   `json:"e"`
			KeyOps []string `json:"key_ops"`
		} `json:"keys"`
	}
	if err := v.fetchJSON(ctx, jwksURL, &document); err != nil {
		return nil, err
	}
	if len(document.Keys) > maxKeys {
		return nil, errors.New("oidcjwt: too many JWKS keys")
	}
	keys := make(map[string]*rsa.PublicKey)
	seen := make(map[string]bool)
	for _, jwk := range document.Keys {
		if jwk.Kid == "" || len(jwk.Kid) > 256 {
			continue
		}
		if seen[jwk.Kid] {
			return nil, errors.New("oidcjwt: duplicate JWKS kid")
		}
		seen[jwk.Kid] = true
		if jwk.Kty != "RSA" || (jwk.Use != "" && jwk.Use != "sig") || (jwk.Alg != "" && jwk.Alg != "RS256") {
			continue
		}
		if jwk.KeyOps != nil {
			verify := false
			for _, op := range jwk.KeyOps {
				if op == "verify" {
					verify = true
				}
			}
			if !verify {
				continue
			}
		}
		n, err := base64.RawURLEncoding.DecodeString(jwk.N)
		if err != nil {
			return nil, errors.New("oidcjwt: malformed RSA modulus")
		}
		e, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("oidcjwt: malformed RSA exponent")
		}
		modulus := new(big.Int).SetBytes(n)
		exponent := new(big.Int).SetBytes(e).Int64()
		if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || modulus.Bit(0) == 0 || exponent < 3 || exponent > 2147483647 || exponent%2 == 0 {
			return nil, errors.New("oidcjwt: unsafe RSA signing key")
		}
		keys[jwk.Kid] = &rsa.PublicKey{N: modulus, E: int(exponent)}
	}
	if len(keys) == 0 {
		return nil, errors.New("oidcjwt: no usable RS256 signing keys")
	}
	return keys, nil
}
