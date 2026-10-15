package auditconfig

import (
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
)

// Local derives local-only identifiers without connecting to the database.
func Local(pool *pgxpool.Pool) authentication.Options {
	schema := "account"
	if pool != nil {
		if first := strings.Trim(strings.TrimSpace(strings.Split(pool.Config().ConnConfig.RuntimeParams["search_path"], ",")[0]), "\""); first != "" {
			schema = first
		}
	}
	return authentication.Options{Source: "accountkit/" + schema, Namespace: "accountkit/" + schema + "/resources"}
}
