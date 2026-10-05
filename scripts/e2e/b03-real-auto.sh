#!/usr/bin/env bash
# H44: with start_runs = "auto", a real coordinator session given two real jobs on two files this script made in its
# temp root ("fix the typo in <root>/a.txt and add a second line to <root>/b.txt") starts both runs unasked. The
# workers are the stub. This spends one real coordinator session of the owner's quota. Run it in a separate named herdr
# session (HERDR_SOCKET_PATH). Only the workspaces a run row or the coordinator of this desk names are closed. A
# failure once the coordinator's pane exists prints the pane's screen first.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of those that want it open.
unset DESK_HERDR
real_coordinator_up auto
coordinator_open

tell_coordinator "$JOBS"
wait_long 300 "the coordinator to start two runs" at_least_two_runs
wait_long 300 "the coordinator to finish its turn" coordinator_turn_done
ab_tasks || fail "the tasks are not one for a.txt and one for b.txt: $(tasks_seen)"
one_run_each || fail "the runs are not one for T$A_TASK and one for T$B_TASK: $(sqlite3 "$DB" "SELECT id, task FROM runs")"
ok "one task and one run for each of a.txt and b.txt, with no go"
end_real_coordinator
pass
