#!/usr/bin/env bash
# H17: bare desk on a terminal is the board and stays open; piped, it is the static text.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home desk add -t "seed a blocked task" --desk --status blocked
run 0 on home desk add -t "seed a started task" --desk --status started
run 0 on home desk add -t "seed a ready task" --desk --status ready

term_start board 100 30 home "$BIN/desk"
term_wait board "NEEDS YOU"
term_wait board "IN MOTION"
term_wait board "ON DECK"
ok "the screen shows NEEDS YOU, IN MOTION, ON DECK"

term_wait board "runner ○ off · home"
ok "the header shows runner ○ off · home"

sleep 2
term_alive board || fail "the board ended within 2 s"
ok "the board is still running after 2 s"

run 0 on home desk
first=$(head -n 1 <<<"$OUT")
[ "$first" = "desk · home · runner off" ] || fail "the piped first line is '$first'"
ok "piped, the first line is desk · home · runner off"

term_keys board q
code=$(term_wait_exit board)
[ "$code" = 0 ] || fail "q ended the board with exit $code"
ok "q ends it with exit 0"
pass
