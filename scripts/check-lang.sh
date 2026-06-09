#!/usr/bin/env bash
# check-lang.sh: drive `geblang check` over tests/.
#
# tests/check/ contains files that MUST each produce at least one diagnostic
# (checked under --strict so warnings count). Every other tests/ file MUST
# pass `geblang check` clean.

set -u

BIN=${BIN:-./geblang}
TESTS_DIR=${TESTS_DIR:-tests}

if [ ! -x "$BIN" ]; then
    echo "check-lang.sh: $BIN not built; run 'make build' first" >&2
    exit 1
fi

bad_dir="$TESTS_DIR/check"
total_bad=0
unflagged_bad=0
if [ -d "$bad_dir" ]; then
    for f in "$bad_dir"/*.gb; do
        [ -f "$f" ] || continue
        total_bad=$((total_bad + 1))
        if "$BIN" check --strict "$f" >/dev/null 2>&1; then
            echo "check-lang: $f was expected to fail but check passed" >&2
            unflagged_bad=$((unflagged_bad + 1))
        fi
    done
fi

failed_clean=0
total_clean=0
for f in $(find "$TESTS_DIR" -name '*.gb' -not -path "$bad_dir/*"); do
    total_clean=$((total_clean + 1))
    if ! "$BIN" check "$f" >/dev/null 2>&1; then
        echo "check-lang: $f failed check unexpectedly:" >&2
        "$BIN" check "$f" >&2
        failed_clean=$((failed_clean + 1))
    fi
done

echo "check-lang: $total_bad expected-to-fail files (${unflagged_bad} silently passed)"
echo "check-lang: $total_clean clean files (${failed_clean} produced diagnostics)"

if [ "$unflagged_bad" -gt 0 ] || [ "$failed_clean" -gt 0 ]; then
    exit 1
fi
