#!/usr/bin/env bash
# H36: on the real herdr, the idle, working, and blocked statuses reach a plugin's pane.agent_status_changed hook, a
# pane's close reaches pane.closed, and `herdr pane get` returns the agent's session. herdr's report-agent takes idle,
# working, blocked, and unknown: it refuses done, so a run's hand-back reaches the hook as idle. Run it in a separate named herdr session (HERDR_SOCKET_PATH). A temp plugin,
# desk-e2e, logs each event to a file and is unlinked at exit; the one workspace the script opens is closed at exit.
# Nothing of herdr-desk runs: it is the contract the event hook rests on.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of those that want it open.
unset DESK_HERDR
need_real_herdr

LOG="$E2E/events.log"
: >"$LOG"
cat >"$E2E/event-logger.sh" <<SH
#!/bin/sh
printf '%s\t%s\n' "\$HERDR_PLUGIN_EVENT" "\$HERDR_PLUGIN_EVENT_JSON" >>'$LOG'
SH
chmod +x "$E2E/event-logger.sh"
link_event_plugin "$E2E/event-logger.sh"

# event_seen <event> <pane> <jq condition on .data>: the log holds a line for that event and pane that meets the condition.
event_seen() {
  while IFS=$'\t' read -r name json; do
    [ "$name" = "$1" ] || continue
    if jq -e --arg p "$2" ".data.pane_id == \$p and ($3)" <<<"$json" >/dev/null 2>&1; then return 0; fi
  done <"$LOG"
  return 1
}
status_seen() { event_seen pane.agent_status_changed "$PANE" ".data.agent_status == \"$1\""; }

created=$(herdr workspace create --cwd "$E2E" --label "desk e2e events" --no-focus) || fail "herdr cannot open a workspace"
WS=$(jq -r .result.workspace.workspace_id <<<"$created")
PANE=$(jq -r .result.root_pane.pane_id <<<"$created")
{ [ -n "$WS" ] && [ "$WS" != null ] && [ -n "$PANE" ] && [ "$PANE" != null ]; } || fail "no workspace or pane in: $created"
printf '%s\n' "$WS" >>"$E2E/workspaces.txt"

# Statuses first: herdr ignores reported states once a pane has an agent session.
for state in idle working blocked; do
  herdr pane report-agent "$PANE" --source desk-e2e --agent stub --state "$state" >/dev/null || fail "herdr refused report-agent $state"
  wait_long 10 "the $state event to reach the hook" status_seen "$state" ||
    {
      cat "$LOG" >&2
      fail "the $state status did not reach the plugin hook"
    }
  ok "$state reached the hook"
done

SESSION=$(python3 -c 'import uuid; print(uuid.uuid4())')
herdr pane report-agent-session "$PANE" --source herdr:claude --agent claude --agent-session-id "$SESSION" >/dev/null || fail "herdr refused report-agent-session"
run 0 herdr pane get "$PANE"
jq -e --arg s "$SESSION" '.result.pane.agent_session.value == $s' <<<"$OUT" >/dev/null || fail "pane get shows no session $SESSION: $OUT"
ok "pane get shows the session"

herdr pane close "$PANE" >/dev/null || fail "herdr cannot close pane $PANE"
wait_long 10 "pane.closed to reach the hook" event_seen pane.closed "$PANE" true ||
  {
    cat "$LOG" >&2
    fail "pane.closed did not reach the plugin hook"
  }
ok "pane.closed reached the hook"
pass
