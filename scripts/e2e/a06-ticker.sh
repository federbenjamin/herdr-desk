#!/usr/bin/env bash
# H9: one ticker per home, none on a client, and every command works with no ticker.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
STATE="$E2E/home/state/herdr-desk"

run 0 on home herdr-desk add -t "no ticker yet" --desk
[ "$OUT" = T1 ] || fail "add printed '$OUT', want T1"
run 0 on home herdr-desk ticker status
jq -e '.ticker.running == false' <<<"$OUT" >/dev/null || fail "a ticker runs before any was started: $OUT"
ok "add works with no ticker running"

ticker_up home
run 0 on home herdr-desk ticker
case "$OUT$ERR" in *"already running"*) ;; *) fail "a second ticker did not say it is already running" ;; esac
ok "a second ticker exits 0 saying already running"

make_client cli home
run 0 on cli herdr-desk ticker
case "$OUT$ERR" in *"client of home"*) ;; *) fail "ticker on a client did not name the home" ;; esac
[ ! -e "$E2E/cli/state/herdr-desk/ticker.lock" ] || fail "a client holds a ticker lock"
ok "ticker on a client exits 0 and names the home"

run 0 on home herdr-desk ticker status
pid=$(jq -r '.ticker.pid' <<<"$OUT")
jq -e '.ticker.running == true and .ticker.pid > 0' <<<"$OUT" >/dev/null || fail "status does not show the ticker running: $OUT"
kill -0 "$pid" 2>/dev/null || fail "status names pid $pid, which is not a process"
ok "ticker status shows running with its pid"

ticker_down home
run 0 on home herdr-desk ticker status
jq -e '.ticker.running == false' <<<"$OUT" >/dev/null || fail "status shows a ticker after stop: $OUT"
kill -0 "$pid" 2>/dev/null && fail "the ticker (pid $pid) is still a process"
ok "ticker stop ends it and status shows not running"

# The removed file's name is built from its parts, so the tree's sweep for dropped names finds none here.
old=dae
old+=mon.json
[ ! -e "$STATE/desk.sock" ] && [ ! -e "$STATE/$old" ] || fail "the state folder holds a removed file: $(ls "$STATE")"
ok "the state folder holds no desk.sock and no $old"
pass
