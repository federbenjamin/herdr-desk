#!/usr/bin/env bash
# H25: in the running herdr, open-board opens the board pane and a second press focuses it; capture lands a task
# and shows a refusal.
#
# This runs in the herdr that is running now, so it touches nothing of the installed plugin. It links a temp copy of
# the manifest under the id desk-e2e, with the build, startup, and events blocks removed (so its copy fires no hook
# into a store) and each pane command wrapped to run this script's own herdr-desk binary against temp XDG folders.
# The panes it opens appear on the screen of the person using herdr. An EXIT trap closes exactly the panes that run in the temp plugin folder and unlinks desk-e2e, also
# when a check fails. It never closes another pane, never runs herdr-desk setup, and never reads or writes herdr's
# config.toml. The capture entrypoint is opened as a split pane: a popup has no pane id, and herdr's API can only
# close it, so a popup cannot be read or typed into. Every pane opens without focus; only the second open-board
# moves focus, to the board, and the script gives it back to the pane that had it.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
HERDR=${HERDR_BIN_PATH:-herdr}
ID=desk-e2e
LINKED=0
OPENED=()
PANE=""
ORIG=""

command -v "$HERDR" >/dev/null 2>&1 || fail "herdr is not installed"
command -v jq >/dev/null 2>&1 || fail "jq is not installed"

# pane_ids: the id of every pane herdr has, one per line.
pane_ids() { "$HERDR" pane list | jq -r '.result.panes[].pane_id' | sort; }

# ours: the id of every pane that runs in this script's temp plugin folder, one per line. Only this script's plugin
# can have opened one, so this is the set cleanup may close, whatever herdr or another plugin renames its label to.
ours() {
  "$HERDR" pane list | jq -r --arg p "$(basename "$E2E")/plugin" \
    '.result.panes[] | select((.cwd // "") | contains($p)) | .pane_id' | sort
}

# record <pane>: remember a pane this script opened, so cleanup closes it.
record() { OPENED+=("$1"); }

# await_board: poll until a pane of ours that is not recorded yet appears, record it, and leave its id in PANE. It
# is not run in a subshell: the record must reach the caller's array.
await_board() {
  local id
  for _ in $(seq 1 100); do
    for id in $(ours); do
      if ! printf '%s\n' "${OPENED[@]:-}" | grep -qxF "$id"; then
        record "$id"
        PANE=$id
        return 0
      fi
    done
    sleep 0.1
  done
  "$HERDR" pane list | jq -c '.result.panes[] | {pane_id, label, cwd}' >&2
  fail "no pane of the $ID plugin appeared"
}

# open_capture: open the manifest's capture entrypoint as a split pane, record it, and leave its id in PANE.
open_capture() {
  local out
  out=$("$HERDR" plugin pane open --plugin "$ID" --entrypoint capture --placement split --no-focus) ||
    fail "herdr plugin pane open failed: $out"
  PANE=$(jq -r '.result.plugin_pane.pane.pane_id' <<<"$out")
  { [ -n "$PANE" ] && [ "$PANE" != null ]; } || fail "no pane id in: $out"
  record "$PANE"
}

pane_open() { pane_ids | grep -qxF "$1"; }

pane_text() { "$HERDR" pane read "$1" --source visible 2>/dev/null || true; }

# pane_wait <pane> <text>: poll until the pane's screen holds the text.
pane_wait() {
  for _ in $(seq 1 100); do
    case "$(pane_text "$1")" in *"$2"*) return 0 ;; esac
    sleep 0.1
  done
  say "--- pane $1 ---" >&2
  pane_text "$1" >&2
  fail "timed out waiting for '$2' in pane $1"
}

# focused: the id of the pane that has focus.
focused() { "$HERDR" pane list | jq -r '[.result.panes[] | select(.focused == true)][0].pane_id // ""'; }

# restore_focus gives focus back to the pane that had it when the script started. The one place the script takes
# focus is the second open-board, whose job is to focus the board; zooming a pane on and off is how herdr focuses
# one by id.
restore_focus() {
  [ -n "$ORIG" ] || return 0
  [ "$(focused 2>/dev/null)" = "$ORIG" ] && return 0
  pane_open "$ORIG" || return 0
  "$HERDR" pane zoom "$ORIG" --on >/dev/null 2>&1 || true
  "$HERDR" pane zoom "$ORIG" --off >/dev/null 2>&1 || true
}

# h25_cleanup closes the panes this script opened: the ones it recorded, and any other pane that runs in the temp
# plugin folder. Then it unlinks desk-e2e.
h25_cleanup() {
  local id strays
  strays=$(ours 2>/dev/null || true)
  for id in "${OPENED[@]:-}" $strays; do
    if [ -n "$id" ] && pane_open "$id"; then "$HERDR" pane close "$id" >/dev/null 2>&1 || true; fi
  done
  restore_focus
  if [ "$LINKED" = 1 ]; then "$HERDR" plugin unlink "$ID" >/dev/null 2>&1 || true; fi
}
trap 'h25_cleanup; cleanup' EXIT

if "$HERDR" plugin list --plugin "$ID" --json 2>/dev/null | jq -e '.result.plugins | length > 0' >/dev/null 2>&1; then
  fail "a plugin with the id $ID is already linked"
