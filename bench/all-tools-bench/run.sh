#!/usr/bin/env bash
# Drive the all-tools-bench binary against the gortex daemon for each
# storage backend. Sequential — only one daemon up at a time so they
# can share the default unix socket / HTTP port.
#
# Inputs (env or arg defaults):
#   BIN              gortex binary to run                  (default: /tmp/gortex-lbug)
#   ADDR             http addr for the daemon              (default: 127.0.0.1:7090)
#   TOKEN            bearer token                          (default: x)
#   RESULTS_DIR      output dir for JSON + log per backend (default: /tmp/all-tools-bench-results)
#   BACKENDS         space-separated list of backend tags  (default: "memory ladybug")
#   LBUG_PATH        path for ladybug store dir            (default: /tmp/gortex-daemon-lbug-all/store.lbug)
#   WAIT_MAX_S       seconds to wait for warmup ready      (default: 1500 — ladybug warmup is slow)
#   LBUG_KEEP_STORE  set =1 to skip the cleanup of LBUG_PATH between runs (default: 0 = fresh)

set -euo pipefail

BIN="${BIN:-/tmp/gortex-lbug}"
ADDR="${ADDR:-127.0.0.1:7090}"
TOKEN="${TOKEN:-x}"
RESULTS_DIR="${RESULTS_DIR:-/tmp/all-tools-bench-results}"
BACKENDS="${BACKENDS:-memory ladybug}"
LBUG_PATH="${LBUG_PATH:-/tmp/gortex-daemon-lbug-all/store.lbug}"
WAIT_MAX_S="${WAIT_MAX_S:-1500}"

mkdir -p "$RESULTS_DIR"
SOCK_PATH="$HOME/.cache/gortex/daemon.sock"

stop_daemon() {
    if [[ -n "${DAEMON_PID:-}" ]]; then
        if kill -0 "$DAEMON_PID" 2>/dev/null; then
            kill -TERM "$DAEMON_PID" 2>/dev/null || true
            for _ in {1..40}; do
                kill -0 "$DAEMON_PID" 2>/dev/null || break
                sleep 0.2
            done
            kill -KILL "$DAEMON_PID" 2>/dev/null || true
        fi
        DAEMON_PID=""
    fi
    rm -f "$SOCK_PATH"
    sleep 0.5
}

trap 'stop_daemon' EXIT INT TERM

http_url() {
    printf 'http://%s' "${ADDR#http://}"
}

wait_for_ready() {
    local log="$1"
    local started=$SECONDS
    while (( SECONDS - started < WAIT_MAX_S )); do
        if grep -q '"daemon: watching"' "$log" 2>/dev/null; then
            return 0
        fi
        if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
            echo "ERROR: daemon died during warmup. Last log:" >&2
            tail -60 "$log" >&2
            return 1
        fi
        sleep 1
    done
    echo "TIMEOUT after ${WAIT_MAX_S}s waiting for warmup. Tail:" >&2
    tail -60 "$log" >&2
    return 1
}

bench_one() {
    local backend="$1"
    local log="$RESULTS_DIR/daemon-$backend.log"
    local out="$RESULTS_DIR/results-$backend.json"
    local args=(--backend "$backend" --http-addr "$ADDR" --http-auth-token "$TOKEN")

    if [[ "$backend" == "ladybug" ]]; then
        # Default: fresh on-disk store every run so the cold-start path
        # is honest. Set LBUG_KEEP_STORE=1 to keep the existing store and
        # measure post-warmup tool latency only (useful when iterating
        # the tool battery without paying for re-warmup each round).
        if [[ "${LBUG_KEEP_STORE:-0}" != "1" ]]; then
            rm -rf "$(dirname "$LBUG_PATH")"
            mkdir -p "$(dirname "$LBUG_PATH")"
        fi
        args+=(--backend-path "$LBUG_PATH")
    fi

    stop_daemon

    echo ""
    echo "==================================================================="
    echo "== Backend: $backend"
    echo "==================================================================="

    : >"$log"
    local start_epoch
    start_epoch=$(perl -e 'use Time::HiRes qw(time); printf "%.3f", time')

    nohup "$BIN" --log-level debug daemon start "${args[@]}" \
        >"$log" 2>&1 < /dev/null &
    DAEMON_PID=$!
    disown 2>/dev/null || true

    echo "[$backend] daemon launched (pid=$DAEMON_PID), log=$log"
    if ! wait_for_ready "$log"; then
        return 1
    fi

    local ready_epoch
    ready_epoch=$(perl -e 'use Time::HiRes qw(time); printf "%.3f", time')
    local warmup_s
    warmup_s=$(awk -v s="$start_epoch" -v r="$ready_epoch" 'BEGIN{printf "%.2f", r-s}')
    echo "[$backend] warmup → ready: ${warmup_s}s"

    sleep 2

    echo "[$backend] running tool battery..."
    /tmp/all-tools-bench \
        --addr "$(http_url)" \
        --token "$TOKEN" \
        --label "$backend" \
        --json "$out" \
    || echo "[$backend] all-tools-bench exited non-zero (continuing)"

    echo "[$backend] saved $out"

    stop_daemon
    echo "[$backend] done."
}

