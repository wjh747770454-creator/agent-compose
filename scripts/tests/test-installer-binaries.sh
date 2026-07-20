#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf -- "$TEST_ROOT"' EXIT

VERSION=installer-test "$ROOT_DIR/scripts/build-installer-binaries.sh" "$TEST_ROOT/assets" >/dev/null

expected=$'SHASUMS256.txt\nagent-compose-installer-linux-amd64\nagent-compose-installer-linux-arm64\ninstall.sh'
actual=$(find "$TEST_ROOT/assets" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)
[[ "$actual" == "$expected" ]] || { printf 'unexpected installer assets:\n%s\n' "$actual" >&2; exit 1; }
(
  cd "$TEST_ROOT/assets"
  sha256sum -c SHASUMS256.txt >/dev/null
)
readelf -h "$TEST_ROOT/assets/agent-compose-installer-linux-amd64" | grep -Fq 'Advanced Micro Devices X86-64'
readelf -h "$TEST_ROOT/assets/agent-compose-installer-linux-arm64" | grep -Fq 'AArch64'
go version -m "$TEST_ROOT/assets/agent-compose-installer-linux-amd64" \
  | grep -Eq 'path[[:space:]]+agent-compose/cmd/installer'
cmp "$ROOT_DIR/deploy/install.sh" "$TEST_ROOT/assets/install.sh" >/dev/null

printf 'test-installer-binaries: all checks passed\n'
