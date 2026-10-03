#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${SERVER_TEST_DB_DSN:?Set SERVER_TEST_DB_DSN to a disposable PostgreSQL database}"
: "${ACCOUNTSVC_TEST_REDIS_URL:?Set ACCOUNTSVC_TEST_REDIS_URL to an isolated test Redis instance}"
export GOWORK=off
export CGO_ENABLED=1
export ACCOUNTSVC_TEST_RACE=1
if [[ "$(go env GOOS)" != linux ]]; then
  echo 'accountsvc process verification requires Linux (actual SIGTERM and TLS trust fixtures)' >&2
  exit 1
fi
mkdir -p .test-output
go build -o .test-output/accountsvc ./cmd/accountsvc
go vet ./internal/accountsvc/... ./examples/remoteauth ./user/sender/smtp
go test -race -json -count=1 -timeout=5m ./internal/accountsvc/... ./examples/remoteauth ./user/sender/smtp | tee .test-output/accountsvc-tests.jsonl
go run ./internal/testgate scripts/accountsvc-required-tests.txt < .test-output/accountsvc-tests.jsonl
