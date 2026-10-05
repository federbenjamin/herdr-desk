#!/usr/bin/env bash
# H13: the session hook prints the view's path on startup and resume, records a compaction, and
# does nothing when switched off.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home
S="s-h7"
SESSIONS="$E2E/home/state/herdr-desk/sessions"
VIEW="$SESSIONS/$S.md"

hook_input() { printf '{"session_id":"%s","source":"%s","hook_event_name":"SessionStart","cwd":"/"}' "$1" "$2"; }

run_in 0 "$(hook_input "$S" startup)" on home herdr-desk hook start --format claude-code
first=$(head -n 1 <<<"$OUT")
[ "$first" = "herdr-desk journal for this session: $VIEW" ] || fail "the hook's first line is '$first'"
[ -f "$VIEW" ] || fail "no view file at $VIEW"
[ "$(mode "$VIEW")" = 600 ] || fail "the view file's mode is $(mode "$VIEW")"
say "startup ok"

run 0 as_agent home "$S" herdr-desk note "remember this"
grep -q "remember this" "$VIEW" || fail "the view file lacks the note right after herdr-desk note"
run_in 0 "$(hook_input "$S" resume)" on home herdr-desk hook start --format claude-code
out_has "$VIEW"
grep -q "remember this" "$VIEW" || fail "the view file lacks the note after resume"
say "resume ok"

run_in 0 "$(hook_input "$S" compact)" on home herdr-desk hook start --format claude-code
out_has "$VIEW"
grep -q "\[session\] compacted" "$VIEW" || fail "the view file lacks the compaction line"
say "compact ok"

run_in 0 "$(hook_input s-off startup)" on home env DESK_HOOKS=off herdr-desk hook start --format claude-code
[ -z "$OUT" ] || fail "the switched-off hook printed: $OUT"
[ ! -e "$SESSIONS/s-off.md" ] || fail "the switched-off hook wrote a view file"
say "off ok"

rc=0
on home herdr-desk hook start --format claude-code >"$E2E/.out" 2>"$E2E/.err" <<<"$(hook_input ../escape startup)" || rc=$?
say "hook with session id ../escape: exit=$rc stderr: $(cat "$E2E/.err")"
[ "$rc" != 0 ] || fail "a session id with a path in it was accepted"
[ ! -e "$E2E/home/state/herdr-desk/escape.md" ] || fail "the hook wrote outside the sessions folder"
say "bad id refused"
pass
