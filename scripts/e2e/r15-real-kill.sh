#!/usr/bin/env bash
# H53 (runner): with the real herdr and the real worker: `runs kill` on a run whose claude session is mid-work (a
# chain of short sleeps) leaves no process in the run's folder, closes the pane, sets the run killed and the task
# blocked, and herdr's close event changes neither. This spends one worker run of the owner's quota. Run it in a
# separate named herdr session. A temp plugin, desk-e2e, carries the event hooks to this script's own desk and is
# unlinked at exit; only the workspaces a run row of this desk names are closed. A failure once the worker's pane
# exists prints the pane's screen first.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
unset DESK_HERDR
need_real_herdr
claude_guard
build
run 0 on home herdr-desk setup --profile claude-code --no-herdr --runner on
wrap_claude worker
home_event_command
link_event_plugin "${EVENT_CMD[@]}"

# procs_in <dir>: the pids of every process whose working folder is the dir.
procs_in() {
  lsof -n -d cwd -F pn 2>/dev/null | awk -v d="n$1" '/^p/ { p = substr($0, 2) } $0 == d { print p }' | sort -u
}
no_procs_in() { [ -z "$(procs_in "$1")" ]; }

run 0 on home herdr-desk add -t "desk e2e: stay busy" \
  -n "This is a test of stopping a busy worker. First create an empty file named started.txt in your working folder. Then run the shell command 'sleep 15' twenty times, as twenty separate commands, one after the other. Do nothing else. Only after all twenty, run the hand-back command from this message with --ref none, with the herdr-desk at $AGENT_BIN/herdr-desk in place of plain herdr-desk." --desk
run 0 on home herdr-desk run start T1
wait_run 1 running 120
track_workspaces
PANE=$(run_field 1 pane)
SHOW_PANE=$PANE
ROOT=$(run_field 1 root)
REAL_ROOT=$(cd "$ROOT" && pwd -P)
answer_trust_question "$PANE"
wait_long 180 "the worker to create started.txt" test -e "$ROOT/started.txt"
sleep 20
run_is 1 running || fail "run 1 is $(run_field 1 state) before the kill, not running"
BEFORE=$(procs_in "$REAL_ROOT")
[ -n "$BEFORE" ] || fail "no process has its working folder in $ROOT before the kill"
say "worker mid-work, $(printf '%s\n' "$BEFORE" | wc -l | tr -d ' ') processes in its folder ok"

run 0 on home herdr-desk runs kill T1
out_has "T1 blocked"
SHOW_PANE=""
END=$((SECONDS + 15))
until no_procs_in "$REAL_ROOT" || [ "$SECONDS" -ge "$END" ]; do sleep 0.5; done
no_procs_in "$REAL_ROOT" || fail "processes left in $ROOT: $(procs_in "$REAL_ROOT" | paste -sd, - | xargs -I{} ps -o pid=,command= -p {} 2>/dev/null)"
say "no process left ok"
herdr_do pane get "$PANE" >/dev/null 2>&1 && fail "pane $PANE is still open"
say "pane closed ok"
run_is 1 killed || fail "run 1 is $(run_field 1 state)"
task_is 1 blocked || fail "T1 is $(task_field 1 status)"
say "run killed, task blocked ok"
sleep 3
run_is 1 killed || fail "the close event changed run 1 to $(run_field 1 state)"
task_is 1 blocked || fail "the close event changed T1 to $(task_field 1 status)"
say "the close event changed nothing ok"
WORKERS=$(claude_calls worker)
if [ "$WORKERS" != 1 ]; then fail "claude ran $WORKERS times as the worker, not once"; fi
ok "real runs: 1 worker"

close_tracked_workspaces
while read -r id; do
  wait_long 20 "workspace $id to close" workspace_closed "$id"
done < <(sort -u "$E2E/workspaces.txt")
say "every workspace this test opened is closed ok"
pass
