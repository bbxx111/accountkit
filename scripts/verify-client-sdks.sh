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
git diff --check -- sdk scripts .github/workflows/client-sdks.yml
(
  cd sdk/accountkit-admin-ts
  pnpm install --frozen-lockfile
  pnpm format:check
  pnpm typecheck
  pnpm test
)
(
  cd sdk/accountkit-flutter
  flutter pub get --enforce-lockfile
  dart format --output=none --set-exit-if-changed lib test
  flutter analyze
  flutter test
)
go test -count=1 -tags integration ./sdk/testsupport/fixture
go build -o "$output/fixture" ./sdk/testsupport/fixture
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
# env -u prevents caller profile overrides from silently changing service mode.
env -u SDK_FIXTURE_SCHEMA -u SDK_FIXTURE_REDIS_PREFIX -u SDK_FIXTURE_REQUIRED_KINDS \
  SDK_FIXTURE_ADDR=127.0.0.1:18081 SDK_FIXTURE_SERVICE_ADDR=127.0.0.1:18082 \
  SDK_FIXTURE_PROFILE=accountsvc "$output/fixture" > "$output/accountsvc.log" 2>&1 &
service_pid=$!
wait_ready http://127.0.0.1:18081 "$service_pid"
start_embedded() {
  SDK_FIXTURE_ADDR=127.0.0.1:18084 SDK_FIXTURE_PROFILE=custom-extended \
    SDK_FIXTURE_SCHEMA=sdk_fixture_extended SDK_FIXTURE_REDIS_PREFIX=sdk_fixture_extended: \
    SDK_FIXTURE_REQUIRED_KINDS="$1" "$output/fixture" > "$output/embedded-$2.log" 2>&1 &
  embedded_pid=$!
  wait_ready http://127.0.0.1:18084 "$embedded_pid"
}
start_embedded '[]' seed
export ACCOUNTKIT_ADMIN_INTEGRATION_ORIGIN=http://127.0.0.1:18082
export ACCOUNTKIT_ADMIN_INTEGRATION_CONTROL_ORIGIN=http://127.0.0.1:18081
export ACCOUNTKIT_FLUTTER_SERVICE_ORIGIN=http://127.0.0.1:18082
export ACCOUNTKIT_FLUTTER_CONTROL_ORIGIN=http://127.0.0.1:18081
export ACCOUNTKIT_FLUTTER_EMBEDDED_ORIGIN=http://127.0.0.1:18081
(cd sdk/accountkit-admin-ts && pnpm test:integration)
(cd sdk/accountkit-flutter && flutter test test/integration/service_contract.dart)
export ACCOUNTKIT_FLUTTER_POLICY_ORIGIN=http://127.0.0.1:18084
export ACCOUNTKIT_FLUTTER_POLICY_CONTROL_ORIGIN=http://127.0.0.1:18084
export ACCOUNTKIT_FLUTTER_POLICY_SUBJECT="sdk-ci-policy-$(date +%s)-$$"
(cd sdk/accountkit-flutter && flutter test --reporter expanded test/integration/policy_seed.dart) | tee "$output/policy-seed.log"
ACCOUNTKIT_FLUTTER_POLICY_USER_ID=$(sed -n 's/.*SDK_POLICY_SEED_USER_ID=\(u_[0-9a-z]*\).*/\1/p' "$output/policy-seed.log" | tail -n 1)
export ACCOUNTKIT_FLUTTER_POLICY_USER_ID
[[ "$ACCOUNTKIT_FLUTTER_POLICY_USER_ID" =~ ^u_[0-9a-z]{13}$ ]] || { echo 'Policy seed did not return user ID' >&2; exit 1; }
stop_fixture "$embedded_pid"
embedded_pid=''
start_embedded EMAIL required-email
(cd sdk/accountkit-flutter && flutter test test/integration/policy_contract.dart)
node scripts/verify-sdk-consumers.mjs --sdk all --platform none
echo 'SDK unit, real accountsvc, embedded policy, and local fixed-commit Git consumption passed.'
echo 'Native Android/iOS and remote release-tag consumption are reported separately.'
