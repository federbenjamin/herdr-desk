#!/usr/bin/env bash
# H26 (runner): a second run of a task starts with a first message that holds the history, and the first run can no
# longer set the task's status.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home

run 0 on home herdr-desk add -t "Write the docs" -n "Check the README first." --desk
run 0 on home herdr-desk steps T1 add "read it"
run 0 on home herdr-desk steps T1 add "fix it"
run 0 on home herdr-desk run start T1
wait_run 1 running
wait_file "$STUB/worker-run1.message"
M1="$STUB/worker-run1.message"
for want in "Write the docs" "Check the README first." "read it" "fix it"; do
  grep -F -- "$want" "$M1" >/dev/null || fail "the first message lacks '$want'"
done
say "first message holds title, notes, steps ok"
SESSION1=$(run_field 1 session)

# Run 1's worker writes a note and asks; its own `blocked` ends the run. A person answers and starts the task again.
run 0 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk note "Found the v1 API in the README" --task T1
run 0 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk set T1 blocked
run_is 1 ended || fail "run 1 is $(run_field 1 state) after its worker's blocked"
run 0 on home herdr-desk note "Use the v2 API" --task T1
run 0 on home herdr-desk run start T1
wait_run 2 running
wait_file "$STUB/worker-run2.message"
M2="$STUB/worker-run2.message"
grep -F -- "History (oldest first):" "$M2" >/dev/null || fail "the second message has no history"
grep -F -- "Found the v1 API in the README" "$M2" >/dev/null || fail "the second message lacks run 1's note"
grep -F -- "Use the v2 API" "$M2" >/dev/null || fail "the second message lacks the answer note"
ok "run 2's message holds run 1's note"
SESSION2=$(run_field 2 session)
[ "$SESSION1" != "$SESSION2" ] || fail "both runs have the session $SESSION1"

# Run 1 is old: it may not set the status. Run 2 may.
run 1 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk set T1 review
err_has "stale-run"
task_is 1 started || fail "a stale run changed T1 to $(task_field 1 status)"
ok "run 1's set is stale-run"
run 0 as_agent home "$SESSION2" env DESK_RUN=2 herdr-desk set T1 review --ref e2e
task_is 1 review || fail "run 2's hand-back left T1 $(task_field 1 status)"
ok "run 2's set review lands"
pass
