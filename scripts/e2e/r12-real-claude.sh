#!/usr/bin/env bash
# H12 (runner): with the real herdr, the real router, and the real worker: a task with no project routes to the
# scratch root, the worker stops at claude's trust question (a folder claude has not seen) and the task goes blocked,
# and once the question is answered in the pane, as a person would, the worker hands the task back. This spends one
# router run and one worker run of the owner's quota. Only the workspaces a run row of this desk names are closed.
# A failure once the worker's pane exists prints the pane's screen first.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of the two that want it open.
unset DESK_HERDR
need_real_herdr
REAL_CLAUDE=$(command -v claude) || fail "no claude"
build
run 0 on home herdr-desk setup --profile claude-code --no-herdr --runner on
# Each template's first word becomes a wrapper that logs which template ran, then runs the real claude. Its paths are
# written into it, because a pane under the real herdr does not inherit this script's environment. It puts this
# script's herdr-desk first on PATH, so the `herdr-desk` the worker runs to hand back is the one under test, not an installed one.
CALLS="$E2E/claude-calls.txt"
cat >"$E2E/count-claude" <<SH
#!/bin/sh
printf '%s\n' "\$1" >>'$CALLS'
shift
PATH='$BIN':"\$PATH"
export PATH
exec '$REAL_CLAUDE' "\$@"
SH
chmod +x "$E2E/count-claude"
python3 - "$E2E/home/config/herdr-desk/config.toml" "$E2E/count-claude" <<'PY' || fail "the profile's templates do not start with claude"
import re, sys
path, wrap = sys.argv[1:3]
text = open(path).read()
for kind in ("router", "worker"):
    text, n = re.subn(r"(?m)^(%s\s*=\s*\[\s*)(['\"])claude\2" % kind, lambda m: m.group(1) + '"%s", "%s"' % (wrap, kind), text)
    if n != 1:
        sys.exit("no %s template starting with claude" % kind)
open(path, "w").write(text)
PY
start_daemon home

run 0 on home herdr-desk add -t "desk e2e: hand this task back" \
  -n "Do nothing else: run the hand-back command from this message with --ref none, then stop. Run it with the herdr-desk binary at $BIN/herdr-desk in place of plain herdr-desk: another herdr-desk may be installed on this machine." --desk --thread agent
run 0 on home herdr-desk set T1 ready
wait_run 1 running 120
track_workspaces
PANE=$(run_field 1 pane)
SHOW_PANE=$PANE
[ "$(run_field 1 root)" = "$SCRATCH" ] || fail "run 1 routed to '$(run_field 1 root)', not $SCRATCH"
say "routed to the scratch root ok"

# herdr shows the pane blocked, with no agent session, while claude asks whether to trust the folder.
wait_task 1 blocked 60
run_is 1 ended || fail "run 1 is $(run_field 1 state)"
task_has_note 1 "the worker is blocked waiting for an answer in pane $PANE" ||
  fail "T1 has no runner note that its worker is blocked in pane $PANE: $(task_notes 1)"
say "trust question → blocked ok"

# The hand-back comes from run 1, ended but still T1's newest run, which the store accepts.
answer_trust_question "$PANE"
wait_task 1 review 300
track_workspaces
SESSION=$(run_field 1 session)
pane_has_session "$SESSION" || fail "herdr shows no pane with the agent session $SESSION"
say "worker session known to herdr ok"
say "T1 $(task_field 1 status)"
[ "$(on home herdr-desk runs --all --json | jq 'length')" = 1 ] || fail "more than one run was started"
ROUTERS=$(grep -cx router "$CALLS" || true)
WORKERS=$(grep -cx worker "$CALLS" || true)
if [ "$ROUTERS" != 1 ] || [ "$WORKERS" != 1 ]; then fail "claude ran $ROUTERS times as the router and $WORKERS times as the worker, not once each"; fi
say "real runs: 1 router, 1 worker"

stop_daemon home
SHOW_PANE=""
close_tracked_workspaces
while read -r id; do
  wait_long 20 "workspace $id to close" workspace_closed "$id"
done < <(sort -u "$E2E/workspaces.txt")
say "every workspace this test opened is closed ok"
pass
