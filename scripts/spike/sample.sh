#!/bin/sh
# Spike for the PRD's riskiest assumption: does summing `top -o power` once a
# minute rank apps the way Activity Monitor's "12 hr Power" column does, and
# what does the sampling itself cost?
#
# Throwaway — the answer is the deliverable, not this script.
# Writes one tab-separated row per process per tick:
#   ts  pid  command(16 chars, as top prints it)  power  full executable path
#
# Run:   nohup scripts/spike/sample.sh >/tmp/batlog-spike.log 2>&1 &
# Stop:  kill "$(cat /tmp/batlog-spike.pid)"
# Read:  scripts/spike/rank.sh
set -u
OUT="${BATLOG_SPIKE_OUT:-$HOME/batlog-spike.tsv}"
INTERVAL="${BATLOG_SPIKE_INTERVAL:-60}"
TMP="$(mktemp -t batlog-spike-ps)"
trap 'rm -f "$TMP"; exit 0' INT TERM

echo $$ > /tmp/batlog-spike.pid
[ -f "$OUT" ] || printf 'ts\tpid\tcommand\tpower\tpath\n' > "$OUT"
echo "sampling every ${INTERVAL}s -> $OUT   (pid $$)"

while :; do
  ts=$(date +%s)
  # `top` truncates names to 16 chars, so fetch full paths by pid in the same tick.
  ps -axo pid=,comm= > "$TMP"
  # -l 2: keep the second sample only; the first always reads zero.
  top -l 2 -o power -n 40 -stats pid,command,power 2>/dev/null \
  | awk -v ts="$ts" -v pf="$TMP" '
      BEGIN {
        while ((getline line < pf) > 0) {
          sub(/^ +/, "", line); pid = line; sub(/ .*/, "", pid)
          path = line; sub(/^[0-9]+ +/, "", path); P[pid] = path
        }
      }
      /^PID/ { hdr++; next }
      hdr == 2 && /^[0-9]/ {
        pid = $1; pw = $NF; cmd = ""
        for (i = 2; i < NF; i++) cmd = cmd (i > 2 ? " " : "") $i
        printf "%s\t%s\t%s\t%s\t%s\n", ts, pid, cmd, pw, P[pid]
      }' >> "$OUT"
  sleep "$INTERVAL"
done
