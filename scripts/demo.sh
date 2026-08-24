#!/usr/bin/env bash
# Runs a complete MAS-Turbo diagnosis against local stub telemetry.
#
# Nothing here touches a network, a cluster or a model API: the point is that a
# new user can see a real report one command after cloning.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${MAS_BIN:-$ROOT/bin/mas}"
OUT_DIR="${MAS_DEMO_OUT:-$ROOT/.demo}"

[[ -x "$BIN" ]] || { echo "build first: make build" >&2; exit 1; }
command -v python3 >/dev/null || { echo "demo needs python3 for the telemetry stubs" >&2; exit 1; }

mkdir -p "$OUT_DIR"
STUB_LOG="$OUT_DIR/stubs.log"

python3 "$ROOT/scripts/demo_stubs.py" >"$STUB_LOG" 2>&1 &
STUB_PID=$!
cleanup() { kill "$STUB_PID" 2>/dev/null || true; }
trap cleanup EXIT

# Wait for both stubs to accept connections.
for _ in $(seq 1 50); do
  if python3 - <<'PY' 2>/dev/null
import socket, sys
for port in (19090, 13100):
    s = socket.socket(); s.settimeout(0.2)
    try: s.connect(("127.0.0.1", port))
    except OSError: sys.exit(1)
    finally: s.close()
PY
  then break; fi
  sleep 0.1
done

echo "==> 1/3 deterministic only: a confident rule short-circuits the agent phase"
"$BIN" --config "$ROOT/examples/mas.demo.yaml" --store-dir "$OUT_DIR/runs" \
  diagnose --target redis-demo \
  --symptom "p99 latency spike with evictions and OOM errors" \
  --since 1h --format text

echo
echo "==> 2/3 full multi-agent investigation (supervisor topology, mock provider)"
"$BIN" --config "$ROOT/examples/mas.demo.yaml" --store-dir "$OUT_DIR/runs" \
  diagnose --target redis-demo --force-agents \
  --symptom "p99 latency spike with evictions and OOM errors" \
  --since 1h --format markdown --output "$OUT_DIR/report.en.md"

echo "==> 3/3 the same investigation, reported in Chinese"
"$BIN" --config "$ROOT/examples/mas.demo.yaml" --store-dir "$OUT_DIR/runs" --lang zh \
  diagnose --target redis-demo --force-agents \
  --symptom "延迟毛刺，伴随驱逐与 OOM 报错" \
  --since 1h --format markdown --output "$OUT_DIR/report.zh.md"

echo
echo "reports written to:"
echo "  $OUT_DIR/report.en.md"
echo "  $OUT_DIR/report.zh.md"
echo
sed -n '1,40p' "$OUT_DIR/report.en.md"
