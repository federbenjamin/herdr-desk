#!/usr/bin/env bash
# H20: the task page shows notes, steps, history, and files, and its keys edit the task.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home herdr-desk add -t "page under test" -n "the first notes" --desk
run 0 on home herdr-desk steps 1 add "find every writer"
run 0 on home herdr-desk steps 1 add "switch the writers"
run 0 on home herdr-desk steps 1 toggle s1
run 0 as_agent home sess-a herdr-desk note "nine writers found" --task 1 --ref docs/writers.md
run 0 as_agent home sess-a herdr-desk decide "one worktree per group" --task 1

term_start board 100 40 home "$BIN/herdr-desk"
term_wait board "page under test"
term_keys board Enter
term_wait board "NOTES"
term_wait board "STEPS  1/2"
term_wait board "HISTORY"
term_wait board "FILES"
ok "enter shows NOTES, STEPS 1/2, HISTORY, FILES"

term_screen board | grep -Eq 'you +created' || fail "the history has no line by you"
term_screen board | grep -Eq 'agent +note +"nine writers found"' || fail "the history has no note by agent"
term_screen board | grep -Eq 'agent +decision +one worktree per group' || fail "the history has no decision by agent"
ok "the history names you and agent and holds the note and the decision"

term_keys board e
term_type board " and the edited ones"
term_keys board C-s
wait_task home 1 .task.notes "the first notes and the edited ones"
ok "e then ctrl+s replaces the notes"

term_keys board t
term_keys board Space
wait_task home 1 '.task.steps[0].done' false
ok "t then space toggles a step"

term_esc board
term_keys board t
term_keys board a
term_wait board "step:"
term_type board "check the result"
term_keys board Enter
wait_task home 1 '.task.steps | length' 3
wait_task home 1 '.task.steps[2].text' "check the result"
ok "t then a adds a step"
term_esc board

term_keys board R
term_wait board "root:"
term_type board "example-root"
term_keys board Enter
wait_task home 1 .task.root example-root
ok "R sets the root"

term_keys board I
wait_task home 1 .task.isolation self
ok "I sets the isolation"

term_keys board M
term_wait board "model:"
term_type board "example-model"
term_keys board Enter
wait_task home 1 .task.model example-model
ok "M sets the model"

term_esc board
term_wait_gone board "NOTES"
term_wait board "ON DECK"
ok "esc returns to the board"

term_keys board q
pass
