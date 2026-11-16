package resourcename

import "testing"

func TestNames(t *testing.T) {
	for _, c := range []struct{ got, want string }{{User("u_x"), "users/u_x"}, {Identity("u_x", "i_x"), "users/u_x/identities/i_x"}, {Session("u_x", "s_x"), "users/u_x/sessions/s_x"}, {Profile("u_x"), "users/u_x/profile"}, {AuditEvent("u_x", "e_x"), "users/u_x/auditEvents/e_x"}} {
		if c.got != c.want {
			t.Fatalf("%q != %q", c.got, c.want)
		}
	}
}
