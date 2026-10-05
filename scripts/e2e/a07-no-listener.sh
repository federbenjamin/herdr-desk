#!/usr/bin/env bash
# H15: on the machine it runs on, no herdr-desk process listens on TCP, the state folder holds no socket, and the
# ticker runs. It reads the installed herdr-desk and the real state folder, and writes nothing.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
STATE="${XDG_STATE_HOME:-$HOME/.local/state}/herdr-desk"
command -v herdr-desk >/dev/null || fail "(env) no herdr-desk on PATH"
command -v lsof >/dev/null || fail "(env) no lsof on PATH"

listening=$(lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | grep -i 'herdr-de' || true)
say "TCP listeners named herdr-desk: ${listening:-none}"
[ -z "$listening" ] || fail "a herdr-desk process listens on TCP: $listening"
ok "no herdr-desk process listens on TCP"

sockets=$(find "$STATE" -type s 2>/dev/null || true)
say "sockets under $STATE: ${sockets:-none}"
[ -z "$sockets" ] || fail "the state folder holds a socket: $sockets"
ok "the state folder holds no socket"

status=$(env -u DESK_SESSION -u CLAUDE_CODE_SESSION_ID herdr-desk ticker status) || fail "ticker status failed"
say "$status"
jq -e '.ticker.running == true' <<<"$status" >/dev/null || fail "the ticker is not running"
ok "ticker status shows running"
pass
