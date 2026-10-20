package enduser

import (
	"context"
	"net/http"
	"sort"

	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/user"
)

var _ CurrentSessionService = (*user.Service)(nil)

// CurrentSessionService 是可选的数据库当前会话查询能力；未实现的宿主仍可使用既有接口。
type CurrentSessionService interface {
	GetCurrentSession(context.Context, user.Principal) (user.CurrentSessionInfo, error)
}

func currentSessionNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

type authenticationIdentityResource struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type currentSessionResource struct {
	sessionResource
	AuthTime                 string                           `json:"auth_time"`
	AuthenticationIdentities []authenticationIdentityResource `json:"authentication_identities"`
}

func (h *Handler) currentSession(w http.ResponseWriter, r *http.Request) {
	p, ok := principal(w, r)
	if !ok {
		return
	}
	svc, ok := h.d.Users.(CurrentSessionService)
	if !ok {
		apierror.Write(w, apierror.New(apierror.StatusUnavailable, "SESSION_QUERY_NOT_CONFIGURED", "session query is not configured"))
		return
	}
	result, err := svc.GetCurrentSession(r.Context(), p)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	out := currentSessionResource{sessionResource: newSessionResource(p.UserID, result.SessionInfo), AuthTime: fmtTime(result.AuthTime), AuthenticationIdentities: make([]authenticationIdentityResource, 0)}
	out.IsCurrent = true
	for _, i := range result.AuthenticationIdentities {
		out.AuthenticationIdentities = append(out.AuthenticationIdentities, authenticationIdentityResource{Name: "users/" + p.UserID + "/identities/" + i.ID, Kind: i.Kind.String()})
	}
	sort.Slice(out.AuthenticationIdentities, func(i, j int) bool {
		return out.AuthenticationIdentities[i].Name < out.AuthenticationIdentities[j].Name
	})
	apierror.WriteJSON(w, http.StatusOK, out)
}
