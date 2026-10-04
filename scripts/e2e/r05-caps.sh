#!/usr/bin/env bash
# H5 (runner): the caps hold. The fourth task waits, the daily cap stops the next, a self root runs two at once, an
# in-place root runs one. The tasks carry their root and model, so the router is never called.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
SELF="$WORK/self"
INPLACE="$WORK/inplace"
mkdir -p "$SELF" "$INPLACE"
RC_CAP=3
add_root "$SELF" self "runs many"
add_root "$INPLACE" in-place "runs one"
runner_up home

# task_on <title> <root>: a task armed on that root, as a person. Its number is left in OUT.
task_on() {
  local root=$2 n
  run 0 on home desk add -t "$1" --desk --thread agent
  n=$OUT
  run 0 on home desk set "$n" --root "$root" --model sonnet
  run 0 on home desk set "$n" ready
  OUT=$n
}
live_count() { on home desk runs --json | jq 'length'; }
no_live_run() { [ "$(live_count)" = 0 ]; }

task_on one "$SELF"
task_on two "$SELF"
task_on three "$SELF"
task_on four "$SELF"
wait_run 1 running
wait_run 2 running
wait_run 3 running
sleep 2
task_is 4 ready || fail "T4 is $(task_field 4 status), not ready, with three runs live"
[ "$(live_count)" = 3 ] || fail "$(live_count) runs are live, not 3"
say "three running, fourth ready ok"
if [ "$(run_field 1 isolation)" != self ] || [ "$(run_field 2 isolation)" != self ]; then fail "the first two runs are not self runs"; fi
if [ "$(run_field 1 root)" != "$SELF" ] || [ "$(run_field 2 root)" != "$SELF" ]; then fail "the first two runs are not on one root"; fi
say "self root runs two ok"

run 0 on home desk set T1 review
wait_run 4 running
say "fourth starts when one ends ok"
run 0 on home desk set T2 review
run 0 on home desk set T3 review
run 0 on home desk set T4 review
wait_long 5 "no live run" no_live_run

# An in-place root runs one task at a time: the second routes, then waits.
task_on five "$INPLACE"
task_on six "$INPLACE"
wait_run 5 running
wait_run 6 waiting
task_is 6 started || fail "T6 is $(task_field 6 status) while its run waits"
say "in-place second waits ok"
run 0 on home desk set T5 review
wait_run 6 running
say "waiting run starts when the first ends ok"
run 0 on home desk set T6 review

# Six runs have started today: a daily cap of six stops the seventh.
RC_DAY=6
runner_config home
restart_daemon
task_on seven "$SELF"
sleep 3
task_is 7 ready || fail "T7 is $(task_field 7 status): the daily cap did not hold"
[ "$(on home desk runs --all --json | jq 'length')" = 6 ] || fail "a seventh run exists"
say "daily cap holds ok"
pass
