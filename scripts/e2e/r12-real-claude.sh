#!/usr/bin/env bash
# H12 (runner): with the real herdr, the real router, and the real worker: a task with no project routes to the
# scratch root and the worker hands it back. This spends one router run and one worker run of the owner's quota.
# Only the workspaces a run row of this desk names are closed.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
need_real_herdr
command -v claude >/dev/null 2>&1 || fail "no claude"
build
run 0 on home desk setup --profile claude-code --no-herdr --runner on
start_daemon home

run 0 on home desk add -t "desk e2e: hand this task back" \
  -n "Do nothing else: run the hand-back command from this message with --ref none, then stop." --desk --thread agent
run 0 on home desk set T1 ready
wait_run 1 running 120
track_workspaces
[ "$(run_field 1 root)" = "$SCRATCH" ] || fail "run 1 routed to '$(run_field 1 root)', not $SCRATCH"
say "routed to the scratch root ok"

wait_task 1 review 300
track_workspaces
SESSION=$(run_field 1 session)
pane_has_session "$SESSION" || fail "herdr shows no pane with the agent session $SESSION"
say "worker session known to herdr ok"
say "T1 $(task_field 1 status)"
[ "$(on home desk runs --all --json | jq 'length')" = 1 ] || fail "more than one run was started"
say "real runs: 1 router, 1 worker"

stop_daemon home
close_tracked_workspaces
while read -r id; do
  wait_long 20 "workspace $id to close" workspace_closed "$id"
done < <(sort -u "$E2E/workspaces.txt")
say "every workspace this test opened is closed ok"
pass
