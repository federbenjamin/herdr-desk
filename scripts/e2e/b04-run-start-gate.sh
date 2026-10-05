#!/usr/bin/env bash
# H18: a worker session's `run start` is refused not-allowed; another agent's too; a person and the recorded
# coordinator may start a run.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
RC_CAP=3
runner_up home

# A person starts T1: its worker session is the one the gate must refuse next.
start_task home "started by a person" --desk
out_has "run 1  T1  running"
run_is 1 running || fail "run 1 is $(run_field 1 state)"
WORKER=$(run_field 1 session)
run 0 on home herdr-desk add -t "to be started" --desk

run 1 as_agent home "$WORKER" env DESK_RUN=1 herdr-desk run start T2 --isolation self
err_has "not-allowed"
[ -z "$(sqlite3 "$DB" "SELECT id FROM runs WHERE task = 2")" ] || fail "a worker's refused run start made a run"
ok "worker: not-allowed, exit 1"

run 1 as_agent home s-other herdr-desk run start T2 --isolation self
err_has "not-allowed"
[ -z "$(sqlite3 "$DB" "SELECT id FROM runs WHERE task = 2")" ] || fail "another agent's refused run start made a run"
ok "other agent: not-allowed"

ok "person: run 1 running"

run 0 on home herdr-desk coordinator
COORD=$(sqlite3 "$DB" "SELECT session FROM coordinator")
[ -n "$COORD" ] || fail "no coordinator is recorded"
run 0 as_agent home "$COORD" herdr-desk run start T2 --isolation self
out_has "run 2  T2  running"
run_is 2 running || fail "run 2 is $(run_field 2 state)"
ok "coordinator session: run 2 running"
pass
