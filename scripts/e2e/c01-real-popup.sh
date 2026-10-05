#!/usr/bin/env bash
# H51: on the real herdr, the board in a popup shows the board's header and sections on the alternate screen, laid out to
# the popup's size; the split beside it shows the same task; and closing the popup leaves the split's screen as it was.
# A popup has no pane id, so the script reads it as a person would: it attaches a client to the session in a private tmux
# and reads that screen. Run it in a separate named herdr session (HERDR_SOCKET_PATH), never one a person works in.
# It links a temp copy of the manifest as the plugin desk-e2e (build, startup, and events blocks removed, each pane
# command wrapped to run this script's own herdr-desk on temp folders), opens one workspace of its own, and at exit
# closes the pane it opened and that workspace and unlinks the plugin.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of those that want it open.
unset DESK_HERDR
need_real_herdr
build

ID=desk-e2e
LINKED=0
WS=""
CLIENT=client
TASK="popup shows this task"
PLUG="$E2E/plugin"

c01_cleanup() {
  if popup_up 2>/dev/null; then tm send-keys -t "$CLIENT" q 2>/dev/null || true; sleep 1; fi
  if [ -n "$WS" ]; then herdr workspace close "$WS" >/dev/null 2>&1 || true; fi
  if [ "$LINKED" = 1 ]; then herdr plugin unlink "$ID" >/dev/null 2>&1 || true; fi
}
c01_exit() {
  local rc=$?
  c01_cleanup
  (exit "$rc")
  runner_cleanup
}
trap c01_exit EXIT

if herdr plugin list --plugin "$ID" --json 2>/dev/null | jq -e '.result.plugins | length > 0' >/dev/null 2>&1; then
  fail "(env) a plugin with the id $ID is already linked"
fi
# The session's name is what `herdr session attach` takes: the one whose socket this script reaches.
SESS=$(herdr session list | awk -v s="${HERDR_SOCKET_PATH:-}" '$NF == s { print $1 }')
[ -n "$SESS" ] || fail "(env) HERDR_SOCKET_PATH does not name a herdr session's socket"

home_up home
run 0 on home herdr-desk add -t "$TASK" --desk --status started

mkdir -p "$PLUG/scripts"
cp "$REPO/scripts/open-pane.sh" "$PLUG/scripts/open-pane.sh"
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
if grep -Eq '^\[\[(build|startup|events)\]\]' "$PLUG/herdr-plugin.toml"; then fail "the temp manifest still has a build, startup, or events block"; fi
run 0 herdr plugin link "$PLUG" --enabled
LINKED=1

created=$(herdr workspace create --cwd "$E2E" --label "desk e2e popup" --focus) || fail "cannot open a workspace"
WS=$(jq -r '.result.workspace.workspace_id' <<<"$created")
SHOW_PANE=""

# A client of the session in a private tmux, sized so the split pane is wide enough for the two-column board and the popup
# is not. herdr refuses to nest, so the client runs without the variable that marks a herdr pane.
tm new-session -d -s keeper -x 80 -y 24 -- sleep 3600 || fail "tmux cannot start a server"
tm set-option -g remain-on-exit on
tm new-session -d -s "$CLIENT" -x 260 -y 50 -- env -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID \
  -u HERDR_SOCKET_PATH -u HERDR_ENV herdr session attach "$SESS" || fail "tmux cannot start the herdr client"
term_wait "$CLIENT" "desk e2e popup"

# popup_up: the popup's frame is on the client's screen. herdr draws it as a box titled with the pane's title.
popup_up() { term_screen "$CLIENT" | grep -qF '┌herdr-desk'; }
popup_gone() { ! popup_up; }
# popup_text: the client's screen from the popup's top border to its bottom border, with the box cut to its columns.
popup_text() {
  term_screen "$CLIENT" | python3 -c '
import sys
lines = sys.stdin.read().split("\n")
top = next(i for i, l in enumerate(lines) if "┌herdr-desk" in l)
col = lines[top].index("┌")
end = lines[top].rindex("┐")
out = []
for l in lines[top:]:
    out.append(l[col:end + 1])
    if "└" in l[col:col + 1]:
        break
print("\n".join(out))'
}
# split_text: the split board's screen, with the age column, which moves with the clock, cut out.
split_text() { herdr pane read "$1" --source visible 2>/dev/null | sed -E 's/[0-9]+[smhd] ago/AGO/'; }
# laid_out_to <columns> <screen>: the board is laid out as board.State does for that width: two columns from 110 columns
# (the task page's HISTORY is beside the list), one surface under 78.
laid_out_to() {
  if [ "$1" -ge 110 ]; then
    grep -qF "HISTORY" <<<"$2" || fail "a board $1 columns wide is not two columns: $2"
  elif [ "$1" -lt 78 ]; then
    if grep -qF "HISTORY" <<<"$2"; then fail "a board $1 columns wide is two columns: $2"; fi
  fi
  say "a board $1 columns wide"
}
open_popup() {
  run 0 herdr plugin action invoke open-popup --plugin "$ID"
  wait_long 15 "the popup to open" popup_up
}
close_popup() {
  tm send-keys -t "$CLIENT" q
  wait_long 15 "the popup to close" popup_gone
}

# The popup, over a workspace whose only pane is a shell.
open_popup
wait_long 15 "the popup to show the task" bash -c "tmux -L '$TMUX_SOCK' capture-pane -p -t '$CLIENT' | grep -qF '$TASK'"
POP=$(popup_text)
for want in "herdr-desk  all" "NEEDS YOU" "IN MOTION" "ON DECK" "T1  started  $TASK"; do
  grep -qF -- "$want" <<<"$POP" || fail "the popup lacks '$want': $POP"
done
POPW=$(($(head -n 1 <<<"$POP" | python3 -c 'import sys; print(len(sys.stdin.readline().rstrip("\n")))') - 2))
[ "$POPW" -lt 250 ] || fail "the popup is $POPW columns wide, as wide as the screen"
laid_out_to "$POPW" "$POP"
ok "the popup shows the board's header and its sections"
close_popup

# The split board in the same workspace.
run 0 herdr plugin action invoke open-board --plugin "$ID"
SPLIT=""
for _ in $(seq 1 100); do
  SPLIT=$(herdr pane list | jq -r --arg p "$(basename "$E2E")/plugin" '[.result.panes[] | select((.cwd // "") | contains($p))][0].pane_id // ""')
  [ -n "$SPLIT" ] && break
  sleep 0.1
done
[ -n "$SPLIT" ] || fail "no split board pane appeared"
SHOW_PANE=$SPLIT
wait_long 15 "the split to show the task" bash -c "herdr pane read '$SPLIT' --source visible | grep -qF '$TASK'"
BEFORE=$(split_text "$SPLIT")
for want in "NEEDS YOU" "T1  started  $TASK"; do
  grep -qF -- "$want" <<<"$BEFORE" || fail "the split lacks '$want': $BEFORE"
done
SPLITW=$(python3 -c 'import sys; print(next((len(l.rstrip("\n")) for l in sys.stdin if l.startswith("─")), 0))' <<<"$BEFORE")
[ "${SPLITW:-0}" -gt 0 ] || fail "the split shows no rule: $BEFORE"
laid_out_to "$SPLITW" "$BEFORE"
ok "the split shows the same task"

# The popup over the split, then closed: the split is as it was.
open_popup
close_popup
sleep 1
AFTER=$(split_text "$SPLIT")
[ "$AFTER" = "$BEFORE" ] || fail "the split's screen changed: before
$BEFORE
after
$AFTER"
ok "closing the popup leaves the split's screen as it was"
SHOW_PANE=""
pass
