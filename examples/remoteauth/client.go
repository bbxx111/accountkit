// Package remoteauth demonstrates per-request authentication for a business
// service. It is optional example code, not a dependency of the accountkit library.
package remoteauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/ids"
)

// ErrUnavailable means the dependency failed, not that the user must log in again.
// Raw HTTP/network errors are deliberately excluded from this error's chain.
var ErrUnavailable = errors.New("remote authentication unavailable")

// Config keeps service credentials separate from the consumer token. AllowHTTP
// is only for explicit local development; production must use HTTPS.
type Config struct {
	Endpoint, ClientID, ClientSecret string
	HTTPClient                       *http.Client
	Timeout                          time.Duration
	AllowHTTP                        bool
}

// Client performs a fresh request each time and never caches active results.
type Client struct {
	endpoint, clientID, clientSecret string
	httpClient                       http.Client
	timeout                          time.Duration
}

// Result contains only authorization context, without identity profile data.
type Result struct {
	Active                    bool
	Subject, SessionID, Scope string
	AuthTime                  time.Time
}

// NewClient snapshots the HTTP client and prevents redirects from forwarding a
// consumer token or Basic credential to another endpoint. Timeout covers the body.
func NewClient(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(cfg.AllowHTTP && u.Scheme == "http")) || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || cfg.ClientID == "" || strings.ContainsAny(cfg.ClientID, ":\r\n") || cfg.ClientSecret == "" || cfg.Timeout < 0 {
		return nil, errors.New("remoteauth: valid endpoint, client credentials and timeout required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Second
	}
	c := &Client{endpoint: cfg.Endpoint, clientID: cfg.ClientID, clientSecret: cfg.ClientSecret, timeout: cfg.Timeout}
	if cfg.HTTPClient != nil {
		c.httpClient = *cfg.HTTPClient
	}
	c.httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return c, nil
}

// Introspect submits the consumer token only in the form body. All remote failure
// modes produce ErrUnavailable; active:false is returned as a successful result.
func (c *Client) Introspect(ctx context.Context, rawToken string) (Result, error) {
	if c == nil || c.endpoint == "" {
		return Result{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	body := url.Values{"token": {rawToken}, "token_type_hint": {"access_token"}}.Encode()
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, strings.NewReader(body))
	if err != nil {
		return Result{}, ErrUnavailable
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Accept", "application/json")
	r.SetBasicAuth(c.clientID, c.clientSecret)
	response, err := c.httpClient.Do(r)
	if err != nil {
		return Result{}, ErrUnavailable
	}
	defer response.Body.Close()
	ct, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != http.StatusOK || err != nil || ct != "application/json" {
		return Result{}, ErrUnavailable
	}
	const maxResponse = 64 * 1024
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(raw) > maxResponse {
		return Result{}, ErrUnavailable
	}
	return parseResponse(raw)
}

func parseResponse(raw []byte) (Result, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return Result{}, ErrUnavailable
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return Result{}, ErrUnavailable
		}
		key, ok := token.(string)
		if !ok {
			return Result{}, ErrUnavailable
		}
		if _, duplicate := fields[key]; duplicate {
			return Result{}, ErrUnavailable
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return Result{}, ErrUnavailable
		}
		fields[key] = value
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return Result{}, ErrUnavailable
	}
	if _, err = d.Token(); err != io.EOF {
		return Result{}, ErrUnavailable
	}
	var active *bool
	if json.Unmarshal(fields["active"], &active) != nil || active == nil {
		return Result{}, ErrUnavailable
	}
	if !*active {
		if len(fields) != 1 {
			return Result{}, ErrUnavailable
		}
		return Result{}, nil
	}
	var result Result
	var authTime *int64
	if json.Unmarshal(fields["sub"], &result.Subject) != nil || json.Unmarshal(fields["sid"], &result.SessionID) != nil || json.Unmarshal(fields["scope"], &result.Scope) != nil || json.Unmarshal(fields["auth_time"], &authTime) != nil || authTime == nil || !ids.Valid(ids.User, result.Subject) || !ids.Valid(ids.Session, result.SessionID) || strings.TrimSpace(result.Scope) == "" {
		return Result{}, ErrUnavailable
	}
	result.Active = true
	result.AuthTime = time.Unix(*authTime, 0)
	return result, nil
}
