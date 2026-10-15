// Package auditmigration prepares an offline historical audit conversion.
// Stop writers and maintenance before preparation and upgrade.
package auditmigration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/internal/migrationlock"
	"github.com/jackc/pgx/v5"
)

type Options struct{ Schema, ConfirmSchema, Source, Namespace string }
type Report struct{ Prepared, Reused int64 }

var schemaPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
var errPreparation = errors.New("auditmigration: incompatible or changed preparation")

// Prepare uses dedicated connections copied from conn.Config(). It never changes
// or closes the caller connection, original audit rows, or migration version.
// Failures roll back the entire preparation, including staging DDL.
func Prepare(ctx context.Context, conn *pgx.Conn, o Options) (report Report, err error) {
	if conn == nil || !schemaPattern.MatchString(o.Schema) || o.ConfirmSchema != o.Schema {
		return report, errors.New("auditmigration: exact schema confirmation required")
	}
	options := authentication.Options{Source: o.Source, Namespace: o.Namespace}
	if err := options.Validate(); err != nil {
		return report, err
	}
	release, err := migrationlock.Initialization(ctx, conn.Config(), o.Schema)
	if err != nil {
		return report, err
	}
	defer release()
	cfg := conn.Config().Copy()
	cfg.RuntimeParams["search_path"] = o.Schema
	c, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return report, errors.New("auditmigration: connection failed")
	}
	defer func() {
		closing, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Close(closing)
	}()
	if err := migrationlock.Engine(ctx, c, o.Schema); err != nil {
		return report, err
	}
	tx, err := c.Begin(ctx)
	if err != nil {
		return report, errors.New("auditmigration: begin failed")
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SET LOCAL TIME ZONE 'UTC'`); err != nil {
		return report, errPreparation
	}
	var version int
	var dirty bool
	if err = tx.QueryRow(ctx, `SELECT version,dirty FROM schema_migrations`).Scan(&version, &dirty); err != nil || version != 1 || dirty {
		return report, errors.New("auditmigration: clean integrated version one required")
	}
	var compatible bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='audit_event' AND column_name='flow_id' AND udt_name='text') AND EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='identity_flow' AND column_name='proof_user_ids' AND udt_name='_text')`).Scan(&compatible); err != nil || !compatible {
		return report, errors.New("auditmigration: integrated baseline required")
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE audit_event IN SHARE MODE`); err != nil {
		return report, errPreparation
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('audit_event_upgrade_meta') IS NOT NULL`).Scan(&exists); err != nil {
		return report, errPreparation
	}
	if !exists {
		if _, err = tx.Exec(ctx, stageDDL); err != nil {
			return report, errPreparation
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_event_upgrade_meta(singleton,format,schema_name,source,namespace,config_digest) VALUES(true,1,$1,$2,$3,sha256(convert_to(jsonb_build_array(1,$1::text,$2::text,$3::text)::text,'UTF8')))`, o.Schema, o.Source, o.Namespace); err != nil {
			return report, errPreparation
		}
	} else {
		if _, err = tx.Exec(ctx, `LOCK TABLE audit_event_upgrade_meta,audit_event_upgrade_stage IN SHARE MODE`); err != nil {
			return report, errPreparation
		}
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT count(*)=1 AND bool_and(singleton AND format=1 AND schema_name=$1 AND source=$2 AND namespace=$3 AND config_digest=sha256(convert_to(jsonb_build_array(format,schema_name,source,namespace)::text,'UTF8'))) FROM audit_event_upgrade_meta`, o.Schema, o.Source, o.Namespace).Scan(&valid); err != nil || !valid {
			return report, errPreparation
		}
		if err = tx.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM audit_event a FULL JOIN audit_event_upgrade_stage s ON s.legacy_id=a.id WHERE a.id IS NULL OR s.legacy_id IS NULL OR s.original IS DISTINCT FROM to_jsonb(a) OR s.event_digest IS DISTINCT FROM sha256(convert_to(s.event::text,'UTF8')))`).Scan(&valid); err != nil || !valid {
			return report, errPreparation
		}
	}
	rows, err := tx.Query(ctx, `SELECT to_jsonb(a) FROM audit_event a ORDER BY id`)
	if err != nil {
		return report, errPreparation
	}
	var originals [][]byte
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return report, errPreparation
		}
		originals = append(originals, b)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return report, errPreparation
	}
	for _, original := range originals {
		var old legacyRow
		if err = json.Unmarshal(original, &old); err != nil {
			return Report{}, errPreparation
		}
		if !ids.Valid(ids.AuditEvent, old.ID) || !old.EventType.Valid() || !old.ActorKind.Valid() || (old.Result != enum.ResultUnspecified && !old.Result.Valid()) || (old.IdentityKind != nil && !old.IdentityKind.Valid()) {
			return Report{}, errors.New("auditmigration: unsupported historical value")
		}
		in := old.input()
		var staged []byte
		if exists {
			if err = tx.QueryRow(ctx, `SELECT id,event FROM audit_event_upgrade_stage WHERE legacy_id=$1`, old.ID).Scan(&in.ID, &staged); err != nil {
				return Report{}, errPreparation
			}
		}
		e, mapErr := authentication.MapLegacy(in, options, old.ID)
		if mapErr != nil {
			return Report{}, errors.New("auditmigration: incompatible historical event")
		}
		if exists {
			var saved event.Event
			if err = json.Unmarshal(staged, &saved); err != nil || !event.Equivalent(e, saved) {
				return Report{}, errPreparation
			}
			report.Reused++
		} else {
			encoded, marshalErr := json.Marshal(e)
			if marshalErr != nil {
				return Report{}, errPreparation
			}
			if _, err = tx.Exec(ctx, `INSERT INTO audit_event_upgrade_stage(legacy_id,id,original,event,event_digest) VALUES($1,$2,$3::jsonb,$4::jsonb,sha256(convert_to(($4::jsonb)::text,'UTF8')))`, old.ID, e.ID, original, encoded); err != nil {
				return Report{}, errPreparation
			}
			report.Prepared++
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return Report{}, errors.New("auditmigration: commit failed")
	}
	return report, nil
}

const stageDDL = `
CREATE TABLE audit_event_upgrade_meta (
 singleton boolean PRIMARY KEY CHECK(singleton), format integer NOT NULL,
 schema_name text NOT NULL, source text NOT NULL, namespace text NOT NULL, config_digest bytea NOT NULL
);
CREATE TABLE audit_event_upgrade_stage (
 legacy_id text COLLATE "C" PRIMARY KEY, id uuid NOT NULL UNIQUE,
 original jsonb NOT NULL, event jsonb NOT NULL, event_digest bytea NOT NULL
);`

type legacyRow struct {
	ID            string             `json:"id"`
	EventType     enum.EventType     `json:"event_type"`
	ActorKind     enum.ActorKind     `json:"actor_kind"`
	Result        enum.Result        `json:"result"`
	UserID        *string            `json:"user_id"`
	SessionID     *string            `json:"session_id"`
	FlowID        *string            `json:"flow_id"`
	IdentityKind  *enum.IdentityKind `json:"identity_kind"`
	SubjectHint   *string            `json:"subject_hint"`
	Reason        *string            `json:"reason"`
	IP            *string            `json:"ip"`
	DeviceID      *string            `json:"device_id"`
	RequestID     *string            `json:"request_id"`
	AdminIssuer   *string            `json:"admin_issuer"`
	AdminSubject  *string            `json:"admin_subject"`
	AdminUsername *string            `json:"admin_username"`
	OccurTime     time.Time          `json:"occur_time"`
}

func value(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
func (r legacyRow) input() authentication.Input {
	in := authentication.Input{Type: r.EventType, Actor: r.ActorKind, Result: r.Result, UserID: value(r.UserID), SessionID: value(r.SessionID), FlowID: value(r.FlowID), SubjectHint: value(r.SubjectHint), Reason: value(r.Reason), IP: value(r.IP), DeviceID: value(r.DeviceID), RequestID: value(r.RequestID), AdminIssuer: value(r.AdminIssuer), AdminSubject: value(r.AdminSubject), AdminUsername: value(r.AdminUsername), OccurTime: r.OccurTime}
	if r.IdentityKind != nil {
		in.IdentityKind = *r.IdentityKind
	}
	return in
}

// CheckConfiguration lets the root lifecycle reject mismatched historical
// provenance before Migrate. Absent staging is allowed, including fresh installs.
func CheckConfiguration(ctx context.Context, conn *pgx.Conn, o Options) error {
	if conn == nil || !schemaPattern.MatchString(o.Schema) {
		return errors.New("auditmigration: invalid schema")
	}
	var exists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, o.Schema+".audit_event_upgrade_meta").Scan(&exists); err != nil {
		return errors.New("auditmigration: inspect preparation failed")
	}
	if !exists {
		return nil
	}
	var valid bool
	query := fmt.Sprintf(`SELECT count(*)=1 AND bool_and(format=1 AND schema_name=$1 AND source=$2 AND namespace=$3) FROM %s.audit_event_upgrade_meta`, o.Schema)
	if err := conn.QueryRow(ctx, query, o.Schema, o.Source, o.Namespace).Scan(&valid); err != nil || !valid {
		return errPreparation
	}
	return nil
}
