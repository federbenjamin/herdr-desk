#!/usr/bin/env bash
# H43: with start_runs = "propose", a real coordinator session given "fix X and update Y" adds two tasks and starts no
# run until a message that names them says go; then two runs start. The workers are the stub. This spends one real
# coordinator session of the owner's quota. Run it in a separate named herdr session (HERDR_SOCKET_PATH). Only the
# workspaces a run row or the coordinator of this desk names are closed. A failure once the coordinator's pane exists
# prints the pane's screen first.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of those that want it open.
unset DESK_HERDR
real_coordinator_up propose
coordinator_open

tell_coordinator "fix X and update Y"
wait_long 300 "the coordinator to add two tasks" at_least_two_tasks
# No run may start in the whole first turn, so the check waits for herdr to show the turn over.
wait_long 300 "the coordinator to finish its first turn" coordinator_turn_done
xy_tasks || fail "the tasks are not one for X and one for Y: $(tasks_seen)"
ok "two tasks added by the coordinator, one for X and one for Y"
[ "$(run_count)" = 0 ] || fail "$(run_count) runs exist before the go-ahead"
ok "no run in the coordinator's first turn"

tell_coordinator "go: start the runs for T$X_TASK and T$Y_TASK"
wait_long 300 "the coordinator to start two runs" at_least_two_runs
wait_long 300 "the coordinator to finish its second turn" coordinator_turn_done
one_run_each || fail "the runs are not one for T$X_TASK and one for T$Y_TASK: $(sqlite3 "$DB" "SELECT id, task FROM runs")"
xy_tasks || fail "the tasks changed after the go-ahead: $(tasks_seen)"
ok "one run for each task after go"
end_real_coordinator
pass
