#!/usr/bin/env bash
# Required SDK gate. Go release/race/recovery gates remain independent.
set -euo pipefail
cd "$(dirname "$0")/.."
for tool in node pnpm flutter dart go git curl; do
  command -v "$tool" >/dev/null || { echo "Required SDK dependency missing: $tool (cannot skip)" >&2; exit 1; }
done
: "${SDK_FIXTURE_DATABASE_URL:?Disposable PostgreSQL connection required; cannot skip}"
: "${SDK_FIXTURE_REDIS_ADDR:?Disposable Redis address required; cannot skip}"
export GOWORK=off
# The checked-in Flutter lock uses this hosted source. Frozen installs must agree.
export PUB_HOSTED_URL=https://pub.flutter-io.cn
export FLUTTER_STORAGE_BASE_URL=https://storage.flutter-io.cn
output="$PWD/.test-output/sdk-ci"
mkdir -p "$output"
exec > >(tee "$output/verification.log") 2>&1
node scripts/check-sdk-toolchain.mjs
node --test scripts/*sdk*.test.mjs
node scripts/check-sdk-coverage.mjs
git diff --check -- sdks scripts .github/workflows/client-sdks.yml
(
  cd sdks/accountkit-admin-ts
  pnpm install --frozen-lockfile
  pnpm format:check
  pnpm typecheck
  pnpm test
)
(
  cd sdks/accountkit-flutter
  flutter pub get --enforce-lockfile
  dart format --output=none --set-exit-if-changed lib test
  flutter analyze
  flutter test
)
go test -count=1 -tags integration ./sdks/testsupport/fixture
go build -o "$output/fixture" ./sdks/testsupport/fixture
service_pid=''
embedded_pid=''
probe_pid=''
stop_fixture() {
  local pid="$1" attempts="${2:-50}" interval="${3:-0.1}" i
  [[ -n "$pid" ]] || return 0
  kill -TERM "$pid" 2>/dev/null || true
  for ((i=0; i<attempts; i++)); do
    if ! kill -0 "$pid" 2>/dev/null; then wait "$pid" 2>/dev/null || true; return 0; fi
    sleep "$interval"
  done
  kill -KILL "$pid" 2>/dev/null || true
  for ((i=0; i<attempts; i++)); do
    if ! kill -0 "$pid" 2>/dev/null; then wait "$pid" 2>/dev/null || true; return 0; fi
    sleep "$interval"
  done
  echo "SDK fixture PID $pid did not exit after KILL" >&2
  return 1
}
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  stop_fixture "$probe_pid" || status=1
  stop_fixture "$embedded_pid" || status=1
  stop_fixture "$service_pid" || status=1
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
wait_ready() {
  local origin="$1" pid="$2" attempts="${3:-120}" probe_timeout="${4:-2}" interval="${5:-1}" attempt
  for ((attempt=0; attempt<attempts; attempt++)); do
    kill -0 "$pid" 2>/dev/null || { echo "SDK fixture exited; inspect $output" >&2; return 1; }
    # A background probe plus wait lets TERM/INT traps interrupt immediately.
    curl --fail --silent --connect-timeout 1 --max-time "$probe_timeout" "$origin/__fixture/ready" > /dev/null &
    probe_pid=$!
    if wait "$probe_pid"; then probe_pid=''; return; fi
    probe_pid=''
    sleep "$interval"
  done
  echo "SDK fixture readiness timeout: $origin" >&2
  return 1
}
read_policy_seed_marker() {
  local marker
  marker=$(node -e 'const fs = require("node:fs"); const value = JSON.parse(fs.readFileSync(process.argv[1], "utf8")).policy_seed_marker; if (value !== "SDK_POLICY_SEED_USER_ID=") process.exit(1); process.stdout.write(value);' "$1") || return
  [[ "$marker" == 'SDK_POLICY_SEED_USER_ID=' ]] || { echo 'Invalid policy seed marker' >&2; return 1; }
  printf '%s' "$marker"
}
extract_policy_seed_user_id() {
  local marker="$1" log="$2"
  sed -n "s/.*${marker}\(u_[0-9a-z]*\).*/\1/p" "$log" | tail -n 1
}
control_addr=127.0.0.1:18081
service_addr=127.0.0.1:18082
policy_addr=127.0.0.1:18084
control_origin="http://$control_addr"
service_origin="http://$service_addr"
policy_origin="http://$policy_addr"
# env -u prevents caller profile overrides from silently changing service mode.
env -u SDK_FIXTURE_SCHEMA -u SDK_FIXTURE_REDIS_PREFIX -u SDK_FIXTURE_REQUIRED_KINDS \
  SDK_FIXTURE_ADDR="$control_addr" SDK_FIXTURE_SERVICE_ADDR="$service_addr" \
  SDK_FIXTURE_PROFILE=accountsvc "$output/fixture" > "$output/accountsvc.log" 2>&1 &
