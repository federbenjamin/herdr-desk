#!/usr/bin/env bash
# H24 (runner): `herdr-desk runs kill` leaves no process from the pane alive, closes the pane, and blocks the task, and
# only a person may kill. The pane's close fires the event hook, which finds no live run and changes nothing.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
with_events
RC_CAP=2
runner_up home

# T1: a worker with a child process.
run 0 on home herdr-desk add -t "kill me" --desk
set_mode 1 children
run 0 on home herdr-desk run start T1
wait_run 1 running
wait_file "$STUB/pids-run1"
pids_alive "$STUB/pids-run1" || fail "the worker or its child is not alive"
say "worker and its child alive before ok"

run 0 on home herdr-desk runs kill T1
out_has "T1 blocked"
wait_long 5 "the worker and its child to be gone" pids_dead "$STUB/pids-run1"
say "no process left ok"
[ -z "$(pane_ids)" ] || fail "a pane is still open: $(pane_ids)"
say "pane closed ok"
run_is 1 killed || fail "run 1 is $(run_field 1 state)"
task_is 1 blocked || fail "T1 is $(task_field 1 status)"
say "run killed ok"
sleep 2
run_is 1 killed || fail "the close event changed run 1 to $(run_field 1 state)"
task_is 1 blocked || fail "the close event changed T1 to $(task_field 1 status)"
say "the close event changed nothing ok"

run 1 on home herdr-desk runs kill T1
err_has "no-run"

# An agent may not kill a live run.
run 0 on home herdr-desk add -t "not yours to kill" --desk
run 0 on home herdr-desk run start T2
wait_run 2 running
run 1 as_agent home s-r04 herdr-desk runs kill T2
err_has "not-allowed"
run_is 2 running || fail "an agent's refused kill ended run 2"
say "agent kill not-allowed ok"
pass
