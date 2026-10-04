#!/bin/sh
# coverage.sh: runs the tests with coverage and exits 1 when statement coverage of ./internal/...
# (without internal/testutil) is under 80%, or when internal/store, internal/journal, or
# internal/secretscan is under 90%. A statement counts as covered when any package's tests run it.
set -eu

total_min=80
strict_min=90
strict_pkgs="internal/store internal/journal internal/secretscan"

cd "$(dirname "$0")/.."

pkgs=$(go list ./internal/... 2>/dev/null | grep -v '/internal/testutil$' | paste -sd, -) || true
if [ -z "$pkgs" ]; then
  echo "coverage: no packages under ./internal; nothing to measure" >&2
  exit 1
fi

profile=$(mktemp)
trap 'rm -f "$profile"' EXIT

go test -count=1 -coverpkg="$pkgs" -coverprofile="$profile" ./...

awk -v total_min="$total_min" -v strict_min="$strict_min" -v strict="$strict_pkgs" '
  NR == 1 { next }
  {
    split($1, loc, ":")
    file = loc[1]
    if (file ~ /\/internal\/testutil\//) next
    key = $1
    stmts[key] = $2
    pkgof[key] = file
    sub(/\/[^\/]*$/, "", pkgof[key])
    sub(/^.*\/internal\//, "internal/", pkgof[key])
    if ($3 > 0) hit[key] = 1
  }
  END {
    nstrict = split(strict, want, " ")
    for (k in stmts) {
      all += stmts[k]
      pkgall[pkgof[k]] += stmts[k]
      if (hit[k]) { allhit += stmts[k]; pkghit[pkgof[k]] += stmts[k] }
    }
    status = 0
    if (all == 0) { print "coverage: no statements measured" > "/dev/stderr"; exit 1 }
    pct = 100 * allhit / all
    printf "coverage: internal total %.1f%% (min %d%%)\n", pct, total_min
    if (pct < total_min) status = 1
    for (i = 1; i <= nstrict; i++) {
      p = want[i]
      if (pkgall[p] == 0) { printf "coverage: %s has no measured statements\n", p; status = 1; continue }
      pp = 100 * pkghit[p] / pkgall[p]
      printf "coverage: %s %.1f%% (min %d%%)\n", p, pp, strict_min
      if (pp < strict_min) status = 1
    }
    exit status
  }
' "$profile"
