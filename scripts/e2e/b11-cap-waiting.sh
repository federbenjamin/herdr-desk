#!/usr/bin/env bash
# H20: with cap = 2 a third run is waiting, and it starts within two seconds of one ending: the process that ends a run
# starts what waits, so the ticker's minute is not the wait.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
SELF="$WORK/self"
mkdir -p "$SELF"
add_root "$SELF" self "runs many"
RC_CAP=2
runner_up home

for n in 1 2 3; do
  run 0 on home herdr-desk add -t "task $n" --desk
  run 0 on home herdr-desk run start "T$n" --root "$SELF" --model sonnet
done
wait_run 1 running
wait_run 2 running
run_is 3 waiting || fail "run 3 is $(run_field 3 state), not waiting"
task_is 3 started || fail "T3 is $(task_field 3 status) while its run waits"
[ -z "$(run_field 3 pane)" ] || fail "a waiting run has the pane $(run_field 3 pane)"
ok "run 3 is waiting"

SESSION1=$(run_field 1 session)
T0=$(now)
run 0 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk set T1 review --ref e2e
wait_long 2 "run 3 to be running within 2 s" run_is 3 running
N=$(elapsed "$T0")
under "$N" 2 || fail "run 3 was running $N s after run 1's hand-back, not under 2"
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
wait_file "$STUB/worker-run3.env"
ok "run 3 is running $N s after run 1's hand-back (under 2)"
pass
