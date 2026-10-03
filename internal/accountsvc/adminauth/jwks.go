package adminauth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"time"
)

const (
	maxResponseSize = 256 * 1024
	maxKeys         = 128
	keyLifetime     = time.Hour
	refreshInterval = time.Minute
)

type keyCache struct {
	keys        map[string]*rsa.PublicKey
	loadedAt    time.Time
	lastAttempt time.Time
	lastError   error
	refreshing  chan struct{}
}

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
		return errors.New("adminauth: provider returned non-200 response")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseSize {
		return errors.New("adminauth: provider response exceeds limit")
	}
	return json.Unmarshal(data, dest)
}

func (v *Verifier) loadKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
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
	if err := v.fetchJSON(ctx, v.jwksURL, &document); err != nil {
		return nil, err
	}
	if len(document.Keys) > maxKeys {
		return nil, errors.New("adminauth: too many JWKS keys")
	}
	keys := make(map[string]*rsa.PublicKey)
	seen := make(map[string]bool)
	for _, jwk := range document.Keys {
		if jwk.Kid == "" || len(jwk.Kid) > 256 {
			continue
		}
		if seen[jwk.Kid] {
			return nil, errors.New("adminauth: duplicate JWKS kid")
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
			return nil, errors.New("adminauth: malformed RSA modulus")
		}
		e, err := base64.RawURLEncoding.DecodeString(jwk.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("adminauth: malformed RSA exponent")
		}
		modulus := new(big.Int).SetBytes(n)
		exponent := new(big.Int).SetBytes(e).Int64()
		if modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || modulus.Bit(0) == 0 || exponent < 3 || exponent > 2147483647 || exponent%2 == 0 {
			return nil, errors.New("adminauth: unsafe RSA signing key")
		}
		keys[jwk.Kid] = &rsa.PublicKey{N: modulus, E: int(exponent)}
	}
	if len(keys) == 0 {
		return nil, errors.New("adminauth: no usable RS256 signing keys")
	}
	return keys, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	for {
		v.mu.Lock()
		now := v.now()
		if key := v.cache.keys[kid]; key != nil && now.Before(v.cache.loadedAt.Add(keyLifetime)) {
			v.mu.Unlock()
			return key, nil
		}
		if active := v.cache.refreshing; active != nil {
			v.mu.Unlock()
			select {
			case <-active:
				continue
			case <-ctx.Done():
				return nil, errUnavailable
			}
		}
		if !v.cache.lastAttempt.IsZero() && now.Sub(v.cache.lastAttempt) < refreshInterval {
			err := errInvalid
			if v.cache.lastError != nil {
				err = errUnavailable
			}
			v.mu.Unlock()
			return nil, err
		}
		active := make(chan struct{})
		v.cache.refreshing = active
		v.cache.lastAttempt = now
		v.mu.Unlock()
		keys, err := v.loadKeys(ctx)
		v.mu.Lock()
		v.cache.lastError = err
		if err == nil {
			v.cache.keys = keys
			v.cache.loadedAt = v.now()
		}
		v.cache.refreshing = nil
		close(active)
		v.mu.Unlock()
		if err != nil {
			return nil, errUnavailable
		}
	}
}
