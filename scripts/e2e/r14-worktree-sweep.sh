#!/usr/bin/env bash
# H5 (runner): the ticker removes a done task's clean worktree, once it has closed the pane the worker handed back
# from, and leaves a dirty one with one note, which a later tick does not repeat. A run's first-message file going
# away marks a tick. This takes two to three minutes.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
APP="$WORK/app"
make_repo "$APP"
add_root "$APP" worktree "a git repository" "first_message = '/go {task_file}'"
RC_CAP=2
runner_up home
ticker_up home
RUNS="$E2E/home/state/herdr-desk/runs"

kept_notes() { task_notes 2 | grep -c "was not removed"; }
pane_listed() { jq -e --arg p "$1" '.panes | has($p)' "$E2E/herdr/state.json" >/dev/null; }

# start_back <task number>: add the task, start it on APP, and wait until its worker hands it back.
start_back() {
  run 0 on home herdr-desk add -t "task $1" --desk
  set_mode "$1" handback
  run 0 on home herdr-desk run start "T$1" --root "$APP"
  wait_run "$(sqlite3 "$DB" "SELECT max(id) FROM runs WHERE task = $1")" ended
  [ -d "$WORK/app-T$1" ] || fail "no worktree at $WORK/app-T$1"
}

start_back 1
PANE1=$(run_field 1 pane)
pane_listed "$PANE1" || fail "T1's worker pane $PANE1 closed at its hand-back"
start_back 2
printf 'not committed\n' >"$WORK/app-T2/untracked.txt"
run 0 on home herdr-desk set T1 "done"
run 0 on home herdr-desk set T2 "done"
start_back 3
wait_long 90 "run-3.md to be removed" gone "$RUNS/run-3.md"
wait_long 15 "T1's worktree to be removed" gone "$WORK/app-T1"
if git -C "$APP" worktree list --porcelain | grep -Fx "worktree $WORK/app-T1" >/dev/null; then fail "git still lists $WORK/app-T1"; fi
git -C "$APP" rev-parse --verify --quiet "refs/heads/desk/T1-task-1" >/dev/null || fail "the sweep removed T1's branch"
! pane_listed "$PANE1" || fail "T1's worktree was removed under its open pane $PANE1"
[ "$(run_field 1 left_open)" = 0 ] || fail "run 1 still owes its pane a close"
ok "clean worktree removed, after its worker's pane closed"
wait_note 2 "worktree $WORK/app-T2 was not removed: "
[ -f "$WORK/app-T2/untracked.txt" ] || fail "T2's untracked file is gone"
ok "dirty worktree kept with a note"

# One more tick: the first tick after T4 is done removes T4's clean worktree. T4 sorts after T2, so that tick has swept
# T2 too. A tick between T4's run ending and its done write removes only run-4.md, so the wait spans a whole tick.
start_back 4
run 0 on home herdr-desk set T4 "done"
wait_long 90 "T4's worktree to be removed" gone "$WORK/app-T4"
[ -f "$WORK/app-T2/untracked.txt" ] || fail "T2's worktree is gone after another tick"
[ "$(kept_notes)" = 1 ] || fail "T2 has $(kept_notes) notes holding 'was not removed': $(task_notes 2)"
ok "one note after another tick"
pass
