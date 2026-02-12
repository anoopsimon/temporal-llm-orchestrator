#!/usr/bin/env bash
set -euo pipefail

if [[ -z "${POSTGRES_DSN:-}" ]]; then
  echo "POSTGRES_DSN is required"
  exit 1
fi

psql "$POSTGRES_DSN" -f db/migrations/001_init.sql
