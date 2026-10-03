package accountkit

import "github.com/bbxx111/accountkit/audit"

// DepsForTest 返回应用默认值后的 Deps（仅测试）。
func (a *Auth) DepsForTest() Deps { return a.deps }

// AuditForTest 返回装配后的审计记录器（仅测试）。
func (a *Auth) AuditForTest() audit.Recorder { return a.deps.Audit }
