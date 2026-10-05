#!/usr/bin/env bash
# H15 (H50): herdr accepts the plugin manifest, its [[events]] blocks included. Links this tree as a disabled plugin in
# the running herdr, reads it back, and unlinks it. Refuses to run when a plugin with the id herdr-desk exists.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
HERDR=${HERDR_BIN_PATH:-herdr}

if "$HERDR" plugin list --plugin herdr-desk --json | jq -e '.result.plugins | length > 0' >/dev/null 2>&1; then
  fail "a plugin with the id herdr-desk is already installed; this check would unlink it"
fi

trap '"$HERDR" plugin unlink herdr-desk >/dev/null 2>&1 || true; cleanup' EXIT
run 0 "$HERDR" plugin link "$REPO" --disabled
run 0 "$HERDR" plugin list --plugin herdr-desk --json
P='.result.plugins[0]'
jq -e "$P.plugin_id == \"herdr-desk\" and $P.enabled == false" <<<"$OUT" >/dev/null || fail "the linked plugin is not a disabled herdr-desk"
jq -e "[$P.actions[].id] | sort == [\"capture\", \"open-board\", \"open-popup\"]" <<<"$OUT" >/dev/null ||
  fail "the actions are $(jq -c "[$P.actions[].id]" <<<"$OUT")"
jq -e "[$P.panes[].id] | sort == [\"board\", \"board-popup\", \"capture\"]" <<<"$OUT" >/dev/null ||
  fail "the panes are $(jq -c "[$P.panes[].id]" <<<"$OUT")"
jq -e "[$P.events[] | .on] | sort == [\"pane.agent_status_changed\", \"pane.closed\"]" <<<"$OUT" >/dev/null ||
  fail "the events are $(jq -c "[$P.events[]? | .on]" <<<"$OUT")"
jq -e "[$P.events[] | .command == [\"herdr-desk\", \"hook\", \"herdr-event\"]] | all" <<<"$OUT" >/dev/null ||
  fail "an event does not run herdr-desk hook herdr-event: $(jq -c "[$P.events[]? | .command]" <<<"$OUT")"
say "plugin id: $(jq -r "$P.plugin_id" <<<"$OUT"), actions: $(jq -c "[$P.actions[].id] | sort" <<<"$OUT"), panes: $(jq -c "[$P.panes[].id] | sort" <<<"$OUT"), events: $(jq -c "[$P.events[].on]" <<<"$OUT")"

run 0 "$HERDR" plugin unlink herdr-desk
if "$HERDR" plugin list --plugin herdr-desk --json | jq -e '.result.plugins | length > 0' >/dev/null 2>&1; then
  fail "herdr-desk is still linked"
fi
say "unlinked ok"
pass
