#!/usr/bin/env bash
# H10: one daemon per home. A second start is a clean no-op, a command starts the daemon when it
# is down, stop removes the socket, a client machine runs none, a config edit made while the daemon
# runs is named (a touch is not an edit), and a daemon that is not running answers status with exit 1
# and no refusal code.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"
INFO="$E2E/home/state/herdr-desk/daemon.json"

run 0 on home herdr-desk daemon run
out_has "already running"
run 0 on home herdr-desk daemon status
jq -e '.version != null and .tasks != null' <<<"$OUT" >/dev/null || fail "status is not the Status JSON"

run 0 on home herdr-desk daemon stop
wait_for "the socket to go" no_sock home
[ ! -e "$INFO" ] || fail "the info file outlived the daemon"
run 1 on home herdr-desk daemon status
any_lacks "home-unreachable"
no_sock home || fail "daemon status started a daemon"
run 0 on home herdr-desk daemon stop
say "stop ok"

run 0 on home herdr-desk add -t "started by a command" --desk
[ "$OUT" = "T1" ] || fail "add printed '$OUT', want T1"
[ -S "$(sock home)" ] || fail "no socket after a command that should start the daemon"
PID1=$(jq -r .pid "$INFO")
say "autostart ok"

run 0 on home herdr-desk daemon status
jq -e '.config_changed == false' <<<"$OUT" >/dev/null || fail "status says the config changed before any edit"
touch "$E2E/home/config/herdr-desk/config.toml"
run 0 on home herdr-desk list
any_lacks "herdr-desk daemon restart"
run 0 on home herdr-desk daemon status
jq -e '.config_changed == false' <<<"$OUT" >/dev/null || fail "status says the config changed after a touch"
sed -i.bak 's/poll_seconds = [0-9]*/poll_seconds = 31/' "$E2E/home/config/herdr-desk/config.toml"
grep -q 'poll_seconds = 31' "$E2E/home/config/herdr-desk/config.toml" || fail "the edit did not change the config"
run 0 on home herdr-desk list
err_has "herdr-desk daemon restart"
run 0 on home herdr-desk daemon status
jq -e '.config_changed == true' <<<"$OUT" >/dev/null || fail "status does not say the config changed after an edit"
say "config edit named ok"

run 0 on home herdr-desk daemon restart
wait_for "the daemon to answer after restart" on home herdr-desk daemon status
PID2=$(jq -r .pid "$INFO")
[ "$PID1" != "$PID2" ] || fail "restart kept pid $PID1"
run 0 on home herdr-desk list
out_has "started by a command"
any_lacks "herdr-desk daemon restart"
run 0 on home herdr-desk daemon status
jq -e '.config_changed == false' <<<"$OUT" >/dev/null || fail "status still says the config changed after the restart"
say "restart ok"

make_client cli home "$PORT"
run 0 on cli herdr-desk daemon run
out_has "is a client of"
no_sock cli || fail "a client machine has a daemon socket"
say "client no-op ok"
pass
