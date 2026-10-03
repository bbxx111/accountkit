#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${ACCOUNTKIT_RECOVERY_SOURCE_DSN:?Set a disposable empty source database}"
: "${ACCOUNTKIT_RECOVERY_TARGET_DSN:?Set a different disposable empty restore database}"
export GOWORK=off PGCONNECT_TIMEOUT=10
for tool in psql pg_dump pg_restore go; do command -v "$tool" >/dev/null || { echo "Missing recovery tool: $tool" >&2; exit 1; }; done
identity_sql="SELECT system_identifier::text || '/' || current_database() FROM pg_control_system()"
source_id="$(psql -X -A -t -v ON_ERROR_STOP=1 "$ACCOUNTKIT_RECOVERY_SOURCE_DSN" -c "$identity_sql")"
target_id="$(psql -X -A -t -v ON_ERROR_STOP=1 "$ACCOUNTKIT_RECOVERY_TARGET_DSN" -c "$identity_sql")"
if [[ -z "$source_id" || -z "$target_id" || "$source_id" == "$target_id" ]]; then
  echo "Recovery source and target must be different actual databases" >&2; exit 1
fi
# Include tables, sequences, views, types and routines by requiring no user-schema relations
# and no additional user schemas. Only the default public schema is allowed initially.
empty_sql="SELECT (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE left(n.nspname,3) <> 'pg_' AND n.nspname <> 'information_schema') + (SELECT count(*) FROM pg_namespace WHERE left(nspname,3) <> 'pg_' AND nspname NOT IN ('information_schema','public')) + (SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public') + (SELECT count(*) FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='public')"
for role in source target; do
  if [[ "$role" == source ]]; then dsn="$ACCOUNTKIT_RECOVERY_SOURCE_DSN"; else dsn="$ACCOUNTKIT_RECOVERY_TARGET_DSN"; fi
  if [[ "$(psql -X -A -t -v ON_ERROR_STOP=1 "$dsn" -c "$empty_sql")" != 0 ]]; then
    echo "Recovery $role must be an empty disposable database" >&2; exit 1
  fi
done
mkdir -p .test-output
work="$(mktemp -d .test-output/recovery.XXXXXX)"
printf '%s\n' github.com/bbxx111/accountkit/TestRecoveryFixture > "$work/required.txt"
export ACCOUNTKIT_RECOVERY_STATE="$PWD/$work/state.json"
phase() {
  local mode="$1" dsn="$2"
  ACCOUNTKIT_RECOVERY_MODE="$mode" ACCOUNTKIT_RECOVERY_DSN="$dsn" go test -json -count=1 -run '^TestRecoveryFixture$' . > "$work/$mode.jsonl"
  go run ./internal/testgate "$work/required.txt" < "$work/$mode.jsonl"
}
phase seed "$ACCOUNTKIT_RECOVERY_SOURCE_DSN"
pg_dump --format=custom --no-owner --no-privileges --dbname="$ACCOUNTKIT_RECOVERY_SOURCE_DSN" --file="$work/database.dump"
pg_restore --exit-on-error --no-owner --no-privileges --dbname="$ACCOUNTKIT_RECOVERY_TARGET_DSN" "$work/database.dump"
phase verify "$ACCOUNTKIT_RECOVERY_TARGET_DSN"
phase source-unchanged "$ACCOUNTKIT_RECOVERY_SOURCE_DSN"
pg_dump --version
pg_restore --version
echo "Recovery drill passed: version, four-table snapshot, decryption, access/refresh and source preservation"
echo "Synthetic fixture evidence retained at $work"