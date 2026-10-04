#!/usr/bin/env bash
# The router's stand-in for the runner's e2e scripts: stub-router.sh <stubdir> <args...>.
# Saves what it was given into <stubdir>, prints <stubdir>/router-out.json, and exits with the number in
# <stubdir>/router-exit (0 when the file is absent). When <stubdir>/router-sleep holds a number it sleeps that
# many seconds first, and <stubdir>/router-pid holds its pid while it does.
set -eu
dir=$1
shift
cat >"$dir/router-stdin.json"
printf '%s\n' "$@" >"$dir/router-argv.txt"
printf '%s' "${DESK_HOOKS-unset}" >"$dir/router-hooks.txt"
printf 'call\n' >>"$dir/router-calls.txt"
if [ -f "$dir/router-sleep" ]; then
  printf '%s' "$$" >"$dir/router-pid"
  sleep "$(cat "$dir/router-sleep")"
fi
if [ -f "$dir/router-out.json" ]; then cat "$dir/router-out.json"; fi
if [ -f "$dir/router-exit" ]; then exit "$(cat "$dir/router-exit")"; fi
exit 0
