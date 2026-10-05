#!/usr/bin/env bash
# The coordinator's stand-in for the e2e scripts: stub-coordinator.sh <stubdir> <args...>.
# `herdr-desk coordinator run` execs it with the [agent] coordinator template's words. It records the arguments, each
# ended by a NUL, in <stubdir>/coordinator.argv and its environment in <stubdir>/coordinator.env, then sleeps.
set -eu
dir=$1
shift
printf '%s\0' "$@" >"$dir/coordinator.argv"
{
  printf 'DESK_SESSION=%s\n' "${DESK_SESSION-}"
  printf 'XDG_CONFIG_HOME=%s\n' "${XDG_CONFIG_HOME-}"
  printf 'XDG_STATE_HOME=%s\n' "${XDG_STATE_HOME-}"
  printf 'XDG_DATA_HOME=%s\n' "${XDG_DATA_HOME-}"
  printf 'XDG_CACHE_HOME=%s\n' "${XDG_CACHE_HOME-}"
  printf 'PWD=%s\n' "$(pwd -P)"
} >"$dir/coordinator.env"
sleep 300
