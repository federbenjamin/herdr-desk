#!/usr/bin/env bash
# H54 (runner): with the real herdr and the real worker: a running claude session that asks the person a question
# (AskUserQuestion) makes herdr report its pane blocked, the run idle and the task blocked with a note naming the
# pane; once the person picks an answer in the pane, the run is running again and the worker writes the answer to a
# file and hands the task back. This spends one worker run of the owner's quota. Run it in a separate named herdr
# session. A temp plugin, desk-e2e, carries the event hooks to this script's own desk and is unlinked at exit; only
# the workspaces a run row of this desk names are closed. A failure once the worker's pane exists prints the pane's
# screen first.
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

WAITING="the worker is waiting for an answer in pane"
waiting_notes() { task_notes 1 | grep -cF -- "$WAITING" || true; }
STATUS=""
question_tracked() { [ "$STATUS" = blocked ] && task_is 1 blocked && run_is 1 idle && [ "$(waiting_notes)" -ge 1 ]; }
run_not_idle() { ! run_is 1 idle; }

run 0 on home herdr-desk add -t "desk e2e: ask the person" \
  -n "This is a test of asking the person a question. Before anything else, call the AskUserQuestion tool with exactly one question, 'Which colour goes in the file?', header 'Colour', and exactly two options, 'teal' and 'amber'. Then write the chosen option, and nothing else, to a file named answer.txt in your working folder. Then run the hand-back command from this message with --ref answer.txt, with the herdr-desk at $AGENT_BIN/herdr-desk in place of plain herdr-desk. Do nothing else." --desk
run 0 on home herdr-desk run start T1
wait_run 1 running 120
track_workspaces
PANE=$(run_field 1 pane)
SHOW_PANE=$PANE
ROOT=$(run_field 1 root)
answer_trust_question "$PANE"

# herdr reports the pane blocked while the question is on screen, and the desk tracks it. claude's trust question
# before it leaves no waiting note today (herdr shows that pane unknown), so the note count is not a sign of which.
herdr_do pane wait-output "$PANE" --match "Which colour goes in the file" --source visible --timeout 180000 >/dev/null 2>&1 || true
pane_shows "$PANE" "Which colour goes in the file" || fail "the worker's question is not on pane $PANE's screen"
say "the worker asked its question ok"
# A question herdr does not report as blocked is the claim's failure, but the script goes on to answer it, so one run
# shows what the desk does with the rest; the failure is raised at the end.
GAP=""
SEEN=""
END=$((SECONDS + 30))
until question_tracked || [ "$SECONDS" -ge "$END" ]; do
  STATUS=$(herdr_do pane get "$PANE" 2>/dev/null | jq -r '.result.pane.agent_status // ""')
  case " $SEEN " in *" $STATUS "*) ;; *) SEEN="$SEEN $STATUS" ;; esac
  sleep 1
done
if question_tracked; then
  say "worker question → run idle, task blocked, note naming pane $PANE ok"
else
  GAP="30 s after the worker's question was on screen, herdr reported pane $PANE as:$SEEN; run 1 $(run_field 1 state), T1 $(task_field 1 status), $(waiting_notes) waiting notes"
  say "GAP: $GAP" >&2
  task_notes 1 >&2
fi

# A person picks the second option in the pane.
sleep 2
herdr_do pane send-keys "$PANE" Down >/dev/null || fail "could not send Down to pane $PANE"
sleep 1
herdr_do pane send-keys "$PANE" Enter >/dev/null || fail "could not send Enter to pane $PANE"
sleep 2
if pane_shows "$PANE" "Submit answers"; then
  herdr_do pane send-keys "$PANE" Enter >/dev/null || fail "could not send Enter to pane $PANE"
fi
[ -n "$GAP" ] || wait_long 30 "run 1 to be running again or ended" run_not_idle
say "answered in the pane → run $(run_field 1 state) ok"
wait_task 1 review 300
[ "$(tr -d '[:space:]' <"$ROOT/answer.txt" 2>/dev/null)" = amber ] ||
  fail "answer.txt holds '$(cat "$ROOT/answer.txt" 2>/dev/null)', not amber"
say "answer.txt holds the answer ok"
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
[ -z "$GAP" ] || fail "$GAP"
pass
