#!/usr/bin/env bash
# H3 (runner): a worker that stops, asks, closes, or hands back moves its task, and its run ends. Five tasks run at
# once, each with its own stub mode.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
# Every task runs on one self root with its fields set, so the router is never called.
ROOT="$WORK/a"
mkdir -p "$ROOT"
RC_CAP=6
add_root "$ROOT" self "watch root"
runner_up home

start_task() {
  local n=$1 mode=$2
  run 0 on home desk add -t "watched $mode" --desk --thread agent
  [ "$OUT" = "T$n" ] || fail "expected T$n, got $OUT"
  run 0 on home desk set "T$n" --root "$ROOT" --model sonnet
  set_mode "$n" "$mode"
  run 0 on home desk set "T$n" ready
}
start_task 1 idle
start_task 2 blocked
start_task 3 exit
start_task 4 handback
start_task 5 busy

wait_task 1 review
task_has_note 1 "session ended without reporting" || fail "T1 has no 'session ended without reporting' note"
wait_run 1 ended
# Two polls, no more: the fake logs every call in the order it ran them, so the pane lists between T1's idle report
# and the review the runner wrote are the polls it took.
REVIEW_TS=$(on home desk show T1 --json | jq -r '[.history[] | select(.kind == "set" and .data.status == "review")][0].ts')
POLLS=$(python3 - "$E2E/herdr/calls.log" "$(run_field 1 pane)" "$REVIEW_TS" <<'PY'
import datetime, re, sys
log, pane, review = sys.argv[1:4]
m = re.match(r"(.*T\d\d:\d\d:\d\d)(\.\d+)?(Z|[+-]\d\d:\d\d)$", review)
frac = (m.group(2) or ".0")[:7]
cut = datetime.datetime.fromisoformat(m.group(1) + frac + m.group(3).replace("Z", "+00:00")).timestamp()
seen, polls = False, 0
for line in open(log):
    ts, args = line.rstrip("\n").split("\t", 1)
    if args.startswith("pane report-agent " + pane + " ") and "--state idle" in args:
        seen, polls = True, 0
    elif seen and args == "pane list" and float(ts) < cut:
        polls += 1
print(polls if seen else "no idle report")
PY
)
[ "$POLLS" = 2 ] || fail "T1 went to review after $POLLS polls of its idle pane, not 2"
say "idle two polls → review ok"
say "session ended without reporting ok"

wait_task 2 blocked
wait_run 2 ended
say "blocked → blocked ok"

wait_task 3 review
task_has_note 3 "the pane closed without a hand-back" || fail "T3 has no 'the pane closed without a hand-back' note"
wait_run 3 ended
say "pane gone → review ok"

wait_task 4 review
wait_run 4 ended
task_has_note 4 "session ended without reporting" && fail "T4 handed back, yet the runner wrote 'session ended without reporting'"
say "worker hand-back → review, run ended ok"

# One idle poll changes nothing: T5 reports idle for less than one poll, three times, with working between. Ticks are
# at least a second apart, so no window holds two.
wait_run 5 running
wait_file "$STUB/worker-run5.env"
PANE=$(run_field 5 pane)
SESSION=$(run_field 5 session)
report() {
  herdr_do pane report-agent "$PANE" --source desk-e2e --agent stub --state "$1" --agent-session-id "$SESSION" >/dev/null
}
for _ in 1 2 3; do
  report idle
  sleep 0.8
  report working
  sleep 1.3
done
task_is 5 started || fail "one idle poll changed T5 to $(task_field 5 status)"
run_is 5 running || fail "one idle poll ended run 5"
say "one idle poll changes nothing ok"
pass
