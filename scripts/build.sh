#!/usr/bin/env bash
# Build: web -> embed -> Go binary
# Usage: scripts/build.sh [--skip-web] [--test]
set -euo pipefail
cd "$(dirname "$0")/.."

SKIP_WEB=0
RUN_TEST=0
for arg in "$@"; do
  case "$arg" in
    --skip-web) SKIP_WEB=1 ;;
    --test) RUN_TEST=1 ;;
    *) echo "unknown option: $arg" >&2; exit 2 ;;
  esac
done

if [ "$SKIP_WEB" -eq 0 ]; then
  [ -d web/node_modules ] || npm --prefix web install
  npm --prefix web run build
fi
if [ "$RUN_TEST" -eq 1 ]; then
  go test ./...
fi

VER="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
OUT="bin/poolsvc"
[ "$(go env GOOS)" = "windows" ] && OUT="bin/poolsvc.exe"
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X github.com/orangeruan/poolservice/internal/panelapi.Version=${VER}" \
  -o "$OUT" ./cmd/poolsvc
echo "built $OUT ($VER)"
