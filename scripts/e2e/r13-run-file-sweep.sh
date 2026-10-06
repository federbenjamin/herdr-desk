#!/usr/bin/env bash
# H4 (runner): the ticker removes the first-message file of a run that ended within one tick, and leaves a live run's
# file. The ticker ticks once a minute, so this takes up to about a minute and a half.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
APP="$WORK/app"
mkdir -p "$APP"
add_root "$APP" self "any number of runs may share it" "first_message = '/go {task_file}'"
RC_CAP=2
runner_up home
ticker_up home
RUNS="$E2E/home/state/herdr-desk/runs"

run 0 on home herdr-desk add -t "hands back" --desk
run 0 on home herdr-desk add -t "keeps running" --desk
set_mode 1 handback
run 0 on home herdr-desk run start T1 --root "$APP"
wait_run 1 ended
ENDED=$(now)
wait_file "$STUB/worker-run1.message"
grep -F "/go $RUNS/run-1.md" "$STUB/worker-run1.message" >/dev/null || fail "run 1 did not start on its file: $(cat "$STUB/worker-run1.message")"
run 0 on home herdr-desk run start T2 --root "$APP"
wait_run 2 running
wait_file "$RUNS/run-2.md"

wait_long 90 "run-1.md to be removed" gone "$RUNS/run-1.md"
ok "run 1's file gone $(elapsed "$ENDED") s after it ended (under 90)"
[ -s "$RUNS/run-2.md" ] || fail "run 2 is $(run_field 2 state) and its file $RUNS/run-2.md is gone"
run_is 2 running || fail "run 2 is $(run_field 2 state), not running"
ok "run 2's file still there"
pass
