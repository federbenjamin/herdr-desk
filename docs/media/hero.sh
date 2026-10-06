#!/usr/bin/env bash
# Re-takes docs/media/hero.png: bare `herdr-desk` in a herdr pane, the board on the left and the selected task's page
# on the right, with tasks in NEEDS YOU, IN MOTION, and ON DECK, two of them on live runs, and the runner on.
# Needs go, git, jq, herdr, and vhs (with ttyd). Run it from anywhere: bash docs/media/hero.sh
#
# Nothing here touches your own herdr or herdr-desk. The script builds herdr-desk from this tree, then runs under a
# temporary HOME with its own XDG folders: its own headless herdr server (stopped at the end), its own desk, and a
# PATH on which `herdr-desk` is the binary it built. The worker is a stand-in that sleeps, so no model is called.
set -euo pipefail
repo=$(cd "$(dirname "$0")/../.." && pwd)
out="$repo/docs/media/hero.png"
for t in go git jq herdr vhs; do
  command -v "$t" >/dev/null || { echo "hero.sh: needs $t on PATH" >&2; exit 1; }
done
herdr_bin=$(command -v herdr)

# Short on purpose: herdr's socket lives under this HOME and its path must fit sun_path (104 bytes on macOS).
tmp=$(mktemp -d "${TMPDIR:-/tmp}/hd.XXXXXX")
tmp=$(cd -P "$tmp" && pwd)
server_pid=""
server_ours=0
ticker_started=0
# Stops only what this script started: `herdr server stop` once the server is known to listen under this HOME, else
# the process it spawned.
teardown() {
  if [ "$ticker_started" = 1 ]; then herdr-desk ticker stop >/dev/null 2>&1 || true; fi
  if [ "$server_ours" = 1 ]; then
    herdr server stop >/dev/null 2>&1 || true
  elif [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
  fi
  rm -rf -- "$tmp"
}
trap teardown EXIT

# Built before HOME moves, so go keeps its own module and build caches.
(cd "$repo" && CGO_ENABLED=0 go build -o "$tmp/bin/herdr-desk" ./cmd/herdr-desk)
ln -s "$herdr_bin" "$tmp/bin/herdr"

# Isolation. A herdr pane exports HERDR_SOCKET_PATH, which wins over HOME, and the XDG and DESK variables would point
# herdr-desk at a real desk: every one of them goes. PATH keeps no folder that holds another herdr-desk.
for v in $(compgen -e); do
  case "$v" in HERDR_* | XDG_* | DESK_* | CLAUDE* | ZDOTDIR | BASH_ENV | ENV) unset "$v" ;; esac
done
path="$tmp/bin"
IFS=: read -ra dirs <<<"$PATH"
for d in "${dirs[@]}"; do
  if [ -n "$d" ] && [ ! -e "$d/herdr-desk" ]; then path="$path:$d"; fi
done
export PATH=$path HOME=$tmp BASH_SILENCE_DEPRECATION_WARNING=1
export XDG_CONFIG_HOME=$tmp/.config XDG_STATE_HOME=$tmp/.local/state XDG_DATA_HOME=$tmp/.local/share
export XDG_CACHE_HOME=$tmp/.cache DESK_HERDR=$tmp/bin/herdr
[ "$(command -v herdr-desk)" = "$tmp/bin/herdr-desk" ] || { echo "hero.sh: herdr-desk does not resolve to the build" >&2; exit 1; }

# A login bash with a neutral prompt in every pane; the profile sets PATH again, since macOS's /etc/profile reorders it.
mkdir -p "$XDG_CONFIG_HOME/herdr"
printf '[terminal]\ndefault_shell = "%s"\nshell_mode = "login"\n' "$(command -v bash)" >"$XDG_CONFIG_HOME/herdr/config.toml"
printf "PS1='\$ '\nexport PATH=%q\n" "$PATH" >"$HOME/.bash_profile"

poll() {
  local tries=$1 i
  shift
  for ((i = 1; i <= tries; i++)); do
    if "$@" >/dev/null 2>&1; then return 0; fi
    sleep 0.25
  done
  return 1
}
server_running() { herdr status server 2>/dev/null | grep -q '^status: running'; }

