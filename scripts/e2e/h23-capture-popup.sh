#!/usr/bin/env bash
# H23: the capture popup lands a task, and shows a refused line and keeps it.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build

term_start landed 80 10 home "$BIN/herdr-desk" capture
term_wait landed "capture:"
term_type landed "popup lands a task #tour"
term_keys landed Enter
code=$(term_wait_exit landed)
[ "$code" = 0 ] || fail "the popup ended with exit $code"
wait_task home 1 .task.thread tour
wait_task home 1 .task.title "popup lands a task"
term_has landed "T1" || fail "the popup did not print T1"
ok "a line with #tour lands a task with the thread tour and the popup ends"

term_start refused 80 10 home "$BIN/herdr-desk" capture
term_wait refused "capture:"
term_type refused "refused line @nosuch"
term_keys refused Enter
term_wait refused "unknown-project"
term_alive refused || fail "the popup ended on a refused line"
term_has refused "refused line @nosuch" || fail "the popup lost the refused line"
ok "a line with @nosuch shows unknown-project and the popup is still open with the line"

term_esc refused
code=$(term_wait_exit refused)
[ "$code" = 0 ] || fail "esc ended the popup with exit $code"
run 0 on home herdr-desk list --json
[ "$(jq '.tasks | length' <<<"$OUT")" = 1 ] || fail "a task landed from the refused line or from esc"
ok "esc ends it with no task"
pass
