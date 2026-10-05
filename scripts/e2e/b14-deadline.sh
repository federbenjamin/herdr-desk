#!/usr/bin/env bash
# H21: a run past max_run_minutes is stopped by the ticker: its pane's processes are killed, the pane is closed, the run
# is killed, and its task is blocked with a note naming the limit. max_run_minutes is 1; the ticker checks once a minute,
# so this takes about two minutes.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
RC_MINUTES=1
runner_up home
ticker_up home

run 0 on home herdr-desk add -t "runs too long" --desk
set_mode 1 children
run 0 on home herdr-desk run start T1
wait_run 1 running
wait_file "$STUB/pids-run1"
two_pids() { [ "$(wc -l <"$STUB/pids-run1" | tr -d ' ')" -ge 2 ]; }
wait_long 10 "the child's pid" two_pids
sleep 20
if ! run_is 1 running || ! task_is 1 started; then fail "the run is $(run_field 1 state) and T1 $(task_field 1 status) after 20 s of a 60 s limit"; fi
pids_alive "$STUB/pids-run1" || fail "the worker died before the limit"
say "still running before the limit ok"

wait_long 150 "the ticker to stop run 1" run_is 1 killed
ok "run 1 killed"
wait_task 1 blocked 15
wait_note 1 "stopped after 1 minutes (runner.max_run_minutes)"
ok "T1 blocked with a note naming max_run_minutes"
wait_long 5 "the worker and its child to be gone" pids_dead "$STUB/pids-run1"
[ -z "$(pane_ids)" ] || fail "a pane is still open: $(pane_ids)"
ok "the pane is closed"
pass
