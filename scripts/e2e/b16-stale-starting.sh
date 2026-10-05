#!/usr/bin/env bash
# H35: a run left starting for over a minute is failed and its task blocked, and a task left started behind an ended run
# is blocked with a note. Both are what a process that stopped midway leaves; the rows are seeded with sqlite3 and the
# ticker's first tick repairs them.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
RC_CAP=3
runner_up home

# minutes_ago <n>: the time n minutes ago, as the store writes it.
minutes_ago() {
  python3 -c 'import datetime, sys; print((datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(minutes=int(sys.argv[1]))).strftime("%Y-%m-%dT%H:%M:%SZ"))' "$1"
}

run 0 on home herdr-desk add -t "left starting" --desk
run 0 on home herdr-desk add -t "left started" --desk

# T1: a run row that says starting since five minutes ago, and its task started.
run 0 on home herdr-desk set T1 started
sqlite3 "$DB" "INSERT INTO runs(task, state, root, isolation, model, started_ts)
  VALUES (1, 'starting', '$SCRATCH', 'self', 'sonnet', '$(minutes_ago 5)')"
[ "$(run_field 1 state)" = starting ] || fail "the seeded run 1 is '$(run_field 1 state)'"

# T2: a real run whose row then says ended five minutes ago, with the task still started and the pane gone.
run 0 on home herdr-desk run start T2 --isolation self
out_has "run 2  T2  running"
wait_note 2 "run 2: workspace"
herdr_do pane close "$(run_field 2 pane)" >/dev/null
sqlite3 "$DB" "UPDATE runs SET state = 'ended', started_ts = '$(minutes_ago 6)', ended_ts = '$(minutes_ago 5)' WHERE id = 2"
task_is 2 started || fail "T2 is $(task_field 2 status), not started"

ticker_up home
wait_run 1 failed 15
wait_task 1 blocked 15
task_has_note 1 "run 1 was left starting for over a minute" || fail "no 'left starting' note on T1: $(task_notes 1)"
ok "run 1 failed, T1 blocked"

wait_task 2 blocked 15
task_has_note 2 "run 2 is ended, but its task was left started" || fail "no 'left started' note on T2: $(task_notes 2)"
ok "T2 blocked with a note that it was left started"
pass
