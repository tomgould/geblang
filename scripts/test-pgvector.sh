#!/usr/bin/env bash
# Spin up a pgvector Postgres container, point GEBLANG_PG_DSN at it, and run the
# Geblang test suite so the pgvector integration tests (skipped by default when
# no database is configured) actually run. The container is removed on exit.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${PGVECTOR_IMAGE:-pgvector/pgvector:pg16}"
NAME="${PGVECTOR_CONTAINER:-geblang-pgvector-test}"
PORT="${PGVECTOR_PORT:-55432}"
TARGET="${1:-tests/}"
BIN="${GEBLANG_BIN:-$ROOT/geblang}"

cleanup() { docker rm -f "$NAME" >/dev/null 2>&1 || true; }
trap cleanup EXIT
cleanup

echo "Starting $IMAGE as $NAME on localhost:$PORT ..."
docker run -d --name "$NAME" \
  -e POSTGRES_PASSWORD=postgres \
  -e POSTGRES_DB=geblang \
  -p "$PORT:5432" "$IMAGE" >/dev/null

printf "Waiting for Postgres to accept connections"
for i in $(seq 1 60); do
  if docker exec "$NAME" pg_isready -U postgres -d geblang >/dev/null 2>&1; then
    echo " ok"
    break
  fi
  printf "."
  sleep 1
  if [ "$i" -eq 60 ]; then
    echo " timed out"
    exit 1
  fi
done

export GEBLANG_PG_DSN="postgres://postgres:postgres@localhost:$PORT/geblang?sslmode=disable"
echo "GEBLANG_PG_DSN=$GEBLANG_PG_DSN"

# FFI allow-flags match `make test-lang` so the FFI tests run too, leaving
# nothing skipped by default in this run.
"$BIN" test \
  --allow-ffi 'libm.so.*' --allow-ffi 'libc.so.*' --allow-ffi 'libsqlite3*' \
  "$TARGET"
