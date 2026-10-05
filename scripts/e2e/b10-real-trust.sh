#!/usr/bin/env bash
# H37: on the real herdr, a worktree run on a root claude trusts starts with no question, and one on a root it has not
# trusted stops at claude's trust question: herdr's event sets the task blocked with a note that names the pane.
# herdr-desk never answers the question. The trusted root is this checkout, or E2E_TRUSTED_ROOT, a git repository the
# claude on this machine has trusted; its worktree is cut beside it and removed at exit with its branch. The untrusted
# root is a new repository in the temp folder. This spends one real worker session of the owner's quota (the other
# claude stops at the question). Run it in a separate named herdr session (HERDR_SOCKET_PATH). A temp plugin,
# desk-e2e, carries the event hooks to this script's own desk and is unlinked at exit; only the workspaces a run row
# of this desk names are closed.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of those that want it open.
unset DESK_HERDR
need_real_herdr
command -v claude >/dev/null 2>&1 || fail "(env) no claude"
TRUSTED=${E2E_TRUSTED_ROOT:-$REPO}
TRUSTED=$(cd "$TRUSTED" && git rev-parse --show-toplevel 2>/dev/null) || fail "(env) $TRUSTED is not a git repository"
TRUSTED=$(cd "$TRUSTED" && pwd -P)
TREE="$(dirname "$TRUSTED")/$(basename "$TRUSTED")-T1"
[ ! -e "$TREE" ] || fail "(env) $TREE exists: remove that worktree first"
TITLE="desk e2e trust $$"
BRANCH="desk/T1-desk-e2e-trust-$$"

# The worktree and branch this script cuts are the only things it removes from the trusted repository.
b10_cleanup() {
  if [ -e "$TREE" ]; then git -C "$TRUSTED" worktree remove --force "$TREE" >/dev/null 2>&1 || true; fi
  if git -C "$TRUSTED" rev-parse --verify --quiet "refs/heads/$BRANCH" >/dev/null 2>&1; then
    git -C "$TRUSTED" branch -D "$BRANCH" >/dev/null 2>&1 || true
  fi
  runner_cleanup
}
trap 'b10_cleanup' EXIT

build
UNTRUSTED="$WORK/untrusted"
make_repo "$UNTRUSTED"
run 0 on home herdr-desk setup --profile claude-code --no-herdr --runner on
run 0 on home herdr-desk roots add "$TRUSTED" --isolation worktree --about "a root claude trusts"
run 0 on home herdr-desk roots add "$UNTRUSTED" --isolation worktree --about "a root claude has not seen"
wrap_claude worker
home_event_command
link_event_plugin "${EVENT_CMD[@]}"
NOTE="Do nothing else: run the hand-back command from this message with --ref none, then stop. Run it with the herdr-desk binary at $BIN/herdr-desk in place of plain herdr-desk: another herdr-desk may be installed on this machine."

# T1: the trusted root.
run 0 on home herdr-desk add -t "$TITLE" -n "$NOTE" --desk
run 0 on home herdr-desk run start T1 --root "$TRUSTED" --model sonnet
wait_run 1 running 60
track_workspaces
PANE=$(run_field 1 pane)
SESSION=$(run_field 1 session)
SHOW_PANE=$PANE
wait_long 180 "herdr to show the agent session $SESSION" pane_has_session "$SESSION"
if pane_shows "$PANE" "trust this folder"; then fail "claude asked to trust $TREE"; fi
task_is 1 blocked && fail "T1 is blocked: claude stopped before its session began"
ok "trusted root: the session began with no question"
SHOW_PANE=""
if run_is 1 running || run_is 1 idle; then run 0 on home herdr-desk runs kill T1; fi

# T2: a root claude has never seen. The worker stops at the question; herdr-desk does not answer it.
run 0 on home herdr-desk add -t "desk e2e untrusted root" --desk
run 0 on home herdr-desk run start T2 --root "$UNTRUSTED" --model sonnet
wait_run 2 running 60
track_workspaces
PANE2=$(run_field 2 pane)
SHOW_PANE=$PANE2
wait_task 2 blocked 90
task_has_note 2 "the worker is waiting for an answer in pane $PANE2" ||
  fail "T2 has no runner note naming pane $PANE2: $(task_notes 2)"
pane_shows "$PANE2" "trust this folder" || fail "pane $PANE2 does not show the trust question"
ok "untrusted root: T2 blocked, note names pane $PANE2"
SHOW_PANE=""
run 0 on home herdr-desk runs kill T2

[ "$(claude_calls worker)" -le 2 ] || fail "claude ran $(claude_calls worker) times as the worker, more than the two this script starts"
track_workspaces
close_tracked_workspaces
while read -r id; do
  wait_long 20 "workspace $id to close" workspace_closed "$id"
done < <(sort -u "$E2E/workspaces.txt")
say "every workspace this test opened is closed ok"
pass
