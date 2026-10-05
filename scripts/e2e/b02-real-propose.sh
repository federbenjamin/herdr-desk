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
ok "two tasks added by the coordinator"

# Give it time to finish proposing: no run may exist before a go-ahead.
sleep 30
[ "$(run_count)" = 0 ] || fail "$(run_count) runs exist before the go-ahead"
ok "no run before go"

FIRST=$(on home herdr-desk list --json | jq -r '.tasks[0].number')
SECOND=$(on home herdr-desk list --json | jq -r '.tasks[1].number')
tell_coordinator "go: start the runs for T$FIRST and T$SECOND"
wait_long 300 "the coordinator to start two runs" at_least_two_runs
ok "two runs after go"
end_real_coordinator
pass
