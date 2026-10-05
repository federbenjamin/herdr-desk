#!/usr/bin/env bash
# H25 (runner): worktree isolation cuts <repo>-T<n> on desk/T<n>-<slug> beside the root, and a second `run start` uses
# it again.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
APP="$WORK/login-app"
PLAIN="$WORK/plain"
make_repo "$APP"
mkdir -p "$PLAIN"
add_root "$APP" worktree "a git repository"
add_root "$PLAIN" worktree "not a git repository"
RC_CAP=2
runner_up home

run 0 on home herdr-desk add -t "Fix the login page" --desk
run 0 on home herdr-desk run start T1 --root "$APP" --model sonnet
out_has "run 1  T1  running  $APP  worktree  sonnet"
wait_file "$STUB/worker-run1.env"
TREE="$WORK/login-app-T1"
[ -e "$TREE/.git" ] || fail "no worktree at $TREE"
git -C "$APP" worktree list --porcelain | grep -Fx "worktree $TREE" >/dev/null || fail "git does not list $TREE as a worktree of $APP"
say "worktree created ok"
BRANCH=desk/T1-fix-the-login-page
git -C "$APP" rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null || fail "no branch $BRANCH"
[ "$(git -C "$TREE" branch --show-current)" = "$BRANCH" ] || fail "the worktree is on $(git -C "$TREE" branch --show-current)"
say "branch $BRANCH ok"
[ "$(env_value "$STUB/worker-run1.env" PWD)" = "$TREE" ] || fail "the worker's cwd is $(env_value "$STUB/worker-run1.env" PWD), not $TREE"
say "worker cwd is the worktree ok"

# The worker hands the task back, which ends its run; a second run start uses the worktree and the branch as they are.
SESSION1=$(run_field 1 session)
run 0 as_agent home "$SESSION1" env DESK_RUN=1 herdr-desk set T1 review --ref e2e
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
run 0 on home herdr-desk run start T1 --root "$APP" --model sonnet
wait_run 2 running
wait_file "$STUB/worker-run2.env"
[ "$(env_value "$STUB/worker-run2.env" PWD)" = "$TREE" ] || fail "run 2's cwd is $(env_value "$STUB/worker-run2.env" PWD)"
[ "$(git -C "$APP" worktree list --porcelain | grep -c '^worktree ')" = 2 ] || fail "the second run made another worktree: $(git -C "$APP" worktree list)"
[ "$(git -C "$APP" branch --list 'desk/*' | wc -l | tr -d ' ')" = 1 ] || fail "the second run made another branch: $(git -C "$APP" branch --list 'desk/*')"
say "the second run reused the worktree ok"

# A worktree root that is not a git repository cannot be cut from: the start fails, exit 1 with the run's reason.
run 0 on home herdr-desk add -t "Nowhere to cut" --desk
run 1 on home herdr-desk run start T2 --root "$PLAIN" --model sonnet
err_has "run-failed: run 3 failed: spawn: "
wait_task 2 blocked
task_has_note 2 "spawn: " || fail "T2 has no 'spawn: ' note: $(task_notes 2)"
run_is 3 failed || fail "run 3 is $(run_field 3 state)"
[ ! -e "$WORK/plain-T2" ] || fail "a folder was made for a root that is not a repository"
say "not a git repo → blocked ok"
pass
