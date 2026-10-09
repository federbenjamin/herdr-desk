#!/usr/bin/env bash
# H32: an idle worker sets its task review, resuming returns it to started, a pane that blocks with no session sets the
# task blocked, and `run start` on an idle run ends it and starts a new one. An idle run is live: done ends it, the
# worker's own review ends it and frees its in-place root, a person's blocked leaves a running run alone, and a
# worker's blocked with a question keeps its run, stays blocked past the turn's end, and starts on an answer. Each
# status change is told to the fake herdr, which fires the event hook as herdr does.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
with_events
SELF="$WORK/self"
INPLACE="$WORK/inplace"
mkdir -p "$SELF" "$INPLACE"
add_root "$SELF" self "runs many"
add_root "$INPLACE" in-place "runs one"
RC_CAP=4
runner_up home

# clear_session <pane>: herdr shows the pane with no agent session, as before claude's session begins.
clear_session() {
  python3 - "$E2E/herdr" "$1" <<'PY'
import fcntl, json, os, sys

d, pane = sys.argv[1:3]
with open(os.path.join(d, "lock"), "w") as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    path = os.path.join(d, "state.json")
    st = json.load(open(path))
    st["panes"][pane]["session"] = None
    json.dump(st, open(path, "w"))
PY
}

# T1: idle → review, working → started again, blocked with no session → blocked.
run 0 on home herdr-desk add -t "goes idle" --desk
run 0 on home herdr-desk run start T1 --root "$SELF" --model sonnet
wait_run 1 running
SESSION1=$(run_field 1 session)
PANE1=$(run_field 1 pane)
wait_long 10 "the stub worker to report its session" pane_has_session "$SESSION1"
STARTED=$(run_field 1 started_ts)

report "$PANE1" idle
wait_run 1 idle
wait_task 1 review
task_has_note 1 "went idle without handing back" || fail "no 'went idle without handing back' note: $(task_notes 1)"
ok "idle: run 1 idle, T1 review, note went idle without handing back"

report "$PANE1" working
wait_run 1 running
wait_task 1 started
[ "$(run_field 1 started_ts)" = "$STARTED" ] || fail "started_ts moved from $STARTED to $(run_field 1 started_ts)"
ok "working: run 1 running, T1 started, started_ts unchanged"

clear_session "$PANE1"
report "$PANE1" blocked
wait_run 1 idle
wait_task 1 blocked
task_has_note 1 "pane $PANE1" || fail "no note names pane $PANE1: $(task_notes 1)"
ok "blocked with no session: run idle, T1 blocked, note names the pane"

run 0 on home herdr-desk run start T1 --root "$SELF" --model sonnet
out_has "run 2  T1  running"
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
run_is 2 running || fail "run 2 is $(run_field 2 state)"
ok "run start on the idle run: run 1 ended, run 2 running"

# done on an idle run ends it.
SESSION2=$(run_field 2 session)
PANE2=$(run_field 2 pane)
wait_long 10 "the second worker to report its session" pane_has_session "$SESSION2"
report "$PANE2" idle
wait_run 2 idle
run 0 on home herdr-desk set T1 "done"
run_is 2 ended || fail "run 2 is $(run_field 2 state) after done"
ok "done on an idle run ends it"

# T2 holds an in-place root while idle; T3 waits for it; the worker's own review ends the idle run and T3 starts.
run 0 on home herdr-desk add -t "holds the root" --desk
run 0 on home herdr-desk run start T2 --root "$INPLACE" --model sonnet
wait_run 3 running
SESSION3=$(run_field 3 session)
PANE3=$(run_field 3 pane)
wait_long 10 "the third worker to report its session" pane_has_session "$SESSION3"
report "$PANE3" idle
wait_run 3 idle
run 0 on home herdr-desk add -t "waits for the root" --desk
run 0 on home herdr-desk run start T3 --root "$INPLACE" --model sonnet
out_has "run 4  T3  waiting"
run 0 as_agent home "$SESSION3" env DESK_RUN=3 herdr-desk set T2 review --ref e2e
run_is 3 ended || fail "run 3 is $(run_field 3 state) after its worker's review"
wait_run 4 running
ok "the worker's own set review from an idle run ends it and frees the in-place root"

# A person's blocked on a started task leaves its run running.
run 0 on home herdr-desk add -t "a person blocks it" --desk
run 0 on home herdr-desk run start T4 --root "$SELF" --model sonnet
wait_run 5 running
run 0 on home herdr-desk set T4 blocked
sleep 1
run_is 5 running || fail "a person's blocked changed run 5 to $(run_field 5 state)"
task_is 4 blocked || fail "T4 is $(task_field 4 status)"
ok "a person's blocked on a started task leaves the run running"

# A worker's question blocks its task and keeps its run; the turn's end keeps the task blocked; working starts it.
run 0 on home herdr-desk add -t "asks in text" --desk
run 0 on home herdr-desk run start T5 --root "$SELF" --model sonnet
wait_run 6 running
SESSION6=$(run_field 6 session)
PANE6=$(run_field 6 pane)
wait_long 10 "the sixth worker to report its session" pane_has_session "$SESSION6"
run 0 as_agent home "$SESSION6" env DESK_RUN=6 herdr-desk set T5 blocked --question "which address?"
run_is 6 running || fail "the worker's question changed run 6 to $(run_field 6 state)"
task_is 5 blocked || fail "T5 is $(task_field 5 status)"
task_has_note 5 "pane $PANE6: which address?" || fail "no note names pane $PANE6 and the question: $(task_notes 5)"
ok "a worker's question: T5 blocked, note names the pane, run 6 still running"
report "$PANE6" "done"
wait_run 6 idle
task_is 5 blocked || fail "the turn's end moved T5 to $(task_field 5 status)"
! task_has_note 5 "went idle without handing back" || fail "the turn's end noted went idle: $(task_notes 5)"
ok "the turn's end: run 6 idle, T5 still blocked, no went-idle note"
report "$PANE6" working
wait_run 6 running
wait_task 5 started
ok "an answer in the pane: run 6 running, T5 started"
pass
