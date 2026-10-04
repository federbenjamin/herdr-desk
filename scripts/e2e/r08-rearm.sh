#!/usr/bin/env bash
# H8 (runner): a re-armed task starts a new run whose first message holds the history, and the old run can no longer
# set the status.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home
REASON="ROUTERREASON-the-scratch-root-fits"
route_to "$SCRATCH" in-place sonnet "$REASON"

run 0 on home desk add -t "Write the docs" -n "Check the README first." --desk --thread agent
run 0 on home desk steps T1 add "read it"
run 0 on home desk steps T1 add "fix it"
set_mode 1 blocked
run 0 on home desk set T1 ready
wait_run 1 running
wait_file "$STUB/worker-run1.message"
M1="$STUB/worker-run1.message"
for want in "Write the docs" "Check the README first." "read it" "fix it"; do
  grep -F -- "$want" "$M1" >/dev/null || fail "the first message lacks '$want'"
done
say "first message holds title, notes, steps ok"
SESSION1=$(run_field 1 session)

# The worker asks and the task blocks; a person answers and arms it again.
wait_task 1 blocked
run 0 on home desk note "Use the v2 API" --task T1
set_mode 1 busy
run 0 on home desk set T1 ready
wait_run 2 running
wait_file "$STUB/worker-run2.message"
M2="$STUB/worker-run2.message"
grep -F -- "Use the v2 API" "$M2" >/dev/null || fail "the second message lacks the answer note"
grep -F -- "History (oldest first):" "$M2" >/dev/null || fail "the second message has no history"
say "second message holds the answer note ok"
if grep -F -- "$REASON" "$M2" "$M1" >/dev/null; then fail "the router's reason reached a worker's message"; fi
say "router reason not in the message ok"
say "run 2 running ok"
SESSION2=$(run_field 2 session)
[ "$SESSION1" != "$SESSION2" ] || fail "both runs have the session $SESSION1"

# Run 1 is old: it may not set the status. Run 2 may.
run 1 as_agent home "$SESSION1" env DESK_RUN=1 desk set T1 review
err_has "stale-run"
task_is 1 started || fail "a stale run changed T1 to $(task_field 1 status)"
run 0 as_agent home "$SESSION2" env DESK_RUN=2 desk set T1 review --ref e2e
task_is 1 review || fail "run 2's hand-back left T1 $(task_field 1 status)"
say "new run sets review ok"
pass
