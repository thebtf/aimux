#!/usr/bin/env bash
# Local-dev aimux build with version injection identical to .goreleaser.yaml.
# Usage:
#   ./scripts/build.sh                       # outputs aimux-dev-next
#   ./scripts/build.sh -o aimux              # custom path
#   ./scripts/build.sh --race                # add -race detector

set -euo pipefail

OUTPUT="aimux-dev-next"
RACE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        -o|--output) OUTPUT="$2"; shift 2 ;;
        --race) RACE="-race"; shift ;;
        *) echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

cd "$(dirname "$0")/.."

VERSION="$(git describe --tags --always --dirty 2>/dev/null | sed 's/^v//' || echo '0.0.0-dev')"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

PKG="github.com/thebtf/aimux/pkg/build"
LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.BuildDate=${BUILD_DATE}"

echo "Building ${OUTPUT}  (Version=${VERSION}  Commit=${COMMIT}  BuildDate=${BUILD_DATE})"

go build ${RACE} -ldflags "${LDFLAGS}" -o "${OUTPUT}" ./cmd/aimux/

echo "OK: ${OUTPUT}"
