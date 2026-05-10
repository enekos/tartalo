#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")"
export LC_NUMERIC=C
export LANG=C
echo "Running benchmark checks..."
go test ./internal/codegen/... ./internal/nativegen/... -count=1 -short
echo "Checks passed."