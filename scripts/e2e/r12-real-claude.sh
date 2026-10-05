#!/usr/bin/env bash
# H39 (runner): with the real herdr and the real worker: a task with no project runs on the scratch root, the worker
# stops at claude's trust question (a folder claude has not seen) and herdr's event sets the run idle and the task
# blocked, and once the question is answered in the pane, as a person would, the worker resumes and hands the task
# back. This spends one worker run of the owner's quota. Run it in a separate named herdr session. A temp plugin,
# desk-e2e, carries the event hooks to this script's own desk and is unlinked at exit; only the workspaces a run row of
# this desk names are closed. A failure once the worker's pane exists prints the pane's screen first.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of those that want it open.
unset DESK_HERDR
need_real_herdr
claude_guard
build
run 0 on home herdr-desk setup --profile claude-code --no-herdr --runner on
wrap_claude worker
home_event_command
link_event_plugin "${EVENT_CMD[@]}"

run 0 on home herdr-desk add -t "desk e2e: hand this task back" \
  -n "Do nothing else: run the hand-back command from this message with --ref none, then stop. Run it with the herdr-desk at $AGENT_BIN/herdr-desk in place of plain herdr-desk: another herdr-desk may be installed on this machine." --desk
run 0 on home herdr-desk run start T1
wait_run 1 running 120
track_workspaces
PANE=$(run_field 1 pane)
SHOW_PANE=$PANE
[ "$(run_field 1 root)" = "$SCRATCH" ] || fail "run 1 is on '$(run_field 1 root)', not $SCRATCH"
say "the scratch root ok"

# herdr shows the pane blocked, with no agent session, while claude asks whether to trust the folder.
wait_task 1 blocked 60
run_is 1 idle || fail "run 1 is $(run_field 1 state)"
task_has_note 1 "the worker is waiting for an answer in pane $PANE" ||
  fail "T1 has no runner note that its worker is waiting in pane $PANE: $(task_notes 1)"
say "trust question → run idle, task blocked ok"

# The answered question starts the session: herdr's working event returns the run to running, and the worker's own
# hand-back (set T1 review, from its run) ends it.
answer_trust_question "$PANE"
wait_task 1 review 300
track_workspaces
SESSION=$(run_field 1 session)
pane_has_session "$SESSION" || fail "herdr shows no pane with the agent session $SESSION"
say "worker session known to herdr ok"
say "T1 $(task_field 1 status)"
[ "$(sqlite3 "$DB" "SELECT count(*) FROM runs")" = 1 ] || fail "more than one run was started"
WORKERS=$(claude_calls worker)
if [ "$WORKERS" != 1 ]; then fail "claude ran $WORKERS times as the worker, not once"; fi
ok "real runs: 1 worker"

SHOW_PANE=""
close_tracked_workspaces
while read -r id; do
  wait_long 20 "workspace $id to close" workspace_closed "$id"
done < <(sort -u "$E2E/workspaces.txt")
say "every workspace this test opened is closed ok"
pass
