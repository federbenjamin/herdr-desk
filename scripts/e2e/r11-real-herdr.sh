#!/usr/bin/env bash
# H11 (runner): on the real herdr, with the stub router and worker: a workspace opens without taking focus, the pane
# is found by its agent session, an idle worker goes to review, and a kill leaves nothing. People may be working in
# this herdr: the script closes only the workspaces a run row of its own desk names, and nothing else.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
need_real_herdr
build
RC_NOTIFY=none
runner_up home
route_to "$SCRATCH" in-place sonnet "stub"
FOCUS=$(focused_workspace)

# T1: a worker that goes idle without handing back.
run 0 on home desk add -t "desk e2e: go idle" --desk --thread agent
set_mode 1 idle
run 0 on home desk set T1 ready
wait_run 1 running 30
wait_file "$STUB/worker-run1.env"
track_workspaces
SESSION=$(run_field 1 session)
PANE=$(run_field 1 pane)
[ "$(focused_workspace)" = "$FOCUS" ] || fail "focus moved from '$FOCUS' to '$(focused_workspace)'"
say "focus unchanged ok"
wait_long 30 "herdr to show the agent session $SESSION" pane_has_session "$SESSION"
[ "$(pane_of_session "$SESSION")" = "$PANE" ] || fail "herdr shows the session on $(pane_of_session "$SESSION"), the run row says $PANE"
say "pane found by agent session ok"
wait_task 1 review 40
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
say "idle → review ok"

# T2: kill a worker with a child.
run 0 on home desk add -t "desk e2e: kill me" --desk --thread agent
set_mode 2 children
run 0 on home desk set T2 ready
wait_run 2 running 30
wait_file "$STUB/pids-run2"
wait_long 10 "the child's pid" test "$(wc -l <"$STUB/pids-run2" | tr -d ' ')" -ge 2
track_workspaces
PANE2=$(run_field 2 pane)
pids_alive "$STUB/pids-run2" || fail "the worker or its child is not alive"
run 0 on home desk runs kill T2
out_has "T2 blocked"
wait_long 10 "the worker and its child to be gone" pids_dead "$STUB/pids-run2"
say "kill: no process left ok"
if pane_ids | grep -Fx "$PANE2" >/dev/null; then fail "herdr still lists pane $PANE2"; fi
say "pane gone from herdr ok"
[ "$(focused_workspace)" = "$FOCUS" ] || fail "focus moved from '$FOCUS' to '$(focused_workspace)'"

# Close what this script opened, and check it is closed and that nothing it started is left.
track_workspaces
stop_daemon home
close_tracked_workspaces
[ -s "$E2E/workspaces.txt" ] || fail "no run row named a workspace"
while read -r id; do
  wait_long 10 "workspace $id to close" workspace_closed "$id"
done < <(sort -u "$E2E/workspaces.txt")
say "every workspace this test opened is closed ok"
wait_long 10 "every stub process to end" pids_dead "$STUB/pids-run1"
pids_dead "$STUB/pids-run2" || fail "a process of run 2 is left"
pass
