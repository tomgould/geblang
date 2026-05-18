#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/../.." && pwd)"
compose_file="$script_dir/docker-compose.yml"

services=(
  python-ext-example
  php-ext-example
  go-ext-example
  node-ext-example
)

ports=(9101 9102 9103 9104)

cleanup() {
  docker compose -f "$compose_file" down >/dev/null
}
trap cleanup EXIT

wait_for_port() {
  local port="$1"
  local deadline=$((SECONDS + 30))
  while (( SECONDS < deadline )); do
    if (echo >"/dev/tcp/127.0.0.1/$port") >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  echo "Timed out waiting for 127.0.0.1:$port" >&2
  return 1
}

docker compose -f "$compose_file" up --build -d "${services[@]}"

for port in "${ports[@]}"; do
  wait_for_port "$port"
done

(
  cd "$repo_root"
  GOCACHE="${GOCACHE:-/tmp/geblang-go-cache}" GOTOOLCHAIN="${GOTOOLCHAIN:-local}" \
    go run ./cmd/geblang examples/ext_tcp_examples.gb
)
