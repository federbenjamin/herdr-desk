#!/usr/bin/env bash
# H18: every status key on the board page writes what the mockup says.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home desk add -t "alpha walks every status" --desk
run 0 on home desk add -t "bravo asks before done" --desk
run 0 on home desk add -t "charlie is spared" --desk
run 0 on home desk add -t "delta gets a thread" --desk --status ready
run 0 on home desk add -t "echo needs an answer" --desk --status blocked

term_start board 100 30 home "$BIN/desk"
term_wait board "alpha walks every status"

term_keys board +
term_wait board "add:"
term_type board "foxtrot lands #tour"
term_keys board Enter
wait_task home 6 .task.thread tour
wait_task home 6 .task.title "foxtrot lands"
ok "+ adds a task with its #thread"
term_wait board "foxtrot lands"

board_pick board 1
term_keys board n
wait_task home 1 .task.status ready
ok "n sets ready"
term_keys board s
wait_task home 1 .task.status started
ok "s sets started"
term_keys board b
wait_task home 1 .task.status blocked
ok "b sets blocked"
term_keys board r
wait_task home 1 .task.status review
ok "r sets review"
term_keys board x
wait_task home 1 .task.status "done"
term_has board "done? y/n" && fail "x on a review task asked"
ok "x on review sets done and asks nothing"
board_unpick board

board_pick board 2
term_keys board x
term_wait board "mark T2 done? y/n"
[ "$(task_field home 2 .task.status)" = open ] || fail "T2 changed before y"
term_keys board y
wait_task home 2 .task.status "done"
ok "x on open asks, and y sets done"
board_unpick board

board_pick board 3
term_keys board x
term_wait board "mark T3 done? y/n"
term_keys board z
term_wait_gone board "done? y/n"
sleep 1
[ "$(task_field home 3 .task.status)" = open ] || fail "T3 changed after another key"
ok "x then another key leaves the task as it was"
board_unpick board

board_pick board 4
term_keys board a
wait_task home 4 .task.thread agent
term_wait board "#agent · queued"
ok "a sets the thread agent and the row shows #agent · queued"
board_unpick board

board_pick board 5
term_keys board n
term_wait board "answer:"
term_type board "use the new path"
term_keys board Enter
wait_task home 5 .task.status ready
wait_task home 5 '[.history[] | select(.kind == "note") | .data.text] | first' "use the new path"
ok "n on a blocked task asks for the answer, writes the note, and sets ready"
board_unpick board

# The ages tick, so they are cut before the screens are compared.
steady() { term_screen board | sed -E 's/[0-9]+[smhd] ago//'; }
before=$(steady)
term_keys board C-d
sleep 1
term_alive board || fail "ctrl+d ended the board"
after=$(steady)
[ "$after" = "$before" ] || fail "ctrl+d changed the screen: $(diff <(echo "$before") <(echo "$after"))"
ok "ctrl+d changes nothing and the board is still running"

term_keys board q
pass
