package remoteauth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/authn"
	"github.com/bbxx111/accountkit/user"
)

// ProtectedHandler is an example business endpoint, mounted as e.g.
// GET /documents/{resource} with net/http.ServeMux. A real handler would read or
// modify its resource after these checks; this example returns 204 on success.
//
// Tiered verification: with a Verifier the token is first checked locally against
// the JWKS. Ordinary routes stop there and accept revocation delay up to the access
// TTL. Critical routes (Critical=true), or handlers without a Verifier, additionally
// call introspection on every request, which reflects revocation immediately.
type ProtectedHandler struct {
	Client     *Client
	Verifier   *Verifier
	Critical   bool
	MaxAuthAge time.Duration
	Now        func() time.Time
	// OwnsResource must constrain the business lookup by BOTH userID and resourceID
	// (e.g. WHERE user_id=$1 AND id=$2); false covers missing and other-owner rows.
	OwnsResource func(ctx context.Context, userID, resourceID string) (bool, error)
}

func (h ProtectedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	headers := r.Header.Values("Authorization")
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if len(headers) != 1 || !ok || !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t\r\n") {
		authn.WriteUnauthenticated(w, authn.ReasonTokenMissing, "bearer token required")
		return
	}
	introspect := h.Verifier == nil || h.Critical
	if (introspect && h.Client == nil) || h.OwnsResource == nil || h.MaxAuthAge <= 0 {
		unavailable(w)
		return
	}
	var principal Result
	var err error
	if h.Verifier != nil {
		// Local check first, so invalid tokens never cost an introspection call.
		if principal, err = h.Verifier.Verify(r.Context(), token); err != nil {
			unavailable(w)
			return
		}
		if !principal.Active {
			authn.WriteUnauthenticated(w, authn.ReasonTokenInvalid, "invalid or expired access token")
			return
		}
	}
	if introspect {
		if principal, err = h.Client.Introspect(r.Context(), token); err != nil {
			unavailable(w)
			return
		}
		if !principal.Active {
			authn.WriteUnauthenticated(w, authn.ReasonTokenInvalid, "invalid or expired access token")
			return
		}
	}
	if principal.Scope != user.ScopeUser {
		apierror.Write(w, apierror.New(apierror.StatusPermissionDenied, "INSUFFICIENT_SCOPE", "token scope does not permit this operation"))
		return
	}
	now := time.Now()
	if h.Now != nil {
		now = h.Now()
	}
	if principal.AuthTime.IsZero() || principal.AuthTime.After(now) || now.Sub(principal.AuthTime) > h.MaxAuthAge {
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "REAUTHENTICATION_REQUIRED", "recent authentication required for this operation"))
		return
	}
	resourceID := r.PathValue("resource")
	if resourceID == "" {
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_RESOURCE", "resource is required"))
		return
	}
	owns, err := h.OwnsResource(r.Context(), principal.Subject, resourceID)
	if err != nil {
		unavailable(w)
		return
	}
	if !owns {
		apierror.Write(w, apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func unavailable(w http.ResponseWriter) {
	apierror.Write(w, apierror.New(apierror.StatusUnavailable, "DEPENDENCY_UNAVAILABLE", "authentication or resource service temporarily unavailable"))
}
