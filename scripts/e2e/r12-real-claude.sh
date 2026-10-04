#!/usr/bin/env bash
# H12 (runner): with the real herdr, the real router, and the real worker: a task with no project routes to the
# scratch root and the worker hands it back. This spends one router run and one worker run of the owner's quota.
# Only the workspaces a run row of this desk names are closed.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of the two that want it open.
unset DESK_HERDR
need_real_herdr
REAL_CLAUDE=$(command -v claude) || fail "no claude"
build
run 0 on home desk setup --profile claude-code --no-herdr --runner on
# Each template's first word becomes a wrapper that logs which template ran, then runs the real claude. Its paths are
# written into it, because a pane under the real herdr does not inherit this script's environment.
CALLS="$E2E/claude-calls.txt"
cat >"$E2E/count-claude" <<SH
#!/bin/sh
printf '%s\n' "\$1" >>'$CALLS'
shift
exec '$REAL_CLAUDE' "\$@"
SH
chmod +x "$E2E/count-claude"
python3 - "$E2E/home/config/desk/config.toml" "$E2E/count-claude" <<'PY' || fail "the profile's templates do not start with claude"
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

run 0 on home desk add -t "desk e2e: hand this task back" \
  -n "Do nothing else: run the hand-back command from this message with --ref none, then stop." --desk --thread agent
run 0 on home desk set T1 ready
wait_run 1 running 120
track_workspaces
[ "$(run_field 1 root)" = "$SCRATCH" ] || fail "run 1 routed to '$(run_field 1 root)', not $SCRATCH"
say "routed to the scratch root ok"

wait_task 1 review 300
track_workspaces
SESSION=$(run_field 1 session)
pane_has_session "$SESSION" || fail "herdr shows no pane with the agent session $SESSION"
say "worker session known to herdr ok"
say "T1 $(task_field 1 status)"
[ "$(on home desk runs --all --json | jq 'length')" = 1 ] || fail "more than one run was started"
ROUTERS=$(grep -cx router "$CALLS" || true)
WORKERS=$(grep -cx worker "$CALLS" || true)
[ "$ROUTERS" = 1 ] && [ "$WORKERS" = 1 ] || fail "claude ran $ROUTERS times as the router and $WORKERS times as the worker, not once each"
say "real runs: 1 router, 1 worker"

stop_daemon home
close_tracked_workspaces
while read -r id; do
  wait_long 20 "workspace $id to close" workspace_closed "$id"
done < <(sort -u "$E2E/workspaces.txt")
say "every workspace this test opened is closed ok"
pass
