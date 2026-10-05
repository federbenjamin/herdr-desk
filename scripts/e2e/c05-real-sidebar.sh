#!/usr/bin/env bash
# H52: on the real herdr, the sidebar draws the $desk row on a run's card and drops it when the run ends. The script
# starts its own named herdr session with a temp XDG_CONFIG_HOME that holds the config `herdr-desk setup` wrote (its
# sidebar block), attaches a client to it in a private tmux to read the sidebar, runs one stub worker through
# `run start`, and at exit stops and deletes the session. It never touches the herdr config of the person running it:
# it fails (env) when herdr does not read the temp config folder.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
# shellcheck source=scripts/e2e/runner-lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/runner-lib.sh"
# The real herdr: lib.sh sealed DESK_HERDR, and this script is one of those that want it open.
unset DESK_HERDR
command -v herdr >/dev/null 2>&1 || fail "(env) no herdr"
command -v tmux >/dev/null 2>&1 || fail "(env) no tmux"
build

SESSION=c5
# A short folder: herdr's session sockets sit under it and a socket path stops at 104 bytes.
XCFG="$E2E/x"
CLIENT=client
SERVER_UP=0

# herdr_x <args...>: herdr as this script's own session sees it. The variables that name a herdr pane or socket of the
# caller are dropped, so it never reaches the caller's herdr.
herdr_x() {
  env -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID -u HERDR_SOCKET_PATH -u HERDR_ENV XDG_CONFIG_HOME="$XCFG" herdr "$@"
}

c05_cleanup() {
  local rc=$1
  if [ "$rc" != 0 ] && [ "$SERVER_UP" = 1 ]; then
    say "--- the client's screen ---" >&2
    term_screen "$CLIENT" >&2 || true
    say "--- the session's panes ---" >&2
    herdr pane list 2>&1 | jq -c '.result.panes[]? | {pane_id, agent, agent_status, tokens}' >&2 || true
  fi
  if [ "$SERVER_UP" = 1 ]; then
    herdr_x session stop "$SESSION" >/dev/null 2>&1 || true
    for _ in $(seq 1 50); do
      herdr_x session delete "$SESSION" >/dev/null 2>&1 && break
      sleep 0.2
    done
  fi
}
c05_exit() {
  local rc=$?
  c05_cleanup "$rc"
  (exit "$rc")
  runner_cleanup
}
trap c05_exit EXIT

# The config setup writes: a machine of its own, whose herdr config has the keys table only. Its sidebar block is the
# one the session reads.
mkdir -p "$E2E/h/config/herdr" "$XCFG/herdr"
printf '[keys]\nprefix = "ctrl+b"\n' >"$E2E/h/config/herdr/config.toml"
run 0 on h herdr-desk setup
grep -qxF '# >>> herdr-desk sidebar' "$E2E/h/config/herdr/config.toml" || fail "setup wrote no sidebar block"
cp "$E2E/h/config/herdr/config.toml" "$XCFG/herdr/config.toml"

# The home that runs the stub worker, as every runner script does.
RC_NOTIFY=none
runner_up home

(herdr_x --session "$SESSION" server >"$E2E/herdr-server.out" 2>&1 &)
SERVER_UP=1
export HERDR_SOCKET_PATH="$XCFG/herdr/sessions/$SESSION/herdr.sock"
wait_long 15 "the herdr session to answer" herdr workspace list
herdr_x session list | grep -F "$XCFG/herdr/sessions/$SESSION" >/dev/null || fail "(env) herdr does not read the temp config folder: $(herdr_x session list)"
HERDR_REAL=1
created=$(herdr workspace create --cwd "$E2E" --label "desk e2e sidebar" --focus) || fail "cannot open a workspace"
say "workspace $(jq -r '.result.workspace.workspace_id' <<<"$created") open in session $SESSION"

tm new-session -d -s keeper -x 80 -y 24 -- sleep 3600 || fail "tmux cannot start a server"
tm set-option -g remain-on-exit on
tm new-session -d -s "$CLIENT" -x 120 -y 40 -- env -u HERDR_PANE_ID -u HERDR_TAB_ID -u HERDR_WORKSPACE_ID \
  -u HERDR_SOCKET_PATH -u HERDR_ENV XDG_CONFIG_HOME="$XCFG" herdr session attach "$SESSION" || fail "tmux cannot start the herdr client"
term_wait "$CLIENT" "desk e2e sidebar"
# A herdr that has never run with this config folder greets the person first: Enter dismisses each step.
for _ in $(seq 1 6); do
  if term_has "$CLIENT" "↵ continue"; then
    term_keys "$CLIENT" Enter
  elif term_has "$CLIENT" "esc close"; then
    term_esc "$CLIENT"
  else
    break
  fi
  sleep 0.7
done

run 0 on home herdr-desk add -t "sidebar row" --desk
set_mode 1 busy
run 0 on home herdr-desk run start T1
wait_run 1 running 30
SHOW_PANE=$(run_field 1 pane)
# herdr draws a card only for a pane it knows an agent in, and it knows the agents it lists, not the stub: tell it the
# pane runs claude, as claude's own hook would.
herdr pane report-agent "$SHOW_PANE" --source desk-e2e --agent claude --state working >/dev/null || fail "herdr refused the agent report"
term_wait "$CLIENT" "T1 running · since"
ok "the sidebar shows T1 running · since"

run 0 on home herdr-desk runs kill T1
term_wait_gone "$CLIENT" "T1 running"
if term_has "$CLIENT" "T1"; then fail "the sidebar still shows T1"; fi
ok "after the close the card is gone"
SHOW_PANE=""
pass
