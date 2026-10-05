#!/usr/bin/env bash
# H33: two in-place runs on one root never run at once, an idle one included: the second waits while the first runs,
# still waits while it is idle, and starts once it ends. The cap is high enough that only the root holds it back.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
with_events
INPLACE="$WORK/inplace"
mkdir -p "$INPLACE"
add_root "$INPLACE" in-place "runs one"
RC_CAP=3
runner_up home

run 0 on home herdr-desk add -t "first on the root" --desk
run 0 on home herdr-desk add -t "second on the root" --desk
run 0 on home herdr-desk run start T1 --root "$INPLACE" --model sonnet
wait_run 1 running
SESSION1=$(run_field 1 session)
PANE1=$(run_field 1 pane)
wait_long 10 "the stub worker to report its session" pane_has_session "$SESSION1"
run 0 on home herdr-desk run start T2 --root "$INPLACE" --model sonnet
out_has "run 2  T2  waiting"
sleep 1
run_is 2 waiting || fail "run 2 is $(run_field 2 state) while run 1 runs"
ok "run 2 waits while run 1 runs"

report "$PANE1" idle
wait_run 1 idle
# A tick of the ticker tries to start what waits; the idle run still holds the root.
ticker_up home
sleep 2
run_is 2 waiting || fail "run 2 is $(run_field 2 state) while run 1 is idle"
ok "run 2 still waits while run 1 is idle"
ticker_down home

run 0 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk set T1 review --ref e2e
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
wait_run 2 running
ok "run 2 starts once run 1 ends"
pass
