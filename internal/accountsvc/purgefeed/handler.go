// Package purgefeed exposes the service-only purged-user feed: business services page through
// purged account IDs with a cursor and idempotently clean their own user_id data.
package purgefeed

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/internal/accountsvc/basicclients"
	"github.com/bbxx111/accountkit/user"
)

const (
	defaultPageSize = 100
	tokenPrefix     = "v1:"
	// lookupTimeout bounds the database read so a hung pool yields 503 instead of holding the request.
	lookupTimeout = 3 * time.Second
)

// Lister reads purged users after a cursor; *user.Service.ListPurgedUsers satisfies it.
type Lister func(ctx context.Context, after int64, limit int) ([]user.PurgedUser, int64, error)

type handler struct {
	clients *basicclients.Matcher // nil when ACCOUNTSVC_PURGE_FEED_CLIENTS is not configured
	list    Lister
}

// New returns the feed handler. With no clients the route answers 503 PURGE_FEED_NOT_CONFIGURED,
// so callers can tell an unconfigured feed from a wrong path.
func New(clients map[string][]string, list Lister) (http.Handler, error) {
	if list == nil {
		return nil, errors.New("accountsvc: purged user lister is required")
	}
	h := &handler{list: list}
	if len(clients) > 0 {
		m, err := basicclients.NewMatcher(clients)
		if err != nil {
			return nil, errors.New("accountsvc: invalid ACCOUNTSVC_PURGE_FEED_CLIENTS")
		}
		h.clients = m
	}
	return h, nil
}

type purgedUser struct {
	UserID    string `json:"user_id"`
	PurgeTime string `json:"purge_time"`
}

type page struct {
	PurgedUsers   []purgedUser `json:"purged_users"`
	NextPageToken string       `json:"next_page_token"`
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.clients == nil {
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "PURGE_FEED_NOT_CONFIGURED", "purged user feed is not configured on this service"))
		return
	}
	if !h.clients.Authorized(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="accountsvc-purge-feed"`)
		apierror.Write(w, apierror.New(apierror.StatusUnauthenticated, "INVALID_CLIENT", "valid service client credentials are required"))
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	for _, name := range []string{"page_size", "page_token"} {
		if len(query[name]) > 1 {
			apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", name+" must appear at most once"))
			return
		}
	}
	size := defaultPageSize
	if raw := query.Get("page_size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > user.MaxPurgedUserPage {
			apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", "page_size must be an integer within 1..1000"))
			return
		}
		size = n
	}
	after, ok := decodeToken(query.Get("page_token"))
	if !ok {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_PAGE_TOKEN", "page_token is invalid; restart from the beginning or resume from a saved token"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	records, next, err := h.list(ctx, after, size)
	switch {
	case errors.Is(err, user.ErrPurgedUserCursorAhead):
		// 数据库恢复或重新部署后序号回退：要求从头重拉，而不是静默跳过新记录。
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_PAGE_TOKEN", "page_token is ahead of the feed (the service database was restored or re-provisioned); restart from the beginning"))
		return
	case errors.Is(err, user.ErrInvalidArgument):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", "invalid page request"))
		return
	case err != nil:
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "DEPENDENCY_UNAVAILABLE", "purged user feed temporarily unavailable"))
		return
	}
	out := page{PurgedUsers: make([]purgedUser, 0, len(records)), NextPageToken: encodeToken(next)}
	for _, rec := range records {
		out.PurgedUsers = append(out.PurgedUsers, purgedUser{UserID: rec.UserID, PurgeTime: rec.PurgeTime.UTC().Format(time.RFC3339Nano)})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

// encodeToken returns the opaque cursor; it is always present so callers can persist their
// position, including when they have caught up.
func encodeToken(seq int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(tokenPrefix + strconv.FormatInt(seq, 10)))
}

// decodeToken accepts an empty token (start) or a canonical non-negative cursor.
func decodeToken(raw string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, false
	}
	digits, ok := strings.CutPrefix(string(b), tokenPrefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != digits {
		return 0, false
	}
	return n, true
}
