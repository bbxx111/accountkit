package accountkit_test

import (
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/authentication"
)

type testAuditMemory struct{ audit.Memory }

func (m *testAuditMemory) Events() []audit.AuthenticationEvent {
	raw := m.Memory.Events()
	out := make([]audit.AuthenticationEvent, len(raw))
	for i, e := range raw {
		var err error
		out[i], err = authentication.Projection(e)
		if err != nil {
			panic(err)
		}
	}
	return out
}
func (m *testAuditMemory) RawEvents() []audit.Event { return m.Memory.Events() }
