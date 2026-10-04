#!/usr/bin/env bash
# H9 (runner): a run past the time limit is killed and its task blocked. runner.max_run_minutes is 1, so this takes
# a little over a minute.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
RC_MINUTES=1
runner_up home
route_to "$SCRATCH" in-place sonnet "stub"

run 0 on home desk add -t "runs too long" --desk --thread agent
set_mode 1 children
run 0 on home desk set T1 ready
wait_run 1 running
wait_file "$STUB/pids-run1"
sleep 20
if ! run_is 1 running || ! task_is 1 started; then fail "the run is $(run_field 1 state) and T1 $(task_field 1 status) after 20 s of a 60 s limit"; fi
pids_alive "$STUB/pids-run1" || fail "the worker died before the limit"
say "still running before the limit ok"

wait_task 1 blocked 90
task_has_note 1 "stopped after 1 minutes (runner.max_run_minutes)" || fail "no 'stopped after' note: $(task_notes 1)"
say "blocked after the limit ok"
wait_long 5 "the worker and its child to be gone" pids_dead "$STUB/pids-run1"
say "no process left ok"
run_is 1 killed || fail "run 1 is $(run_field 1 state)"
say "run killed ok"
pass
