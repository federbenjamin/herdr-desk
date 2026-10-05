#!/usr/bin/env bash
# H2: with the home down, a client's add exits 3 and bare herdr-desk shows the offline banner.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"
run 0 on home herdr-desk add -t "seen before the outage" --desk
make_client cli home "$PORT"
run 0 on cli herdr-desk list
out_has "seen before the outage"

stop_daemon home

run 3 on cli herdr-desk add -t "while offline" --desk
err_has "home-unreachable"
[ -z "$OUT" ] || fail "a refused add printed on stdout: $OUT"

run 0 on cli herdr-desk
first=$(head -n 1 <<<"$OUT")
case "$first" in *"offline (snapshot "*) ;; *) fail "the board's first line is '$first'" ;; esac
[[ "$ERR" =~ showing\ the\ snapshot\ from\ [0-9]{4}-[0-9]{2}-[0-9]{2}\ [0-9]{2}:[0-9]{2}Z$ ]] ||
  fail "the offline notice does not give the snapshot time in UTC with a Z: $ERR"
out_has "seen before the outage"

run 0 on cli herdr-desk list --json
jq -e '.offline == true and .snapshot_ts != null and (.tasks | length) == 1' <<<"$OUT" >/dev/null ||
  fail "the offline list is not marked offline with its snapshot time"

run 3 on cli herdr-desk show T1
err_has "home-unreachable"
run 3 on cli herdr-desk list --done
err_has "home-unreachable"
pass
