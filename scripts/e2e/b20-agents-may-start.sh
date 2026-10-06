#!/usr/bin/env bash
# H2 (gate): an agent session that is not the coordinator starts a run in a root with agents_may_start = true and is
# refused in another root; a session that owns a live run (a worker) is refused, with or without DESK_RUN.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
CLOSED="$WORK/closed"
OPEN="$WORK/open"
mkdir -p "$CLOSED" "$OPEN"
add_root "$CLOSED" self "agents may not start runs here"
add_root "$OPEN" self "agents may start runs here" "agents_may_start = true"
RC_CAP=3
runner_up home

no_run_of() { [ -z "$(sqlite3 "$DB" "SELECT id FROM runs WHERE task = $1")" ]; }

# A person starts T1: its worker session owns a live run.
start_task home "started by a person" -p "$OPEN"
out_has "run 1  T1  running"
WORKER=$(run_field 1 session)
[ -n "$WORKER" ] || fail "run 1 has no session"
run 0 on home herdr-desk add -t "started by an agent" --desk
run 0 on home herdr-desk add -t "refused for agents" --desk

run 0 as_agent home s-launcher herdr-desk run start T2 --root "$OPEN"
out_has "T2  running"
ok "agent on an opted-in root: running"

run 1 as_agent home s-launcher herdr-desk run start T3 --root "$CLOSED"
err_has "not-allowed"
err_has "only a person or the desk's coordinator may start a run"
no_run_of 3 || fail "a refused run start made a run of T3"
ok "same session on another root: not-allowed"

run 1 as_agent home "$WORKER" herdr-desk run start T3 --root "$OPEN"
err_has "not-allowed"
err_has "owns a live run may not start one (run 1 of T1)"
no_run_of 3 || fail "a worker's refused run start made a run of T3"
ok "worker session with DESK_RUN unset: not-allowed"

run 1 as_agent home "$WORKER" env DESK_RUN=1 herdr-desk run start T3 --root "$OPEN"
err_has "owns a live run may not start one (run 1 of T1)"
no_run_of 3 || fail "a worker's refused run start made a run of T3"
ok "worker session on the opted-in root: not-allowed"
pass
