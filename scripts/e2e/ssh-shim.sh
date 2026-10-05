#!/bin/sh
# ssh-shim.sh <E2E> <home> <words...>: stands in for ssh in a client's [client] command. It logs the call, fails
# like an unreachable host while <E2E>/<home>.down exists, and otherwise runs the words on the home machine as
# ssh would: its own four XDG folders, none of the caller's session variables.
e2e=$1
home=$2
shift 2

printf '%s\n' "$*" >>"$e2e/$home.shim.log"

if [ -e "$e2e/$home.down" ]; then
  hold=$(cat "$e2e/$home.down")
  sleep "${hold:-0}"
  exit 255
fi

exec env -u DESK_SESSION -u DESK_RUN -u DESK_HOOKS -u CLAUDE_CODE_SESSION_ID \
  XDG_CONFIG_HOME="$e2e/$home/config" XDG_STATE_HOME="$e2e/$home/state" \
  XDG_DATA_HOME="$e2e/$home/data" XDG_CACHE_HOME="$e2e/$home/cache" "$@"
