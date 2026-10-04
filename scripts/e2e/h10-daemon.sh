#!/usr/bin/env bash
# H10: one daemon per home. A second start is a clean no-op, a command starts the daemon when it
# is down, stop removes the socket, and a client machine runs none.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"
INFO="$E2E/home/state/desk/daemon.json"

run 0 on home desk daemon run
out_has "already running"
run 0 on home desk daemon status
jq -e '.version != null and .tasks != null' <<<"$OUT" >/dev/null || fail "status is not the Status JSON"

run 0 on home desk daemon stop
wait_for "the socket to go" no_sock home
[ ! -e "$INFO" ] || fail "the info file outlived the daemon"
run 1 on home desk daemon status
no_sock home || fail "daemon status started a daemon"
run 0 on home desk daemon stop
say "stop ok"

run 0 on home desk add -t "started by a command" --desk
[ "$OUT" = "T1" ] || fail "add printed '$OUT', want T1"
[ -S "$(sock home)" ] || fail "no socket after a command that should start the daemon"
PID1=$(jq -r .pid "$INFO")
say "autostart ok"

run 0 on home desk daemon restart
wait_for "the daemon to answer after restart" on home desk daemon status
PID2=$(jq -r .pid "$INFO")
[ "$PID1" != "$PID2" ] || fail "restart kept pid $PID1"
run 0 on home desk list
out_has "started by a command"
say "restart ok"

make_client cli home "$PORT"
run 0 on cli desk daemon run
out_has "is a client of"
no_sock cli || fail "a client machine has a daemon socket"
say "client no-op ok"
pass