fi

build
PLUG="$E2E/plugin"
mkdir -p "$PLUG/scripts"
# The copy opens the board without taking focus, so the first open-board leaves the user's focus alone and the
# second one, which must focus the board, is the only step that moves it (restore_focus puts it back).
sed 's/ --focus$/ --no-focus/' "$REPO/scripts/open-pane.sh" >"$PLUG/scripts/open-pane.sh"
grep -q -- '--no-focus' "$PLUG/scripts/open-pane.sh" || fail "the temp open-pane.sh keeps --focus"
ORIG=$(focused)
python3 - "$REPO/herdr-plugin.toml" "$PLUG/herdr-plugin.toml" "$BIN/herdr-desk" "$E2E/home" <<'PY'
import json
import re
import sys

src, dst, desk, home = sys.argv[1:5]
text = open(src).read()
parts = re.split(r"(?m)^(?=\[\[)", text)
text = "".join(p for p in parts if not p.startswith(("[[build]]", "[[startup]]", "[[events]]")))
text = text.replace('id = "herdr-desk"\n', 'id = "desk-e2e"\n', 1)
env = ["env", "-u", "DESK_SESSION", "-u", "DESK_RUN", "-u", "DESK_HOOKS",
       "XDG_CONFIG_HOME=%s/config" % home, "XDG_STATE_HOME=%s/state" % home,
       "XDG_DATA_HOME=%s/data" % home, "XDG_CACHE_HOME=%s/cache" % home]
text = text.replace('command = ["herdr-desk", "capture"]', "command = " + json.dumps(env + [desk, "capture"]))
text = text.replace('command = ["herdr-desk"]', "command = " + json.dumps(env + [desk]))
open(dst, "w").write(text)
PY
grep -q 'id = "desk-e2e"' "$PLUG/herdr-plugin.toml" || fail "the temp manifest has no id desk-e2e"
if grep -Eq '^\[\[(build|startup|events)\]\]' "$PLUG/herdr-plugin.toml"; then fail "the temp manifest still has a build, startup, or events block"; fi

run 0 "$HERDR" plugin link "$PLUG" --enabled
LINKED=1
run 0 "$HERDR" plugin list --plugin "$ID" --json
jq -e '.result.plugins[0].enabled == true' <<<"$OUT" >/dev/null || fail "$ID is not enabled"

run 0 "$HERDR" plugin action invoke open-board --plugin "$ID"
await_board
BOARD=$PANE
pane_wait "$BOARD" "NEEDS YOU"
"$HERDR" pane list | jq -e --arg id "$BOARD" '.result.panes[] | select(.pane_id == $id and (.label | endswith("herdr-desk")))' >/dev/null ||
  fail "the new pane is not titled herdr-desk"
sleep 2
pane_open "$BOARD" || fail "the board pane closed within 2 s"
ok "open-board opened one pane titled herdr-desk and it is still open after 2 s"

run 0 "$HERDR" plugin action invoke open-board --plugin "$ID"
sleep 1
[ "$(ours | wc -l | tr -d ' ')" = 1 ] || fail "a second open-board left $(ours | wc -l | tr -d ' ') panes"
found=0
for _ in $(seq 1 100); do
  if "$HERDR" pane list | jq -e --arg id "$BOARD" '.result.panes[] | select(.pane_id == $id and .focused == true)' >/dev/null; then
    found=1
    break
  fi
  sleep 0.1
done
if [ "$found" != 1 ]; then
  "$HERDR" plugin log list --plugin "$ID" | jq -c '.result.logs[-1] | {exit_code, status, stdout: (.stdout | .[0:700])}' >&2
  "$HERDR" pane list | jq -c '.result.panes[] | select(.pane_id == "'"$BOARD"'") | {pane_id, label, focused}' >&2
  fail "the board pane is not focused after the second open-board"
fi
restore_focus
ok "a second open-board left one pane and it is focused"

open_capture
POPUP=$PANE
pane_wait "$POPUP" "capture:"
"$HERDR" pane send-text "$POPUP" "landed from herdr #tour"
"$HERDR" pane send-keys "$POPUP" enter
wait_task home 1 .task.thread tour
wait_task home 1 .task.title "landed from herdr"
ok "the capture popup landed a task"

for _ in $(seq 1 100); do
  pane_open "$POPUP" || break
  sleep 0.1
done

open_capture
POPUP2=$PANE
pane_wait "$POPUP2" "capture:"
"$HERDR" pane send-text "$POPUP2" "refused in herdr @nosuch"
"$HERDR" pane send-keys "$POPUP2" enter
pane_wait "$POPUP2" "unknown-project"
pane_open "$POPUP2" || fail "the capture pane closed on a refused line"
ok "a refused line shows unknown-project in the popup"

h25_cleanup
LINKED=0
for id in "${OPENED[@]}"; do
  ! pane_open "$id" || fail "pane $id is still open"
done
if "$HERDR" plugin list --plugin "$ID" --json 2>/dev/null | jq -e '.result.plugins | length > 0' >/dev/null 2>&1; then
  fail "$ID is still linked"
fi
ok "every pane this script opened is closed and desk-e2e is unlinked"
pass
