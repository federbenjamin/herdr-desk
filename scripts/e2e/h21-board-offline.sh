#!/usr/bin/env bash
# H21: with the home down, a client's board shows the offline banner and refuses n.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PORT=$(free_port)
home_with_listen home "$PORT"
run 0 on home desk add -t "seen before the outage" --desk
make_client cli home "$PORT"
run 0 on cli desk list
out_has "seen before the outage"

stop_daemon home

term_start board 100 30 cli "$BIN/desk"
term_wait board "offline (snapshot"
term_wait board "seen before the outage"
ok "the header shows offline (snapshot"

term_keys board n
term_wait board "offline: n needs the home"
ok "n shows offline: n needs the home"

start_daemon home
[ "$(task_field home 1 .task.status)" = open ] || fail "the task is not open after the home returned"
ok "after the home returns the task is still open"

term_wait_gone board "offline (snapshot"
term_keys board n
wait_task home 1 .task.status ready
ok "the banner clears and n sets ready"

term_keys board q
pass
