#!/usr/bin/env bash
# 真机四源端到端冒烟：sources → 每源 sessions → 每源导出 1 个会话 → 页面冒烟。
# 不联网、只读；结果写 out/smoke/report.json（含每源耗时与产物大小）。
set -u
cd "$(dirname "$0")/.." || exit 1
BIN=./out/sesscope
DIR=out/smoke
mkdir -p "$DIR"
REPORT="$DIR/report.json"
printf '[\n' > "$REPORT"
first=1
fail=0

for src in opencode dsh codex claude; do
  start=$(date +%s%3N)
  json=$("$BIN" sessions --source "$src" --limit 1 --all --json 2>/dev/null || true)
  id=$(printf '%s' "$json" | sed -n 's/.*"id": "\([^"]*\)".*/\1/p' | head -1)
  if [ -z "$id" ]; then
    echo "[$src] 没有会话，跳过"
    continue
  fi
  html="$DIR/$src.html"
  # 会话 id 只在来源内唯一，选择器必须带来源（这里等价于 --source "$src"）。
  out=$("$BIN" export --session "$src:$id" --html "$html" --quiet 2>&1)
  code=$?
  size=0
  [ -f "$html" ] && size=$(wc -c < "$html" | tr -d ' ')
  elapsed=$(( $(date +%s%3N) - start ))
  smoke="skipped"
  if [ -f "$html" ]; then
    if node tools/cdp-smoke.mjs --html "$html" > "$DIR/$src.smoke.log" 2>&1; then smoke="pass"; else smoke="fail"; fail=1; fi
  fi
  [ $code -ne 0 ] && fail=1
  [ $first -eq 0 ] && printf ',\n' >> "$REPORT"
  first=0
  printf '  {"source":"%s","session":"%s","html_bytes":%s,"elapsed_ms":%s,"exit_code":%s,"page_smoke":"%s"}' \
    "$src" "$id" "$size" "$elapsed" "$code" "$smoke" >> "$REPORT"
  printf '[%s] %s → %s（%s 字节，%s ms，页面冒烟 %s）\n' "$src" "$id" "$html" "$size" "$elapsed" "$smoke"
  [ -n "$out" ] && printf '    %s\n' "$out"
done

printf '\n]\n' >> "$REPORT"
echo "报告：$REPORT"
exit $fail