service_pid=$!
wait_ready "$control_origin" "$service_pid"
start_embedded() {
  SDK_FIXTURE_ADDR="$policy_addr" SDK_FIXTURE_PROFILE=custom-extended \
    SDK_FIXTURE_SCHEMA=sdk_fixture_extended SDK_FIXTURE_REDIS_PREFIX=sdk_fixture_extended: \
    SDK_FIXTURE_REQUIRED_KINDS="$1" "$output/fixture" > "$output/embedded-$2.log" 2>&1 &
  embedded_pid=$!
  wait_ready "$policy_origin" "$embedded_pid"
}
start_embedded '[]' seed
curl --fail --silent --connect-timeout 1 --max-time 2 \
  "$policy_origin/__fixture/ready" > "$output/policy-readiness.json"
policy_seed_marker=$(read_policy_seed_marker "$output/policy-readiness.json")
export ACCOUNTKIT_ADMIN_INTEGRATION_ORIGIN="$service_origin"
export ACCOUNTKIT_ADMIN_INTEGRATION_CONTROL_ORIGIN="$control_origin"
export ACCOUNTKIT_FLUTTER_SERVICE_ORIGIN="$service_origin"
export ACCOUNTKIT_FLUTTER_CONTROL_ORIGIN="$control_origin"
export ACCOUNTKIT_FLUTTER_EMBEDDED_ORIGIN="$control_origin"
(cd sdks/accountkit-admin-ts && pnpm test:integration)
(cd sdks/accountkit-flutter && flutter test test/integration/service_contract.dart)
export ACCOUNTKIT_FLUTTER_POLICY_ORIGIN="$policy_origin"
export ACCOUNTKIT_FLUTTER_POLICY_CONTROL_ORIGIN="$policy_origin"
export ACCOUNTKIT_FLUTTER_POLICY_SUBJECT="sdk-ci-policy-$(date +%s)-$$"
(cd sdks/accountkit-flutter && flutter test --reporter expanded test/integration/policy_seed.dart) | tee "$output/policy-seed.log"
ACCOUNTKIT_FLUTTER_POLICY_USER_ID=$(extract_policy_seed_user_id "$policy_seed_marker" "$output/policy-seed.log")
export ACCOUNTKIT_FLUTTER_POLICY_USER_ID
[[ "$ACCOUNTKIT_FLUTTER_POLICY_USER_ID" =~ ^u_[0-9a-z]{13}$ ]] || { echo 'Policy seed did not return user ID' >&2; exit 1; }
stop_fixture "$embedded_pid"
embedded_pid=''
start_embedded EMAIL required-email
(cd sdks/accountkit-flutter && flutter test test/integration/policy_contract.dart)
node scripts/verify-sdk-consumers.mjs --sdk all --platform none
echo 'SDK unit, real accountsvc, embedded policy, and local fixed-commit Git consumption passed.'
echo 'Native Android/iOS and remote release-tag consumption are reported separately.'