# herdr's own server, started only when none answers under this HOME, and checked to listen under it.
if server_running; then echo "hero.sh: a herdr server already answers under $HOME" >&2; exit 1; fi
nohup herdr server >"$tmp/server.log" 2>&1 &
server_pid=$!
poll 40 server_running || { echo "hero.sh: herdr server did not answer; its log:" >&2; cat "$tmp/server.log" >&2; exit 1; }
sock=$(herdr status server | sed -n 's/^socket: //p')
case "$(cd -P "$(dirname "$sock")" && pwd)/" in
  "$tmp"/*) ;;
  *) echo "hero.sh: herdr's socket $sock is not under $tmp" >&2; exit 1 ;;
esac
server_ours=1

# The fixture: two git roots, alpha and beta, and a worker that tells herdr it is working and then sleeps.
work=$tmp/work
for r in alpha beta; do
  mkdir -p "$work/$r"
  git -C "$work/$r" init -q
  git -C "$work/$r" -c user.name=demo -c user.email=demo@example.invalid commit -q --allow-empty -m init
done
cat >"$tmp/bin/fake-agent" <<'AGENT'
#!/bin/sh
herdr pane report-agent "$HERDR_PANE_ID" --source hero --agent worker --state working >/dev/null 2>&1 || true
printf 'fake agent on model %s\n' "$1"
exec sleep 3600
AGENT
chmod +x "$tmp/bin/fake-agent"

herdr-desk setup --no-herdr >/dev/null
cat >"$XDG_CONFIG_HOME/herdr-desk/config.toml" <<TOML
[runner]
enabled = true
cap = 3
max_runs_per_day = 20
max_run_minutes = 180

[[roots]]
path = "$work/alpha"
about = "the web app"
isolation = "worktree"

[[roots]]
path = "$work/beta"
about = "the sync service"
isolation = "self"

[agent]
worker = ["$tmp/bin/fake-agent", "{model}"]
coordinator = ["$tmp/bin/fake-agent", "{session}"]
models = ["small", "large"]
TOML
chmod 600 "$XDG_CONFIG_HOME/herdr-desk/config.toml"

herdr-desk ticker >"$tmp/ticker.log" 2>&1 &
ticker_started=1
ticker_running() { herdr-desk ticker status | jq -e '.ticker.running'; }
poll 40 ticker_running || { echo "hero.sh: the ticker did not start; its log:" >&2; cat "$tmp/ticker.log" >&2; exit 1; }

# The tasks. add prints T<n>.
add() { herdr-desk add "$@"; }
t_review=$(add -t "Fix the flaky upload test" -p "$work/alpha" \
  -n "Fails about one CI run in 30. The upload worker may not be done when the assertion runs.")
herdr-desk steps "$t_review" add "replace the fixed sleep with a poll on the queue" >/dev/null
herdr-desk steps "$t_review" add "run the test 200 times" >/dev/null
herdr-desk steps "$t_review" add "say the cause in the PR" >/dev/null
for s in s1 s2 s3; do herdr-desk steps "$t_review" toggle "$s" >/dev/null; done
herdr-desk note "the test slept 2s and hoped; it polls the queue now, 200 runs green" --task "$t_review" \
  --ref https://github.com/example/app/pull/41 >/dev/null
herdr-desk set "$t_review" review >/dev/null
t_blocked=$(add -t "Pick a delimiter for the CSV export" -p "$work/beta")
herdr-desk set "$t_blocked" blocked >/dev/null
herdr-desk note "question: semicolons for the EU locales, or commas everywhere?" --task "$t_blocked" >/dev/null
t_retry=$(add -t "Add retry to the sync job" -p "$work/beta" \
  -n "Retry the upstream call on 502 and 503 with backoff, capped at 30s. Log each retry with the attempt number.")
herdr-desk steps "$t_retry" add "wrap the upstream call in a retry loop" >/dev/null
herdr-desk steps "$t_retry" add "cap the backoff at 30s" >/dev/null
herdr-desk steps "$t_retry" add "test a 503 then a 200" >/dev/null
herdr-desk steps "$t_retry" toggle s1 >/dev/null
t_thumbs=$(add -t "Cache the avatar thumbnails" -p "$work/alpha")
add -t "Write the 0.4 release notes" -p "$work/alpha" --status ready >/dev/null
add -t "Bump the Go toolchain to 1.27" -p "$work/beta" --status ready >/dev/null
add -t "Try a dark theme for the settings page" -p "$work/alpha" >/dev/null

herdr-desk run start "$t_retry" --model large >/dev/null
herdr-desk run start "$t_thumbs" >/dev/null
herdr-desk note "retries 502 and 503 now; writing the 503 test" --task "$t_retry" >/dev/null
herdr-desk note "thumbnails cached at 64 and 128 px" --task "$t_thumbs" >/dev/null
two_running() { [ "$(herdr-desk runs --json | jq '[.[] | select(.state == "running")] | length')" = 2 ]; }
poll 80 two_running || { echo "hero.sh: the two runs did not reach running:" >&2; herdr-desk runs >&2; exit 1; }

# The board, in its own workspace, which takes focus so the client below shows it.
ws=$(herdr workspace create --cwd "$work/alpha" --label desk --focus)
board=$(printf '%s' "$ws" | jq -r '.result.root_pane.pane_id')
herdr pane wait-output "$board" --match '$' --source recent --timeout 10000 >/dev/null
herdr pane run "$board" "command -v herdr-desk" >/dev/null
herdr pane wait-output "$board" --match "$tmp/bin/herdr-desk" --source recent --timeout 10000 >/dev/null ||
  { echo "hero.sh: herdr-desk in a pane is not the build" >&2; exit 1; }
herdr pane run "$board" "clear; exec herdr-desk" >/dev/null

# vhs runs under this same environment, so its client attaches to this server. The first attach shows herdr's
# welcome, then its settings: Enter, then Escape, dismisses both. j selects the review row, whose page shows on
# the right: a page with a live run names its root's full path, which would put this machine's temp folder in the
# picture. vhs needs a frame after Show before the Screenshot.
cat >"$tmp/hero.tape" <<TAPE
Output "$tmp/hero.gif"
Set Shell bash
Set FontSize 14
Set Width 1600
Set Height 560
Set Padding 0
Env PS1 "\$ "
Hide
Type 'clear; exec herdr'
Enter
Wait+Screen@15s /continue/
Enter
Wait+Screen@5s /integrations/
Sleep 1s
Escape
Wait+Screen@10s /running ·/
Type "j"
Wait+Screen@5s /200 times/
Sleep 1s
Show
Sleep 1s
Screenshot "$out"
Sleep 0.5s
TAPE
vhs "$tmp/hero.tape"
echo "wrote $out"
