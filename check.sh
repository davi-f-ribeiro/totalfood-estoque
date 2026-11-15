#!/usr/bin/env sh
set -eu

if [ -z "${TEST_DATABASE_URL:-}" ]; then
  echo "TEST_DATABASE_URL is required" >&2
  exit 2
fi

# Go do PATH; GO aponta outro binário (ex.: GO=/opt/data/go/go/bin/go).
GO_BIN="${GO:-}"
if [ -z "$GO_BIN" ]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN=go
  else
    GO_BIN=/opt/data/go/go/bin/go
  fi
fi

GOTOOLCHAIN=local "$GO_BIN" vet ./...
GOTOOLCHAIN=local "$GO_BIN" test ./...
