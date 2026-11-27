#!/usr/bin/env bash
# 构建 Windows E2E 包：sesscope.exe + 夹具 + run-e2e.cmd，输出到 <share>/sesscope-e2e.tgz。
#
#   tools/windows-e2e/package.sh /path/to/http/root
#
set -euo pipefail
cd "$(dirname "$0")/../.."
ROOT="$1"
if [ -z "$ROOT" ]; then
  echo "用法：tools/windows-e2e/package.sh <http-root>" >&2
  exit 2
fi
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
mkdir -p "$STAGE/sesscope-e2e"

GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -X main.version=windows-e2e" \
  -o "$STAGE/sesscope-e2e/sesscope.exe" .
go run ./tools/windows-e2e/gen -out "$STAGE/gen"
cp -a "$STAGE/gen/fixtures" "$STAGE/sesscope-e2e/fixtures"
cp tools/windows-e2e/run-e2e.cmd "$STAGE/sesscope-e2e/run-e2e.cmd"
python3 - "$STAGE/sesscope-e2e/run-e2e.cmd" <<'PY'
import sys
from pathlib import Path
p = Path(sys.argv[1])
p.write_bytes(p.read_bytes().replace(b"\r\n", b"\n").replace(b"\n", b"\r\n"))
PY
tar -czf "$ROOT/sesscope-e2e.tgz" -C "$STAGE/sesscope-e2e" .
echo "已生成 $ROOT/sesscope-e2e.tgz"
