package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/jackc/pgx/v5"
)

// Exact pre-consolidation sources stay in Git; never rewrite a historical fixture.
const sessionContextBeforeConsolidation = "b34810a567c2dd2b2da6bef9f7b226b4eed596a2"

func sessionContextHistoricalSQL(t *testing.T, name string) string {
	t.Helper()
	b, err := exec.Command("git", "show", sessionContextBeforeConsolidation+":migrations/"+name).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Only these two locations were authorized to change in this simplification.
// Preserve every other catalog row and its column ordinal, constraint and index
// definition, including all other session columns and all authentication links.
func sessionContextUnchangedCatalog(t *testing.T, catalog string) string {
	t.Helper()
	var groups map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(catalog), &groups); err != nil {
		t.Fatal(err)
	}
	for group, rows := range groups {
		kept := make([]json.RawMessage, 0, len(rows))
		for _, row := range rows {
			var object struct {
				TableName  string `json:"table_name"`
				IndexTable string `json:"tablename"`
				ColumnName string `json:"column_name"`
			}
			if err := json.Unmarshal(row, &object); err != nil {
				t.Fatal(err)
			}
			if object.TableName == "session_client_info" || object.IndexTable == "session_client_info" ||
				(group == "columns" && object.TableName == "session" && object.ColumnName == "authentication_identities_known") {
				continue
			}
			kept = append(kept, row)
		}
		groups[group] = kept
	}
	b, err := json.Marshal(groups)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSessionContextConsolidatedCatalog(t *testing.T) {
	cfg, oldSchema := safetyDB(t)
	_, schema := safetyDB(t)
	ctx := context.Background()
	old := migrationTestConn(t, cfg, oldSchema)
	for _, statement := range []string{
		"CREATE SCHEMA " + oldSchema,
		sessionContextHistoricalSQL(t, "0001_init.up.sql"),
		sessionContextHistoricalSQL(t, "0002_session_authentication_context.up.sql"),
		databaseReviewIndexSQL,
	} {
		if _, err := old.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	initialUp(t, cfg, schema)
	current := migrationTestConn(t, cfg, schema)
	assertVersion(t, cfg, schema, 1, false)
	// TestSimplifiedSessionClientStorage checks the new table itself. This
	// comparison ensures the authorized rewrite did not alter anything else.
	if want, got := sessionContextUnchangedCatalog(t, migrationCatalog(t, old, oldSchema)), sessionContextUnchangedCatalog(t, migrationCatalog(t, current, schema)); want != got {
		t.Fatalf("simplification changed catalog beyond authorized fields, including column order\nwant: %s\ngot: %s", want, got)
	}
}

func TestSessionContextRetiredBaselinesRejected(t *testing.T) {
	for _, version := range []int{1, 2} {
		for _, populated := range []bool{false, true} {
			name := map[int]string{1: "published_v030", 2: "withdrawn_session_v2"}[version]
			if populated {
				name += "/populated"
			} else {
				name += "/empty"
			}
			t.Run(name, func(t *testing.T) {
				cfg, schema := safetyDB(t)
				ctx := context.Background()
				c := migrationTestConn(t, cfg, schema)
				statements := []string{"CREATE SCHEMA " + schema, sessionContextHistoricalSQL(t, "0001_init.up.sql")}
				if version == 2 {
					statements = append(statements, sessionContextHistoricalSQL(t, "0002_session_authentication_context.up.sql"))
				}
				for _, statement := range statements {
					if _, err := c.Exec(ctx, statement); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := c.Exec(ctx, "CREATE TABLE schema_migrations(version bigint PRIMARY KEY, dirty boolean NOT NULL); INSERT INTO schema_migrations VALUES($1,false)", pgx.QueryExecModeSimpleProtocol, version); err != nil {
					t.Fatal(err)
				}
				if populated {
					seedRenameFlow(t, c, "sign_in_completion_flow")
					if version == 2 {
						if _, err := c.Exec(ctx, `INSERT INTO session_authentication_identity VALUES('s_0000000000001','i_0000000000001'); INSERT INTO session_client_info(session_id,snapshot_kind,app_name,observe_time) VALUES('s_0000000000001',1,'preserved',now())`); err != nil {
							t.Fatal(err)
						}
					}
				}
				snapshot := func() string {
					s := renameDataSnapshot(t, c, "sign_in_completion_flow")
					if version == 2 {
						var extra string
						if err := c.QueryRow(ctx, `SELECT jsonb_build_array((SELECT jsonb_agg(t ORDER BY session_id,identity_id) FROM session_authentication_identity t),(SELECT jsonb_agg(t ORDER BY session_id,snapshot_kind) FROM session_client_info t))::text`).Scan(&extra); err != nil {
							t.Fatal(err)
						}
						s += extra
					}
					return s
				}
				before, catalog := snapshot(), migrationCatalog(t, c, schema)
				for range 2 {
					retry, cancel := context.WithTimeout(ctx, time.Second)
					err := Up(retry, cfg, schema)
					cancel()
					if !errors.Is(err, ErrIncompatibleBaseline) {
						t.Fatalf("retired version %d accepted or locks leaked: %v", version, err)
					}
					assertVersion(t, cfg, schema, version, false)
					if snapshot() != before || migrationCatalog(t, c, schema) != catalog {
						t.Fatal("rejection changed data or catalog")
					}
				}
				if _, err := c.Exec(ctx, "UPDATE schema_migrations SET dirty=true"); err != nil {
					t.Fatal(err)
				}
				var dirty migrate.ErrDirty
				if err := Up(ctx, cfg, schema); !errors.As(err, &dirty) || dirty.Version != version {
					t.Fatalf("dirty precedence lost: %v", err)
				}
				assertVersion(t, cfg, schema, version, true)
				if snapshot() != before || migrationCatalog(t, c, schema) != catalog {
					t.Fatal("dirty rejection changed data or catalog")
				}
			})
		}
	}
}
