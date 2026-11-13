#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
python3 tools/patchset.py branch --target .work/application "$@"
cd .work/application
go mod download