# Build the bench binary once.
echo "== building all-tools-bench =="
(cd "$(dirname "$0")/../.." && go build -o /tmp/all-tools-bench ./bench/all-tools-bench/)

# Run each backend in turn.
for backend in $BACKENDS; do
    bench_one "$backend" || echo "[$backend] FAILED, continuing"
done

echo ""
echo "==================================================================="
echo "== Summary"
echo "==================================================================="
for backend in $BACKENDS; do
    out="$RESULTS_DIR/results-$backend.json"
    if [[ -f "$out" ]]; then
        echo ""
        echo "-- $backend --"
        python3 - "$out" <<'PY'
import json, sys
with open(sys.argv[1]) as f:
    d = json.load(f)
print(f"label={d['label']}, total_ms={d['total_ms']}")
ok = sum(1 for r in d['records'] if r['status'] == 'ok')
em = sum(1 for r in d['records'] if r['status'] == 'empty')
ae = sum(1 for r in d['records'] if r['status'] == 'argerror')
er = sum(1 for r in d['records'] if r['status'] == 'error')
print(f"ok={ok} empty={em} argerror={ae} error={er} / {len(d['records'])}")
PY
    else
        echo "-- $backend -- (no result file)"
    fi
done

# If both backends ran, emit a side-by-side comparison sorted by
# ladybug latency descending — slow tools rise to the top.
mem="$RESULTS_DIR/results-memory.json"
lbug="$RESULTS_DIR/results-ladybug.json"
if [[ -f "$mem" && -f "$lbug" ]]; then
    echo ""
    echo "==================================================================="
    echo "== Comparison (sorted by ladybug ms desc)"
    echo "==================================================================="
    python3 - "$mem" "$lbug" <<'PY'
import json, sys
with open(sys.argv[1]) as f: mem = json.load(f)
with open(sys.argv[2]) as f: lb  = json.load(f)
mem_by = {r['label']: r for r in mem['records']}
lb_by  = {r['label']: r for r in lb['records']}
labels = sorted(set(mem_by) | set(lb_by))
rows = []
for lab in labels:
    m, l = mem_by.get(lab), lb_by.get(lab)
    ms_m = m['elapsed_ms'] if m else -1
    ms_l = l['elapsed_ms'] if l else -1
    ratio = (ms_l / ms_m) if (m and l and ms_m > 0) else float('nan')
    rows.append((lab, ms_m, ms_l, ratio,
                 m['status'] if m else '-', l['status'] if l else '-',
                 m['output_bytes'] if m else 0, l['output_bytes'] if l else 0,
                 (m['category'] if m else (l['category'] if l else '-'))))
rows.sort(key=lambda r: -r[2])
print(f"{'cat':<10} {'tool':<46} {'mem_ms':>8} {'lb_ms':>8} {'ratio':>6} {'mem':>6} {'lb':>6} {'memB':>8} {'lbB':>8}")
for r in rows:
    rstr = f"{r[3]:.2f}" if r[3] == r[3] else "-"
    print(f"{r[8]:<10} {r[0]:<46} {r[1]:>8} {r[2]:>8} {rstr:>6} {r[4]:>6} {r[5]:>6} {r[6]:>8} {r[7]:>8}")
PY
fi
