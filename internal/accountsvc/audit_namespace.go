package accountsvc

import (
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/internal/auditconfig"
)

// identityAuditNamespace preserves the service's pre-defaulted configuration semantics.
func identityAuditNamespace(cfg accountkit.Config) string {
	if cfg.AuditNamespace != "" {
		return cfg.AuditNamespace
	}
	if cfg.Schema == "" {
		return "accountkit//resources"
	}
	return auditconfig.ForSchema(cfg.Schema).Namespace
}
