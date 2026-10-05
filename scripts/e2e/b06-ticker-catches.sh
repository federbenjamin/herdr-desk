#!/usr/bin/env bash
# H29: with DESK_HOOKS=off the hook writes nothing for a closed pane; the ticker's reconcile, once a minute while a run is
# live, catches it within 90 seconds.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
with_events
runner_up home
ticker_up home

start_task home "close my pane quietly" --desk
wait_file "$STUB/worker-run1.env"
wait_note 1 "run 1: workspace"
PANE=$(run_field 1 pane)
EVENTS=$(sqlite3 "$DB" "SELECT count(*) FROM events")

# The fake fires the hook with the environment of the call: DESK_HOOKS=off reaches it.
T0=$(now)
on home env DESK_HOOKS=off "$DESK_HERDR" pane close "$PANE" >/dev/null
sleep 3
[ "$(sqlite3 "$DB" "SELECT count(*) FROM events")" = "$EVENTS" ] || fail "the hook wrote to the store with DESK_HOOKS=off"
run_is 1 running || fail "run 1 is $(run_field 1 state) with the hook off"
task_is 1 started || fail "T1 is $(task_field 1 status) with the hook off"
ok "the hook wrote nothing"

wait_long 90 "the ticker to set T1 review" task_is 1 review
N=$(elapsed "$T0")
under "$N" 90 || fail "T1 went to review $N s after the close, not under 90"
ok "T1 review $N s after the close (under 90)"
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
pass
