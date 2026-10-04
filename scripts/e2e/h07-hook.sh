#!/usr/bin/env bash
# H7: the session hook prints the view's path on startup and resume, records a compaction, and
# does nothing when switched off.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
run 0 on home desk setup --no-herdr
start_daemon home
S="s-h7"
SESSIONS="$E2E/home/state/desk/sessions"
VIEW="$SESSIONS/$S.md"

hook_input() { printf '{"session_id":"%s","source":"%s","hook_event_name":"SessionStart","cwd":"/"}' "$1" "$2"; }

run_in 0 "$(hook_input "$S" startup)" on home desk hook start --format claude-code
first=$(head -n 1 <<<"$OUT")
[ "$first" = "desk journal for this session: $VIEW" ] || fail "the hook's first line is '$first'"
[ -f "$VIEW" ] || fail "no view file at $VIEW"
[ "$(mode "$VIEW")" = 600 ] || fail "the view file's mode is $(mode "$VIEW")"
say "startup ok"

run 0 as_agent home "$S" desk note "remember this"
run_in 0 "$(hook_input "$S" resume)" on home desk hook start --format claude-code
out_has "$VIEW"
grep -q "remember this" "$VIEW" || fail "the view file lacks the note after resume"
say "resume ok"

run_in 0 "$(hook_input "$S" compact)" on home desk hook start --format claude-code
out_has "$VIEW"
grep -q "\[session\] compacted" "$VIEW" || fail "the view file lacks the compaction line"
say "compact ok"

run_in 0 "$(hook_input s-off startup)" on home env DESK_HOOKS=off desk hook start --format claude-code
[ -z "$OUT" ] || fail "the switched-off hook printed: $OUT"
[ ! -e "$SESSIONS/s-off.md" ] || fail "the switched-off hook wrote a view file"
say "off ok"

rc=0
on home desk hook start --format claude-code >"$E2E/.out" 2>"$E2E/.err" <<<"$(hook_input ../escape startup)" || rc=$?
say "hook with session id ../escape: exit=$rc stderr: $(cat "$E2E/.err")"
[ "$rc" != 0 ] || fail "a session id with a path in it was accepted"
[ ! -e "$E2E/home/state/desk/escape.md" ] || fail "the hook wrote outside the sessions folder"
say "bad id refused"
pass
