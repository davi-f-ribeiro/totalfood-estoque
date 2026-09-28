#!/usr/bin/env sh
set -eu

if [ -z "${TEST_DATABASE_URL:-}" ]; then
  echo "TEST_DATABASE_URL is required" >&2
  exit 2
fi

GOTOOLCHAIN=local /opt/data/go/go/bin/go test ./...
