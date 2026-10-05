#!/usr/bin/env bash
# H44: with start_runs = "auto", a real coordinator session given "fix X and update Y" starts both runs unasked. The
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

tell_coordinator "fix X and update Y"
wait_long 300 "the coordinator to start two runs" at_least_two_runs
[ "$(task_count)" -ge 2 ] || fail "two runs exist, but only $(task_count) tasks"
ok "two runs with no go"
end_real_coordinator
pass
