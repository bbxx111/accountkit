package accountsvc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
)

type metadataKey struct{}
type metadata struct{ ip, id string }

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func requestID(r *http.Request) string {
	m, _ := r.Context().Value(metadataKey{}).(metadata)
	return m.id
}
func clientIP(r *http.Request) string {
	m, _ := r.Context().Value(metadataKey{}).(metadata)
	return m.ip
}

func requestMetadata(trusted []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if existing, ok := r.Context().Value(metadataKey{}).(metadata); ok && existing.id != "" {
			w.Header().Set("X-Request-Id", existing.id)
			next.ServeHTTP(w, r)
			return
		}
		id := ""
		values := r.Header.Values("X-Request-Id")
		if len(values) == 1 && requestIDPattern.MatchString(values[0]) {
			id = values[0]
		}
		if id == "" {
			var bytes [16]byte
			_, _ = rand.Read(bytes[:])
			id = hex.EncodeToString(bytes[:])
		}
		m := metadata{ip: resolveClientIP(r, trusted), id: id}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), metadataKey{}, m)))
	})
}

func resolveClientIP(r *http.Request, trusted []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	isTrusted := func(addr netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
	values := r.Header.Values("X-Forwarded-For")
	if !isTrusted(peer) || len(values) != 1 {
		return peer.String()
	}
	parts := strings.Split(values[0], ",")
	chain := make([]netip.Addr, len(parts))
	for i, part := range parts {
		addr, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil || addr.Zone() != "" {
			return peer.String()
		}
		chain[i] = addr.Unmap()
	}
	current := peer
	for i := len(chain) - 1; i >= 0; i-- {
		if !isTrusted(current) {
			break
		}
		current = chain[i]
	}
	return current.String()
}
