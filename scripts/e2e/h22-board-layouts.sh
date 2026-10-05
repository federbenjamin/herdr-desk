#!/usr/bin/env bash
# H22: under 78 columns one surface, from 110 the board beside the task.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home herdr-desk add -t "migrate the push tokens to the new table" -n "move the writers" --desk --status blocked
run 0 as_agent home sess-a herdr-desk note "two writers found, keep the devtools path?" --task 1
run 0 on home herdr-desk add -t "rank tickets by score" --desk --status started

term_start narrow 70 30 home "$BIN/herdr-desk"
term_wait narrow "migrate the push tokens"
[ "$(term_widest narrow)" -le 70 ] || fail "the board at 70 columns has a line of $(term_widest narrow)"
term_keys narrow Enter
term_wait narrow "HISTORY"
term_has narrow "NEEDS YOU" && fail "the board stays beside the task at 70 columns"
[ "$(term_widest narrow)" -le 70 ] || fail "the task page at 70 columns has a line of $(term_widest narrow)"
ok "at 70 columns no line is wider than 70 and the task page replaces the board"
term_keys narrow q

term_start mid 100 30 home "$BIN/herdr-desk"
term_wait mid "migrate the push tokens"
term_wait mid "ago"
term_has mid "HISTORY" && fail "the task page shows beside the board at 100 columns"
ok "at 100 columns the rows carry their detail"
term_keys mid q

term_start wide 120 30 home "$BIN/herdr-desk"
term_wait wide "NEEDS YOU"
term_wait wide "HISTORY"
term_has wide "migrate the push tokens" || fail "the row is gone at 120 columns"
ok "at 120 columns NEEDS YOU and HISTORY are on one screen"
term_keys wide q
pass
