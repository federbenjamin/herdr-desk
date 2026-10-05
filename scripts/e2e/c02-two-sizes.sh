#!/usr/bin/env bash
# H45: the board at two sizes shows the same tasks, each laid out to its size, and a task added in one appears in the other
# within its refresh. A herdr popup runs the same `herdr-desk` command in a terminal of the size herdr gives it, so two tmux
# terminals of two sizes stand for the popup and the split.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
run 0 on home herdr-desk add -t "first task here" --desk --status started

term_start small 60 20 home "$BIN/herdr-desk"
term_start large 140 40 home "$BIN/herdr-desk"
term_wait small "first task here"
term_wait large "first task here"
term_has small "T1" || fail "the 60x20 board does not show T1"
term_has large "T1" || fail "the 140x40 board does not show T1"
ok "both show T1"

term_has small "HISTORY" && fail "the 60x20 board shows the task beside the board"
term_screen small | grep -qF "│" && fail "the 60x20 board has a second column"
term_wait large "HISTORY"
term_screen large | grep -qF "│" || fail "the 140x40 board has no column divider"
[ "$(term_widest small)" -le 60 ] || fail "the 60x20 board has a line of $(term_widest small)"
ok "the 60x20 board is one surface and the 140x40 board is two columns"

term_keys small +
term_wait small "add:"
term_type small "second task added small"
START=$(now)
term_keys small Enter
term_wait large "second task added small"
N=$(elapsed "$START")
under "$N" 4 || fail "T2 reached the large board $N s after the add, not under 4"
term_has large "T2" || fail "the large board does not show T2"
ok "T2 added in the small one shows in the large one within 4 s ($N s)"

term_start floor 70 6 home "$BIN/herdr-desk"
term_wait floor "needs 10 rows; this one has 6"
[ "$(term_screen floor | grep -c .)" = 1 ] || fail "the 70x6 board shows more than the one line"
ok "a 70x6 board shows the height-floor line"
pass
