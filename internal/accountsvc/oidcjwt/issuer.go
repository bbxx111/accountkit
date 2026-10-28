package oidcjwt

import (
	"errors"
	"net/url"
	"strings"
)

// ValidIssuer reports whether issuer is an absolute HTTPS URL without credentials,
// query, fragment or surrounding whitespace.
func ValidIssuer(issuer string) bool {
	if validateHTTPS(issuer) != nil {
		return false
	}
	u, _ := url.Parse(issuer)
	return u.RawQuery == "" && !u.ForceQuery
}

func validateHTTPS(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.Contains(value, "#") || u.Opaque != "" || strings.TrimSpace(value) != value {
		return errors.New("invalid HTTPS URL")
	}
	return nil
}
