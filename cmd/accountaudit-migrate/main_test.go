package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestMigrationCommandPreparesAndRepeats(t *testing.T) {
	dsn := os.Getenv("SERVER_TEST_DB_DSN")
	if dsn == "" {
		t.Skip("SERVER_TEST_DB_DSN not set")
	}
	c, err := pgx.Connect(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	schema := fmt.Sprintf("akcli_%08x", rand.Uint32())
	defer c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
	if _, err := c.Exec(context.Background(), "CREATE SCHEMA "+schema+"; SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../migrations/0001_init.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(context.Background(), string(b)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Exec(context.Background(), `CREATE TABLE schema_migrations(version bigint PRIMARY KEY,dirty boolean NOT NULL);INSERT INTO schema_migrations VALUES(1,false);INSERT INTO audit_event(id,event_type,actor_kind,result) VALUES('e_0000000000001',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	args := []string{"--dsn-env", "TEST_PREP_DSN", "--schema", schema, "--confirm-schema", schema, "--source", "test/auth", "--namespace", "urn:test:accounts"}
	getenv := func(name string) string {
		if name == "TEST_PREP_DSN" {
			return dsn
		}
		return ""
	}
	for _, want := range []string{"prepared=1 reused=0", "prepared=0 reused=1"} {
		var out bytes.Buffer
		if code := run(args, getenv, &out); code != 0 {
			t.Fatalf("exit=%d output=%s", code, out.String())
		}
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output=%q want %q", out.String(), want)
		}
		if strings.Contains(out.String(), dsn) {
			t.Fatal("output leaked DSN")
		}
	}
}

func TestMigrationCommandErrorsAreSafe(t *testing.T) {
	for _, args := range [][]string{{}, {"--dsn", "postgres://secret@invalid"}, {"--unknown=secret"}, {"--dsn-env", "TEST", "--schema", "valid", "--confirm-schema", "other", "--source", "auth", "--namespace", "ns"}} {
		var out bytes.Buffer
		if code := run(args, func(string) string { return "postgres://secret@invalid" }, &out); code == 0 {
			t.Fatal("invalid command accepted")
		}
		if strings.Contains(out.String(), "secret") || strings.Contains(out.String(), "postgres://") {
			t.Fatalf("secret exposed: %s", out.String())
		}
	}
}
