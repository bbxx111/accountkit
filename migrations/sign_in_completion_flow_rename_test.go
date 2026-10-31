package migrations

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func seedInitialFlow(t *testing.T, c *pgx.Conn) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO user_account(id,state,display_name) VALUES('u_0000000000001',1,'preserved')`,
		`INSERT INTO identity(id,user_id,kind,provider_subject) VALUES('i_0000000000001','u_0000000000001','WECHAT','synthetic-provider')`,
		`INSERT INTO session(id,user_id,device_id,auth_time,refresh_token_hash,refresh_expire_time) VALUES('s_0000000000001','u_0000000000001','device',now(),decode('1234','hex'),now()+interval '1 day')`,
		`INSERT INTO sign_in_completion_flow(id,token_digest,state,policy_fingerprint,payload,cipher_version,revision,proof_user_ids,user_id,session_id,create_time,update_time,expire_time) VALUES('f_0000000000001','test-token-digest',3,'policy',decode('001234ff','hex'),1,7,ARRAY['u_0000000000001'],'u_0000000000001','s_0000000000001',now(),now(),now()+interval '5 minutes')`,
		`INSERT INTO audit_event(id,source,action,result,actor,targets,data,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','SIGN_IN',1,'{"type":"ANONYMOUS"}','[{"type":"IDENTITY_FLOW","namespace":"urn:test:accounts","id":"f_0000000000001"}]','{"accountkit":{"flow_id":"f_0000000000001"}}','2024-03-04T05:06:07.123456Z')`,
	} {
		if _, err := c.Exec(context.Background(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func migrationDataSnapshot(t *testing.T, c *pgx.Conn) string {
	t.Helper()
	var result string
	if err := c.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT jsonb_agg(t ORDER BY id) FROM user_account t),(SELECT jsonb_agg(t ORDER BY id) FROM identity t),(SELECT jsonb_agg(t ORDER BY id) FROM session t),(SELECT jsonb_agg(t ORDER BY id) FROM sign_in_completion_flow t),(SELECT jsonb_agg(t ORDER BY id) FROM audit_event t))::text`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSignInCompletionFlowInitialDefinition(t *testing.T) {
	cfg, schema := safetyDB(t)
	initialUp(t, cfg, schema)
	c := migrationTestConn(t, cfg, schema)
	assertVersion(t, cfg, schema, 1, false)
	var newExists, oldExists bool
	if err := c.QueryRow(context.Background(), `SELECT to_regclass('sign_in_completion_flow') IS NOT NULL, to_regclass('identity_flow') IS NOT NULL`).Scan(&newExists, &oldExists); err != nil || !newExists || oldExists {
		t.Fatalf("initial definition has new=%v old=%v err=%v", newExists, oldExists, err)
	}
}

func TestSignInCompletionFlowNonemptyRepeatAndIsolation(t *testing.T) {
	cfg, schema := safetyDB(t)
	_, otherSchema := safetyDB(t)
	initialUp(t, cfg, schema)
	initialUp(t, cfg, otherSchema)
	c := migrationTestConn(t, cfg, schema)
	other := migrationTestConn(t, cfg, otherSchema)
	seedInitialFlow(t, c)
	seedInitialFlow(t, other)
	before, otherBefore := migrationDataSnapshot(t, c), migrationDataSnapshot(t, other)
	catalog, otherCatalog := migrationCatalog(t, c, schema), migrationCatalog(t, other, otherSchema)
	var oldOID uint32
	if err := c.QueryRow(context.Background(), `SELECT 'sign_in_completion_flow'::regclass::oid`).Scan(&oldOID); err != nil {
		t.Fatal(err)
	}
	initialUp(t, cfg, schema)
	assertVersion(t, cfg, schema, 1, false)
	if got := migrationDataSnapshot(t, c); got != before {
		t.Fatal("repeat Up changed records or encrypted bytes")
	}
	repeatedCatalog := migrationCatalog(t, c, schema)
	if repeatedCatalog != catalog {
		t.Fatal("repeat Up changed catalog")
	}
	var newOID uint32
	var oldExists bool
	if err := c.QueryRow(context.Background(), `SELECT 'sign_in_completion_flow'::regclass::oid, to_regclass('identity_flow') IS NOT NULL`).Scan(&newOID, &oldExists); err != nil || newOID != oldOID || oldExists {
		t.Fatalf("repeat Up recreated table: %d -> %d old=%v err=%v", oldOID, newOID, oldExists, err)
	}
	initialUp(t, cfg, schema)
	if migrationDataSnapshot(t, c) != before || migrationCatalog(t, c, schema) != repeatedCatalog {
		t.Fatal("repeat Up changed schema or records")
	}
	assertVersion(t, cfg, schema, 1, false)
	assertVersion(t, cfg, otherSchema, 1, false)
	if migrationDataSnapshot(t, other) != otherBefore || migrationCatalog(t, other, otherSchema) != otherCatalog {
		t.Fatal("repeat Up changed another instance")
	}
}
