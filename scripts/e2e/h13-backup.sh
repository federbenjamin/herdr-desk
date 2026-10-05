#!/usr/bin/env bash
# H14: a backup exports every event and pushes it to the configured remote.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
home_up home

run 1 on home herdr-desk backup
err_has "backup-off"
say "backup-off refused ok"

REMOTE="$E2E/remote.git"
run 0 git init --quiet --bare "$REMOTE"
write_config home <<TOML
[backup]
git_remote = "$REMOTE"
TOML

run 0 on home herdr-desk add -t "first" --desk
run 0 on home herdr-desk add -t "second" --desk
run 0 as_agent home s-h13 herdr-desk note "a fact worth keeping"

run 0 on home herdr-desk backup
out_has "backup: 3 events"
out_has "committed"
out_has "pushed"
LINES=$(git --git-dir="$REMOTE" show main:events.jsonl | wc -l | tr -d ' ')
[ "$LINES" = 3 ] || fail "the remote's events.jsonl has $LINES lines, want 3"
git --git-dir="$REMOTE" show main:events.jsonl | grep -q "a fact worth keeping" || fail "the note is not in the remote's export"
say "pushed ok: the remote's events.jsonl has $LINES lines for 3 events"

run 0 on home herdr-desk backup
out_has "unchanged"
[ "$(git --git-dir="$REMOTE" rev-list --count main)" = 1 ] || fail "an unchanged backup made a second commit"
say "second run unchanged ok"
pass
