#!/usr/bin/env bash
# H40: `herdr-desk coordinator` twice gives one workspace and the second only reports it, with no focus call; the agent in its pane gets --session-id and the skill as
# --append-system-prompt; an agent session's `coordinator` is not-allowed and opens nothing.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
build
use_fake_herdr
runner_up home
STATE_JSON="$E2E/herdr/state.json"
CALLS="$E2E/herdr/calls.log"

# coordinator_workspaces: how many workspaces labelled desk coordinator the fake holds.
coordinator_workspaces() { jq '[.workspaces[] | select(.label == "desk coordinator")] | length' "$STATE_JSON"; }

run 0 on home herdr-desk coordinator
out_has "coordinator opened"
[ "$(coordinator_workspaces)" = 1 ] || fail "$(coordinator_workspaces) workspaces labelled desk coordinator, not 1"
ok "one workspace labelled desk coordinator"

run 0 on home herdr-desk coordinator
out_has "coordinator open"
[ "$(coordinator_workspaces)" = 1 ] || fail "the second call made $(coordinator_workspaces) workspaces"
# A script's herdr focus moves every attached herdr window, so the second call must not focus anything.
FOCUS_CALLS=$(cut -f2- "$CALLS" | grep -E '^(workspace focus|pane zoom) ' || true)
[ -z "$FOCUS_CALLS" ] || fail "the second call asked herdr to focus: $FOCUS_CALLS"
ok "the second call reported the open coordinator and focused nothing"

wait_file "$STUB/coordinator.argv"
SESSION=$(sqlite3 "$DB" "SELECT session FROM coordinator")
on home herdr-desk skill coordinator >"$E2E/skill.txt"
python3 - "$STUB/coordinator.argv" "$E2E/skill.txt" "$SESSION" <<'PY' || fail "the stub coordinator was not given --session-id and the skill as --append-system-prompt"
import sys

argv = open(sys.argv[1], "rb").read().decode().split("\0")[:-1]
skill = open(sys.argv[2]).read()
session = sys.argv[3]
if argv[argv.index("--session-id") + 1] != session:
    sys.exit("--session-id is not the recorded session")
if argv[argv.index("--append-system-prompt") + 1] != skill:
    sys.exit("--append-system-prompt is not the skill text")
PY
[ "$(env_value "$STUB/coordinator.env" DESK_SESSION)" = "$SESSION" ] || fail "DESK_SESSION in the coordinator's pane is not the recorded session"
ok "the agent got --session-id and the skill as --append-system-prompt"

BEFORE=$(wc -l <"$CALLS" | tr -d ' ')
run 1 as_agent home s-b01 herdr-desk coordinator
err_has "not-allowed"
[ "$(wc -l <"$CALLS" | tr -d ' ')" = "$BEFORE" ] || fail "an agent's coordinator called herdr"
[ "$(coordinator_workspaces)" = 1 ] || fail "an agent's coordinator opened a workspace"
ok "herdr-desk coordinator from an agent session is not-allowed and opens nothing"
pass
