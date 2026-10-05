#!/usr/bin/env bash
# H17 [real pair]: with the home unreachable the client lists its snapshot, refuses add with exit 3, queues a note
# that arrives later with its time, and its session-start hook returns in under 10 s. The home is made unreachable by
# pointing the client at a TEST-NET-1 address that never answers (ssh gives up with exit 255 after ConnectTimeout).
# Run on the home: E2E_CLIENT=<ssh target of the client> E2E_HOME=<target the client uses for this machine>
# bash scripts/e2e/p02-pair-offline.sh
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
pair_up

run 0 on home herdr-desk add -t "seen before the outage" --desk
run 0 cn list
out_has "seen before the outage"

client_config 192.0.2.1

run 0 cn list --json
jq -e '.offline == true and (.tasks | map(.title) | index("seen before the outage")) != null' <<<"$OUT" >/dev/null ||
  fail "the client's list is not the snapshot: $OUT"
ok "offline list from the snapshot"

run 3 cn add -t "while offline" --desk
err_has "home-unreachable"
ok "add exits 3 home-unreachable"

written=$(date +%s)
CN_SESSION=s-p02 run 0 cn note "written while offline"
[ "$OUT" = queued ] || fail "the offline note printed '$OUT', want queued"
ok "note queued"

run_in 0 '{"session_id":"s-p02-hook","source":"startup","hook_event_name":"SessionStart","cwd":"/"}' \
  ctimed hook start --format claude-code
under "$CT" 10 || fail "the hook took $CT s"
out_has "journal is not loaded"
ok "the hook took $CT s (under 10) and printed journal is not loaded"

sleep 3
client_config "$E2E_HOME"
arrived=$(date +%s)
run 0 cn list
run 0 on home herdr-desk session s-p02 --md
out_has "written while offline"
ts=$(sqlite3 "$E2E/home/data/herdr-desk/desk.db" "select ts from events where kind = 'note' and session = 's-p02'")
epoch=$(python3 -c 'import sys; from datetime import datetime; print(int(datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00")).timestamp()))' "$ts")
# Both machines' clocks are read, so the written time is allowed a few seconds of drift.
[ "$epoch" -ge $((written - 5)) ] && [ "$epoch" -le $((written + 5)) ] || fail "the note's ts is $ts (epoch $epoch), written at $written"
[ "$epoch" -lt $((arrived - 2)) ] || fail "the note's ts $ts is the time it arrived ($arrived)"
ok "after the home returns the note is there with its original ts"
pass
