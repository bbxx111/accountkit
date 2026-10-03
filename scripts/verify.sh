#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
: "${SERVER_TEST_DB_DSN:?Set SERVER_TEST_DB_DSN to a disposable PostgreSQL database}"
: "${ACCOUNTKIT_RECOVERY_SOURCE_DSN:?Set a disposable empty recovery source database}"
: "${ACCOUNTKIT_RECOVERY_TARGET_DSN:?Set a different empty recovery target database}"
export GOWORK=off
export CGO_ENABLED=1
mkdir -p .test-output
bash scripts/check-migrations.sh
go build ./...
go vet ./...
go test -race -json -count=1 ./... | tee .test-output/tests.jsonl
go run ./internal/testgate scripts/required-tests.txt < .test-output/tests.jsonl
bash scripts/verify-recovery.sh