#!/bin/sh
# Sum `top` power per app over the last H hours and print the top N.
# Compare the list with Activity Monitor -> Energy -> "12 hr Power" column.
#
# Usage: scripts/spike/rank.sh [hours=12] [n=10]
#
# App name = the first ".app" in the executable path (so every Brave helper
# rolls up into "Brave Browser"); processes outside a bundle keep the name
# `top` printed. Look for `top` itself in the list — that is what the sampling
# costs, and it must stay far outside the top ten.
set -u
OUT="${BATLOG_SPIKE_OUT:-$HOME/batlog-spike.tsv}"
HOURS="${1:-12}"
N="${2:-10}"
since=$(( $(date +%s) - HOURS * 3600 ))

[ -f "$OUT" ] || { echo "no data yet at $OUT — is sample.sh running?" >&2; exit 1; }

awk -F'\t' -v since="$since" '
  NR > 1 && $1 >= since {
    app = $3
    if (match($5, /[^\/]+\.app\//)) app = substr($5, RSTART, RLENGTH - 5)
    e[app] += $4; total += $4; t[$1] = 1
  }
  END {
    for (k in t) ticks++
    printf "ticks\t%d\t%.1f\n", ticks, total
    for (a in e) printf "row\t%s\t%.1f\n", a, e[a]
  }' "$OUT" > "/tmp/batlog-rank.$$"

ticks=$(awk -F'\t' '$1=="ticks"{print $2}' "/tmp/batlog-rank.$$")
total=$(awk -F'\t' '$1=="ticks"{print $3}' "/tmp/batlog-rank.$$")
printf 'last %sh · %s ticks (%.1f h of data)\n\n' "$HOURS" "$ticks" "$(echo "$ticks / 60" | bc -l)"
printf '%-3s %-34s %6s\n' '#' 'APP' 'SHARE'
grep '^row' "/tmp/batlog-rank.$$" | sort -t"$(printf '\t')" -k3 -rn | head -n "$N" \
| awk -F'\t' -v total="$total" '{ printf "%-3d %-34s %5.1f%%\n", NR, $2, $3 / total * 100 }'

printf '\nsampler cost: '
grep '^row' "/tmp/batlog-rank.$$" | awk -F'\t' -v total="$total" '$2=="top" { printf "top = %.2f%% of total energy\n", $3 / total * 100; f=1 } END { if (!f) print "top did not appear in the samples (good)" }'
rm -f "/tmp/batlog-rank.$$"
