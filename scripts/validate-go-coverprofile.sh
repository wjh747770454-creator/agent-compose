#!/usr/bin/env bash
set -euo pipefail

profile="${1:?usage: validate-go-coverprofile.sh <profile>}"

go tool cover -func="$profile" >/dev/null
