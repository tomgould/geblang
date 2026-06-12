#!/usr/bin/env bash
# benchmarks/db/run.sh -- database connectivity benchmarks.
#
# Seeds a 1M-row SQLite database, then measures streaming vs eager
# 1M-row scans (time + max RSS) and concurrent point lookups at several
# task counts on the bytecode VM. GEBLANG_BIN overrides the binary for
# A/B runs (mirrors benchmarks/run.sh).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BIN="${GEBLANG_BIN:-$ROOT/geblang}"
DB="${BENCH_DB_PATH:-$(mktemp -d)/bench.sqlite}"

echo "Seeding 1M rows at $DB ..."
"$BIN" --vm-strict "$SCRIPT_DIR/seed.gb" "$DB"

for mode in stream all func; do
    echo "--- 1M-row scan: $mode ---"
    /usr/bin/time -f "  max RSS: %M KB" "$BIN" --vm-strict "$SCRIPT_DIR/stream.gb" "$DB" "$mode"
done

for t in 1 8 32; do
    q=$((6400 / t))
    printf -- "--- concurrent lookups %dx%d --- " "$t" "$q"
    "$BIN" --vm-strict "$SCRIPT_DIR/concurrent.gb" "$DB" "$t" "$q"
done
