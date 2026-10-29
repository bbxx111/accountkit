package oidcjwt

import (
	"errors"
	"net/url"
	"strings"
)

// ValidIssuer reports whether issuer is an absolute HTTPS URL without credentials,
// query, fragment or surrounding whitespace.
func ValidIssuer(issuer string) bool { return validIssuer(issuer, false) }

// ValidPlainIssuer is ValidIssuer that also accepts http://. It exists for accountsvc's
// development mode only; production configuration keeps using ValidIssuer.
func ValidPlainIssuer(issuer string) bool { return validIssuer(issuer, true) }

func validIssuer(issuer string, allowHTTP bool) bool {
	if validateURL(issuer, allowHTTP) != nil {
		return false
	}
	u, _ := url.Parse(issuer)
	return u.RawQuery == "" && !u.ForceQuery
}

// validateURL accepts an absolute HTTPS URL, or an HTTP one when allowHTTP is set, without
// credentials, fragment, opaque form or surrounding whitespace.
func validateURL(value string, allowHTTP bool) error {
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && !(allowHTTP && u.Scheme == "http")) || u.Hostname() == "" || u.User != nil || strings.Contains(value, "#") || u.Opaque != "" || strings.TrimSpace(value) != value {
		return errors.New("invalid issuer URL")
	}
	return nil
}
