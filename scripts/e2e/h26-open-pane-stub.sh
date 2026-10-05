#!/usr/bin/env bash
# H26 (H49): open-pane.sh focuses the open board pane even after a title plugin renamed it, opens one when none is open,
# opens the board in a popup and takes a busy popup (a second press) as done, against a stub herdr.
#
# The stub on HERDR_BIN_PATH logs its argv and answers `pane list` from a fixture with the shape herdr 0.9.1
# prints: one line, keys in order, the agent pane's nested objects, and a plugin pane whose cwd is its plugin's
# folder and that carries no agent keys. Values are examples. Nothing opens on a screen; no herdr-desk binary is built.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"

PLUG="$E2E/plugin"
STUB="$E2E/stub/herdr"
LOG="$E2E/stub.log"
mkdir -p "$PLUG/scripts" "$E2E/stub"
cp "$REPO/scripts/open-pane.sh" "$PLUG/scripts/open-pane.sh"
ROOT=$(cd "$PLUG" && pwd -P)

# The stub focuses by id only the panes in STUB_OWNED and answers any other with herdr's plugin_pane_not_found,
# as herdr focuses only a pane a plugin owns. It fails `pane list` when STUB_LIST is fail, and every focus with
# another error when STUB_FOCUS is fail.
cat >"$STUB" <<'SH'
#!/bin/sh
printf '%s\n' "$*" >>"$STUB_LOG"
case "$*" in
  "pane list"*)
    if [ "$STUB_LIST" = fail ]; then
      echo 'error: workspace not found' >&2
      exit 4
    fi
    cat "$STUB_PANES"
    ;;
  "plugin pane focus "*)
    if [ "$STUB_FOCUS" = fail ]; then
      echo 'error: connection reset by peer' >&2
      exit 5
    fi
    case " $STUB_OWNED " in
      *" $4 "*) ;;
      *)
        printf '{"error":{"code":"plugin_pane_not_found","message":"example"}}\n' >&2
        exit 1
        ;;
    esac
    ;;
  *"--entrypoint capture"* | *"--entrypoint board-popup"*)
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

# No board pane: a pane labelled herdr-desk that is not this plugin's, and another plugin's pane.
pane_list "$(agent_pane w1:p1 herdr-desk /example/project)" "$(plugin_pane w1:p2 Files /example/plugins/viewer)" \
  >"$E2E/none.json"
# The board pane after a title plugin renamed it, behind an agent pane that also sits in the plugin's folder.
pane_list "$(agent_pane w1:p1 "example › task" "$ROOT")" "$(plugin_pane w1:p3 "plugin › herdr-desk" "$ROOT")" \
  >"$E2E/renamed.json"
# No board pane, and an agent's pane in the plugin's folder (a checkout linked as the plugin).
pane_list "$(agent_pane w1:p1 "desk › work" "$ROOT")" "$(plugin_pane w1:p2 Files /example/plugins/viewer)" \
  >"$E2E/agent.json"
# No board pane, and a shell in the plugin's folder: it carries no agent keys, and no plugin owns it.
pane_list "$(plugin_pane w1:p4 zsh "$ROOT")" >"$E2E/shell.json"

# open_pane <want-exit> <panes-fixture> <popup> <args...>: run the plugin copy of open-pane.sh against the stub,
# outside any herdr workspace, with a fresh log. w1:p2 and w1:p3 are plugin panes; STUB_LIST=fail fails the list,
# and STUB_FOCUS=fail fails every focus.
open_pane() {
  local want=$1 panes=$2 popup=$3
  shift 3
  : >"$LOG"
  run "$want" env -u HERDR_WORKSPACE_ID -u HERDR_PLUGIN_ID HERDR_BIN_PATH="$STUB" STUB_LOG="$LOG" \
    STUB_PANES="$panes" STUB_POPUP="$popup" STUB_OWNED="w1:p2 w1:p3" STUB_LIST="${STUB_LIST:-}" \
    STUB_FOCUS="${STUB_FOCUS:-}" sh "$PLUG/scripts/open-pane.sh" "$@"
  CALLS=$(cat "$LOG")
  say "herdr calls: ${CALLS:-none}"
}

called() { grep -qxF -- "$1" <<<"$CALLS" || fail "herdr was not called with: $1"; }
not_called() { if grep -qF -- "$1" <<<"$CALLS"; then fail "herdr was called with: $1"; fi; }

open_pane 0 "$E2E/none.json" free board
called "pane list"
called "plugin pane open --plugin herdr-desk --entrypoint board --focus"
not_called "plugin pane focus"
ok "with no board pane, board opens one"

open_pane 0 "$E2E/renamed.json" free board
called "pane list"
called "plugin pane focus w1:p3"
not_called "plugin pane focus w1:p1"
not_called "plugin pane open"
ok "with a board pane whose label was renamed, board focuses it and opens none"

open_pane 0 "$E2E/agent.json" free board
called "plugin pane open --plugin herdr-desk --entrypoint board --focus"
not_called "plugin pane focus"
ok "an agent's pane in the plugin's folder is not taken for the board, and board opens one"

open_pane 0 "$E2E/shell.json" free board
called "plugin pane focus w1:p4"
called "plugin pane open --plugin herdr-desk --entrypoint board --focus"
ok "a shell in the plugin's folder refuses the focus, and board opens one"

STUB_LIST=fail open_pane 4 "$E2E/none.json" free board
grep -qF "workspace not found" <<<"$ERR" || fail "stderr does not carry the failed list's error"
not_called "plugin pane open"
ok "a failed pane list exits with its status and error, and opens no pane"

STUB_FOCUS=fail open_pane 5 "$E2E/renamed.json" free board
called "plugin pane focus w1:p3"
grep -qF "connection reset by peer" <<<"$ERR" || fail "stderr does not carry the failed focus's error"
not_called "plugin pane open"
ok "a focus that fails for another reason than plugin_pane_not_found exits with its status and error, and opens no pane"

open_pane 0 "$E2E/none.json" free capture
called "plugin pane open --plugin herdr-desk --entrypoint capture --focus"
ok "capture opens the popup"

open_pane 0 "$E2E/none.json" busy capture
called "plugin pane open --plugin herdr-desk --entrypoint capture --focus"
ok "a busy popup exits 0"

open_pane 0 "$E2E/renamed.json" free board-popup
called "plugin pane open --plugin herdr-desk --entrypoint board-popup --focus"
not_called "pane list"
not_called "plugin pane focus"
not_called "--entrypoint board --focus"
ok "board-popup opens the popup, and neither lists panes nor focuses the split board"

open_pane 0 "$E2E/none.json" busy board-popup
called "plugin pane open --plugin herdr-desk --entrypoint board-popup --focus"
{ [ -z "$OUT" ] && [ -z "$ERR" ]; } || fail "a second press of board-popup printed: $OUT$ERR"
ok "a second press while the popup is up exits 0 and prints nothing"

open_pane 2 "$E2E/none.json" free nosuch
grep -qF "unknown pane 'nosuch'" <<<"$ERR" || fail "stderr does not name the unknown pane"
[ -z "$CALLS" ] || fail "an unknown pane name called herdr"
ok "an unknown pane name exits 2"

pass
