#!/usr/bin/env bash
# H28: closing a worker's pane sets its task review within two seconds, with no ticker running: herdr's pane.closed event
# runs the hook, which asks herdr for the pane and finds it gone.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
with_events
runner_up home

start_task home "close my pane" --desk
wait_file "$STUB/worker-run1.env"
ticker_stopped home || fail "a ticker runs"
ok "no ticker runs"

PANE=$(run_field 1 pane)
T0=$(now)
herdr_do pane close "$PANE" >/dev/null
wait_long 2 "T1 to be review within 2 s of the close" task_is 1 review
N=$(elapsed "$T0")
under "$N" 2 || fail "T1 went to review $N s after the close, not under 2"
ok "T1 review $N s after the close (under 2)"
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
task_has_note 1 "the pane closed without a hand-back" || fail "no note says the pane closed: $(task_notes 1)"
ok "run 1 ended"
pass
