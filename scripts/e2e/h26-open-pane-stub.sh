#!/usr/bin/env bash
# H26: open-pane.sh focuses the open board pane even after a title plugin renamed it, opens one when none is open,
# and takes a busy popup as done, against a stub herdr.
#
# The stub on HERDR_BIN_PATH logs its argv and answers `pane list` from a fixture with the shape herdr 0.9.1
# prints: one line, keys in order, the agent pane's nested objects, and a plugin pane whose cwd is its plugin's
# folder and that carries no agent keys. Values are examples. Nothing opens on a screen; no desk binary is built.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

PLUG="$E2E/plugin"
STUB="$E2E/stub/herdr"
LOG="$E2E/stub.log"
mkdir -p "$PLUG/scripts" "$E2E/stub"
cp "$REPO/scripts/open-pane.sh" "$PLUG/scripts/open-pane.sh"
ROOT=$(cd "$PLUG" && pwd -P)

cat >"$STUB" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$STUB_LOG"
case "$*" in
  "pane list"*) cat "$STUB_PANES" ;;
  *"--entrypoint capture"*)
    if [ "$STUB_POPUP" = busy ]; then
      echo 'error: a popup pane is already open (ui_busy)' >&2
      exit 1
    fi
    echo '{"id":"cli:plugin:pane:open","result":{"type":"plugin_pane_opened"}}'
    ;;
esac
SH
chmod +x "$STUB"

# agent_pane <pane_id> <label> <cwd>: an agent's pane, as herdr lists it.
agent_pane() {
  printf '{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"example"},'
  printf '"agent_status":"idle","cwd":"%s","focused":true,"foreground_cwd":"%s","label":"%s","pane_id":"%s",' "$3" "$3" "$2" "$1"
  printf '"revision":1,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":40},'
  printf '"tab_id":"w1:t1","terminal_id":"term_example1","terminal_title":"example","terminal_title_stripped":"example",'
  printf '"tokens":{"branch":"example"},"workspace_id":"w1"}'
}

# plugin_pane <pane_id> <label> <cwd>: a plugin's pane, as herdr lists it.
plugin_pane() {
  printf '{"agent_status":"unknown","cwd":"%s","focused":false,"foreground_cwd":"%s","label":"%s","pane_id":"%s",' "$3" "$3" "$2" "$1"
  printf '"revision":1,"scroll":{"max_offset_from_bottom":0,"offset_from_bottom":0,"viewport_rows":40},'
  printf '"tab_id":"w1:t1","terminal_id":"term_example2","workspace_id":"w1"}'
}

# pane_list <pane-json...>: herdr's answer to `pane list`.
pane_list() {
  local IFS=,
  printf '{"id":"cli:pane:list","result":{"panes":[%s],"type":"pane_list"}}\n' "$*"
}

# No board pane: a pane labelled desk that is not this plugin's, and another plugin's pane.
pane_list "$(agent_pane w1:p1 desk /example/project)" "$(plugin_pane w1:p2 Files /example/plugins/viewer)" \
  >"$E2E/none.json"
# The board pane after a title plugin renamed it, behind an agent pane.
pane_list "$(agent_pane w1:p1 "example › task" /example/project)" "$(plugin_pane w1:p3 "plugin › desk" "$ROOT")" \
  >"$E2E/renamed.json"

# open_pane <want-exit> <panes-fixture> <popup> <args...>: run the plugin copy of open-pane.sh against the stub,
# outside any herdr workspace, with a fresh log.
open_pane() {
  local want=$1 panes=$2 popup=$3
  shift 3
  : >"$LOG"
  run "$want" env -u HERDR_WORKSPACE_ID -u HERDR_PLUGIN_ID HERDR_BIN_PATH="$STUB" STUB_LOG="$LOG" \
    STUB_PANES="$panes" STUB_POPUP="$popup" sh "$PLUG/scripts/open-pane.sh" "$@"
  CALLS=$(cat "$LOG")
  say "herdr calls: ${CALLS:-none}"
}

called() { grep -qxF -- "$1" <<<"$CALLS" || fail "herdr was not called with: $1"; }
not_called() { if grep -qF -- "$1" <<<"$CALLS"; then fail "herdr was called with: $1"; fi; }

open_pane 0 "$E2E/none.json" free board
called "pane list"
called "plugin pane open --plugin desk --entrypoint board --focus"
not_called "plugin pane focus"
ok "with no board pane, board opens one"

open_pane 0 "$E2E/renamed.json" free board
called "pane list"
called "plugin pane focus w1:p3"
not_called "plugin pane open"
ok "with a board pane whose label was renamed, board focuses it and opens none"

open_pane 0 "$E2E/none.json" free capture
called "plugin pane open --plugin desk --entrypoint capture --focus"
ok "capture opens the popup"

open_pane 0 "$E2E/none.json" busy capture
called "plugin pane open --plugin desk --entrypoint capture --focus"
ok "a busy popup exits 0"

open_pane 2 "$E2E/none.json" free nosuch
grep -qF "unknown pane 'nosuch'" <<<"$ERR" || fail "stderr does not name the unknown pane"
[ -z "$CALLS" ] || fail "an unknown pane name called herdr"
ok "an unknown pane name exits 2"

pass
