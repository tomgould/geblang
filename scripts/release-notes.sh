#!/usr/bin/env bash
# Print the release-notes section for a version from the user docs, for
# `goreleaser release --release-notes`. Usage: scripts/release-notes.sh 1.29.2
set -euo pipefail
ver="${1:?usage: scripts/release-notes.sh <version>}"
notes="$(cd "$(dirname "$0")/.." && pwd)/docs/user/18-release-notes.md"
out="$(awk -v v="## ${ver}" '
  $0 == v { f = 1; next }
  f && /^## / { exit }
  f { print }
' "$notes")"
if [ -z "$(printf '%s' "$out" | tr -d '[:space:]')" ]; then
  echo "release-notes.sh: no section '## ${ver}' in ${notes}" >&2
  exit 1
fi
printf '%s\n' "$out"
