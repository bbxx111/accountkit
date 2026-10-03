#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
before="$(find user/db audit/db -type f -name '*.go' -print0 | sort -z | xargs -0 sha256sum)"
sqlc generate
after="$(find user/db audit/db -type f -name '*.go' -print0 | sort -z | xargs -0 sha256sum)"
if [[ "$before" != "$after" ]]; then
  echo "sqlc generated output was stale; regenerate and review the changes" >&2
  exit 1
fi