package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

// Object names determine catalog row order, so compare every definition after
// sorting the renamed rows again. No columns, constraints or indexes are omitted.
func sortedRenameCatalog(t *testing.T, catalog string) string {
	t.Helper()
	var groups map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(catalog), &groups); err != nil {
		t.Fatal(err)
	}
	for _, rows := range groups {
		sort.Slice(rows, func(i, j int) bool { return string(rows[i]) < string(rows[j]) })
	}
	b, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func seedRenameFlow(t *testing.T, c *pgx.Conn, flowTable string) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO user_account(id,state,display_name) VALUES('u_0000000000001',1,'preserved')`,
		`INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001',3,'synthetic-provider')`,
		`INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) VALUES('s_0000000000001','u_0000000000001','device',now(),decode('1234','hex'),now()+interval '1 day')`,
		`INSERT INTO ` + flowTable + `(id,token_digest,state,policy_fingerprint,payload,cipher_version,revision,proof_user_ids,user_id,session_id,create_time,update_time,expire_time) VALUES('f_0000000000001','test-token-digest',3,'policy',decode('001234ff','hex'),1,7,ARRAY['u_0000000000001'],'u_0000000000001','s_0000000000001',now(),now(),now()+interval '5 minutes')`,
		`INSERT INTO audit_event(id,source,action,result,actor,targets,data,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','SIGN_IN',1,'{"type":"ANONYMOUS"}','[{"type":"IDENTITY_FLOW","namespace":"urn:test:accounts","id":"f_0000000000001"}]','{"accountkit":{"flow_id":"f_0000000000001"}}','2024-03-04T05:06:07.123456Z')`,
	} {
		if _, err := c.Exec(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func renameDataSnapshot(t *testing.T, c *pgx.Conn, flowTable string) string {
	t.Helper()
	var result string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(t ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(t ORDER BY id) FROM session t),(SELECT jsonb_agg(t ORDER BY id) FROM `+flowTable+` t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t))::text`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func historicalFlowInitial(t *testing.T, cfg *pgx.ConnConfig, schema string) *pgx.Conn {
	t.Helper()
	// The exact pre-rename definition remains immutable in Git history.
	initial, err := exec.Command("git", "show", "732639ccddd379552edb58c8b5b128ebb7bbf2a6:migrations/0001_init.up.sql").Output()
	if err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	for _, statement := range []string{"CREATE SCHEMA " + schema, string(initial), "CREATE TABLE schema_migrations(version bigint PRIMARY KEY, dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES(1,false)"} {
		if _, err := c.Exec(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestSignInCompletionFlowInitialDefinition(t *testing.T) {
	cfg, oldSchema := safetyDB(t)
	_, schema := safetyDB(t)
	old := historicalFlowInitial(t, cfg, oldSchema)
	if _, err := old.Exec(context.Background(), sessionContextHistoricalSQL(t, "0002_session_authentication_context.up.sql")); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(context.Background(), databaseReviewIndexSQL); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(context.Background(), profileAttributesInitialDDL); err != nil {
		t.Fatal(err)
	}
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	assertVersion(t, cfg, schema, 1, false)
	var newExists, oldExists bool
	if err := c.QueryRow(context.Background(), `SELECT to_regclass('sign_in_completion_flow') IS NOT NULL, to_regclass('identity_flow') IS NOT NULL`).Scan(&newExists, &oldExists); err != nil || !newExists || oldExists {
		t.Fatalf("initial definition has new=%v old=%v err=%v", newExists, oldExists, err)
	}
	want := strings.ReplaceAll(sessionContextUnchangedCatalog(t, migrationCatalog(t, old, oldSchema)), "identity_flow", "sign_in_completion_flow")
	if got := sessionContextUnchangedCatalog(t, migrationCatalog(t, c, schema)); sortedRenameCatalog(t, got) != sortedRenameCatalog(t, want) {
		t.Fatalf("initial definition changed more than authorized names and session simplification\nwant: %s\ngot: %s", want, got)
	}
}

func TestSignInCompletionFlowNonemptyRepeatAndIsolation(t *testing.T) {
	cfg, schema := safetyDB(t)
	_, otherSchema := safetyDB(t)
	initialUp(t, cfg, schema)
	initialUp(t, cfg, otherSchema)
	c := migrationTestConn(t, cfg, schema)
	other := migrationTestConn(t, cfg, otherSchema)
	seedRenameFlow(t, c, "sign_in_completion_flow")
	seedRenameFlow(t, other, "sign_in_completion_flow")
	before, otherBefore := renameDataSnapshot(t, c, "sign_in_completion_flow"), renameDataSnapshot(t, other, "sign_in_completion_flow")
	catalog, otherCatalog := migrationCatalog(t, c, schema), migrationCatalog(t, other, otherSchema)
	var oldOID uint32
	if err := c.QueryRow(context.Background(), `SELECT 'sign_in_completion_flow'::regclass::oid`).Scan(&oldOID); err != nil {
		t.Fatal(err)
	}
	initialUp(t, cfg, schema)
	assertVersion(t, cfg, schema, 1, false)
	if got := renameDataSnapshot(t, c, "sign_in_completion_flow"); got != before {
		t.Fatal("repeat Up changed records or encrypted bytes")
	}
	renamedCatalog := migrationCatalog(t, c, schema)
	if renamedCatalog != catalog {
		t.Fatal("repeat Up changed catalog")
	}
	var newOID uint32
	var oldExists bool
	if err := c.QueryRow(context.Background(), `SELECT 'sign_in_completion_flow'::regclass::oid, to_regclass('identity_flow') IS NOT NULL`).Scan(&newOID, &oldExists); err != nil || newOID != oldOID || oldExists {
		t.Fatalf("repeat Up recreated table: %d -> %d old=%v err=%v", oldOID, newOID, oldExists, err)
	}
	initialUp(t, cfg, schema)
	if renameDataSnapshot(t, c, "sign_in_completion_flow") != before || migrationCatalog(t, c, schema) != renamedCatalog {
		t.Fatal("repeat Up changed schema or records")
	}
	assertVersion(t, cfg, schema, 1, false)
	assertVersion(t, cfg, otherSchema, 1, false)
	if renameDataSnapshot(t, other, "sign_in_completion_flow") != otherBefore || migrationCatalog(t, other, otherSchema) != otherCatalog {
		t.Fatal("repeat Up changed another instance")
	}
}

func TestSignInCompletionFlowBaselineMismatch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		version  int
		mutation string
	}{
		{"v1_old_name", 1, `SELECT 1`},
		{"v2_old_name", 2, `UPDATE schema_migrations SET version=2`},
		{"v2_new_name", 2, `ALTER TABLE identity_flow RENAME TO sign_in_completion_flow; UPDATE schema_migrations SET version=2`},
		{"v1_both_names", 1, `CREATE TABLE sign_in_completion_flow (LIKE identity_flow INCLUDING ALL)`},
		{"v2_both_names", 2, `CREATE TABLE sign_in_completion_flow (LIKE identity_flow INCLUDING ALL); UPDATE schema_migrations SET version=2`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, schema := safetyDB(t)
			c := historicalFlowInitial(t, cfg, schema)
			seedRenameFlow(t, c, "identity_flow")
			if _, err := c.Exec(context.Background(), tc.mutation); err != nil {
				t.Fatal(err)
			}
			table := "identity_flow"
			if tc.name == "v2_new_name" {
				table = "sign_in_completion_flow"
			}
			before, catalog := renameDataSnapshot(t, c, table), migrationCatalog(t, c, schema)
			if err := Up(context.Background(), cfg, schema); !errors.Is(err, ErrIncompatibleBaseline) {
				t.Fatalf("mismatched baseline accepted: %v", err)
			}
			assertVersion(t, cfg, schema, tc.version, false)
			if renameDataSnapshot(t, c, table) != before || migrationCatalog(t, c, schema) != catalog {
				t.Fatal("rejected migration changed data or schema")
			}
			if _, err := c.Exec(context.Background(), "UPDATE schema_migrations SET dirty=true"); err != nil {
				t.Fatal(err)
			}
			var dirty migrate.ErrDirty
			if err := Up(context.Background(), cfg, schema); !errors.As(err, &dirty) || dirty.Version != tc.version {
				t.Fatalf("dirty precedence lost: %v", err)
			}
			assertVersion(t, cfg, schema, tc.version, true)
			if renameDataSnapshot(t, c, table) != before || migrationCatalog(t, c, schema) != catalog {
				t.Fatal("dirty rejection changed data or schema")
			}
		})
	}
}
