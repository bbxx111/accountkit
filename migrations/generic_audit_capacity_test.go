package migrations

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
)

func TestGenericAuditCapacityMatchesPublicJSON(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	for _, result := range []enum.Result{enum.ResultUnspecified, enum.ResultSuccess, enum.ResultFailure} {
		for _, tc := range []struct {
			name, zone, stamp string
			escaped           bool
		}{
			{"seconds", "UTC", "2024-01-02T03:04:05Z", false},
			{"micros", "Asia/Shanghai", "2024-01-02T03:04:05.1234Z", false},
			{"one_micro", "America/New_York", "2024-01-02T03:04:05.000001Z", true},
			{"escaped_seconds", "Asia/Shanghai", "2024-01-02T03:04:05Z", true},
		} {
			t.Run(fmt.Sprintf("%s/%s", result.String(), tc.name), func(t *testing.T) {
				if _, err := c.Exec(context.Background(), `SELECT set_config('TimeZone',$1,false)`, tc.zone); err != nil {
					t.Fatal(err)
				}
				stamp, err := time.Parse(time.RFC3339Nano, tc.stamp)
				if err != nil {
					t.Fatal(err)
				}
				id, err := uuid.NewV7()
				if err != nil {
					t.Fatal(err)
				}
				e := event.Event{ID: id, Source: "test/auth", Action: "ORDER_APPROVED", Actor: event.Actor{Type: event.ActorAnonymous}, OccurTime: stamp, Data: json.RawMessage(`{"padding":"","numbers":[1.23000,-0.0000,1e30,1e-25,9007199254740993123456789.0012300]}`)}
				var dbResult any
				if result != enum.ResultUnspecified {
					r := result
					e.Result = &r
					dbResult = int16(r)
				}
				if tc.escaped {
					escaped := "<&>\u2028\u2029\"\\\n\r\t\b\f\x01中文"
					e.Actor = event.Actor{Type: event.ActorUser, Namespace: escaped, ID: escaped, Name: escaped}
					e.Targets = []event.Resource{{Type: "ORDER", Namespace: escaped, ID: escaped, Name: escaped}}
					e.Context = event.EventContext{RequestID: escaped, UserAgent: escaped, RelatedResources: []event.Resource{{Type: "USER", Namespace: escaped, ID: escaped, Name: escaped}}}
					var data map[string]json.RawMessage
					if err := json.Unmarshal(e.Data, &data); err != nil {
						t.Fatal(err)
					}
					data[escaped], err = json.Marshal(escaped)
					if err != nil {
						t.Fatal(err)
					}
					e.Data, err = json.Marshal(data)
					if err != nil {
						t.Fatal(err)
					}
				}
				e, err = event.Freeze(e)
				if err != nil {
					t.Fatal(err)
				}
				small, err := json.Marshal(e)
				if err != nil {
					t.Fatal(err)
				}
				for _, size := range []int{65536, 65537} {
					t.Run(fmt.Sprint(size), func(t *testing.T) {
						candidate := e
						candidate.Data = json.RawMessage(strings.Replace(string(e.Data), `"padding":""`, `"padding":"`+strings.Repeat("x", size-len(small))+`"`, 1))
						wire, err := json.Marshal(candidate)
						if err != nil || len(wire) != size {
							t.Fatalf("public size=%d error=%v want=%d", len(wire), err, size)
						}
						_, validationErr := event.Freeze(candidate)
						if (validationErr == nil) != (size == 65536) {
							t.Fatalf("public validation size=%d err=%v", size, validationErr)
						}
						actor, _ := json.Marshal(candidate.Actor)
						targets, _ := json.Marshal(candidate.Targets)
						request, _ := json.Marshal(candidate.Context)
						// SQL stores both sizes; Go alone enforces the public JSON limit.
						// Equivalent numeric spellings remain valid JSONB input.
						data := strings.Replace(string(candidate.Data), "1.23,0,", "1.23000,-0.00000,", 1)
						_, err = c.Exec(context.Background(), `INSERT INTO audit_event(id,source,action,result,actor,targets,context,data,occur_time) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, candidate.ID, candidate.Source, candidate.Action, dbResult, actor, targets, request, data, candidate.OccurTime)
						if err != nil {
							t.Fatalf("SQL rejected basic JSON structure at %d bytes: %v", size, err)
						}
						if _, err := c.Exec(context.Background(), `DELETE FROM audit_event WHERE id=$1`, candidate.ID); err != nil {
							t.Fatal(err)
						}
					})
				}
			})
		}
	}
}

func TestGenericAuditResultStorageAllowsFutureEnumValues(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	if _, err := c.Exec(context.Background(), `INSERT INTO audit_event(id,source,action,result,actor,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','ORDER_APPROVED',3,'{"type":"ANONYMOUS"}','2024-01-01')`); err != nil {
		t.Fatalf("SQL enum evolution blocked: %v", err)
	}
}

func TestGenericAuditDatabaseTimeDomain(t *testing.T) {
	cfg, schema := safetyDB(t)
	if err := Up(context.Background(), cfg, schema); err != nil {
		t.Fatal(err)
	}
	c := migrationTestConn(t, cfg, schema)
	// PostgreSQL's time type accepts values outside the Go business time domain.
	for _, stamp := range []string{
		"infinity", "-infinity", "10000-01-01T00:00:00Z", "0001-01-01 00:00:00 BC",
		"0001-01-01T00:00:00Z", "0001-01-01T08:00:00+08:00",
		"0001-01-01T00:00:00.000001Z", "9999-12-31T23:59:59.999999Z",
	} {
		t.Run(stamp, func(t *testing.T) {
			_, err := c.Exec(context.Background(), `INSERT INTO audit_event(id,source,action,actor,occur_time) VALUES('019a0000-0000-7000-8000-000000000001','test/auth','ORDER_APPROVED','{"type":"ANONYMOUS"}',$1::timestamptz)`, stamp)
			if err != nil {
				t.Errorf("valid PostgreSQL timestamp rejected: %v", err)
			}
			if _, err := c.Exec(context.Background(), `DELETE FROM audit_event`); err != nil {
				t.Fatal(err)
			}
		})
	}
}
