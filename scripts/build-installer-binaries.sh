#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
OUTPUT_DIR=${1:-"$ROOT_DIR/upload-installer"}
VERSION=${VERSION:-dev}

[[ ! -e "$OUTPUT_DIR" && ! -L "$OUTPUT_DIR" ]] \
  || { printf 'build-installer-binaries: output path already exists: %s\n' "$OUTPUT_DIR" >&2; exit 1; }
command -v go >/dev/null 2>&1 || { printf 'build-installer-binaries: go is required\n' >&2; exit 1; }
command -v sha256sum >/dev/null 2>&1 || { printf 'build-installer-binaries: sha256sum is required\n' >&2; exit 1; }

mkdir -p -- "$(dirname -- "$OUTPUT_DIR")"
WORK_DIR=$(mktemp -d "$(dirname -- "$OUTPUT_DIR")/.installer-binaries.XXXXXX")
cleanup() {
  rm -rf -- "$WORK_DIR"
}
trap cleanup EXIT

for arch in amd64 arm64; do
  asset="agent-compose-installer-linux-$arch"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$WORK_DIR/$asset" "$ROOT_DIR/cmd/installer"
  chmod 0755 "$WORK_DIR/$asset"
done
install -m 0755 "$ROOT_DIR/deploy/install.sh" "$WORK_DIR/install.sh"
(
  cd "$WORK_DIR"
  sha256sum agent-compose-installer-linux-amd64 agent-compose-installer-linux-arm64 >SHASUMS256.txt
)
chmod 0644 "$WORK_DIR/SHASUMS256.txt"
mv -- "$WORK_DIR" "$OUTPUT_DIR"
trap - EXIT
printf 'Built installer release assets in %s\n' "$OUTPUT_DIR"
