package migrations

import (
	"context"
	"testing"
)

func TestPurgedUserInitialDefinition(t *testing.T) {
	cfg, schema, c := consolidatedProfileAttributesInitial(t)
	ctx := context.Background()
	assertVersion(t, cfg, schema, 1, false)
	var identity, generated string
	if err := c.QueryRow(ctx, `SELECT is_identity, identity_generation FROM information_schema.columns WHERE table_schema=$1 AND table_name='purged_user' AND column_name='seq'`, schema).Scan(&identity, &generated); err != nil {
		t.Fatal(err)
	}
	if identity != "YES" || generated != "ALWAYS" {
		t.Fatalf("seq identity = %s %s", identity, generated)
	}
	if _, err := c.Exec(ctx, `INSERT INTO purged_user(user_id) VALUES('u_0000000000001'),('u_0000000000002')`); err != nil {
		t.Fatal(err)
	}
	var first, second int64
	if err := c.QueryRow(ctx, `SELECT min(seq), max(seq) FROM purged_user`).Scan(&first, &second); err != nil || second <= first {
		t.Fatalf("seq not increasing: %d %d %v", first, second, err)
	}
	for name, q := range map[string]string{
		"duplicate user":  `INSERT INTO purged_user(user_id) VALUES('u_0000000000001')`,
		"bad user id":     `INSERT INTO purged_user(user_id) VALUES('users/1')`,
		"explicit seq":    `INSERT INTO purged_user(user_id, seq) VALUES('u_0000000000003', 1)`,
		"null purge time": `INSERT INTO purged_user(user_id, purge_time) VALUES('u_0000000000004', NULL)`,
	} {
		if _, err := c.Exec(ctx, q); err == nil {
			t.Errorf("%s: expected constraint violation", name)
		}
	}
	// 重复 Up 不改变已安装结构。
	catalog := migrationCatalog(t, c, schema)
	if err := Up(ctx, cfg, schema); err != nil || catalog != migrationCatalog(t, c, schema) {
		t.Fatalf("repeat Up: %v", err)
	}
}
