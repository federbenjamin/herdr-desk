#!/usr/bin/env bash
# H12: setup writes the config with its warning, the scratch root, the profile, the skill, and
# the herdr keys only where they are free.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
CFG="$E2E/s/config/desk/config.toml"
HERDR="$E2E/s/config/herdr/config.toml"
mkdir -p "$E2E/s/config/herdr"
cat >"$HERDR" <<'TOML'
[keys]
prefix = "ctrl+b"

[[keys.command]]
key = "prefix+t"
type = "plugin_action"
command = "other.open"

[[keys.command]]
key = "prefix+x"
type = "plugin_action"
command = "other.x"
TOML

run 0 on s desk setup --profile claude-code --skill-dir "$E2E/s/skills"
[ "$(mode "$CFG")" = 600 ] || fail "the config's mode is $(mode "$CFG")"
say "config 0600 ok"
grep -B 3 '^agents_may_arm' "$CFG" | grep -q 'WARNING' || fail "no WARNING comment above agents_may_arm"
say "warning comment ok"
[ -d "$E2E/s/data/desk/scratch/.git" ] || fail "the scratch root is not a git repo"
say "scratch root ok"

grep -q '^# >>> desk keys' "$HERDR" || fail "no desk keys block"
grep -q '^# <<< desk keys' "$HERDR" || fail "the desk keys block is not closed"
grep -q 'command = "desk.capture"' "$HERDR" || fail "prefix+a is not bound to desk.capture"
say "keys written ok"
grep -q 'command = "other.open"' "$HERDR" || fail "the other program's prefix+t binding was removed"
if grep -q 'command = "desk.open-board"' "$HERDR"; then fail "prefix+t was taken without --force"; fi
out_has "prefix+t"
say "taken keys left ok"
ls "$E2E"/s/config/herdr/config.toml.desk-bak-* >/dev/null 2>&1 || fail "no backup of herdr's config"
say "backup file ok"

grep -Eq "session_env = [\"']CLAUDE_CODE_SESSION_ID[\"']" "$CFG" || fail "no session_env from the profile"
grep -q -- '--safe-mode' "$CFG" || fail "the router template lacks --safe-mode"
grep -q -- '--permission-mode' "$CFG" || fail "the worker template lacks --permission-mode"
say "profile templates ok"
grep -q '^name: desk' "$E2E/s/skills/desk/SKILL.md" || fail "the skill file was not written"
cmp -s "$E2E/s/skills/desk/SKILL.md" "$REPO/profiles/claude-code/skills/desk/SKILL.md" || fail "the written skill differs from the repo's"
say "skill written ok"

cp "$HERDR" "$E2E/herdr.before"
cp "$CFG" "$E2E/cfg.before"
run 0 on s desk setup --profile claude-code --skill-dir "$E2E/s/skills"
cmp -s "$HERDR" "$E2E/herdr.before" || fail "a second setup changed herdr's config"
cmp -s "$CFG" "$E2E/cfg.before" || fail "a second setup changed the desk config"
say "second run changes nothing ok"

run 0 on s desk setup --profile claude-code --force
grep -q 'command = "desk.open-board"' "$HERDR" || fail "--force did not bind prefix+t"
if grep -q 'command = "other.open"' "$HERDR"; then fail "--force left the other prefix+t binding"; fi
grep -q 'command = "other.x"' "$HERDR" || fail "--force removed an unrelated binding"
[ "$(grep -c 'key = "prefix+t"' "$HERDR")" = 1 ] || fail "prefix+t is bound more than once"
say "force replaced ok"
if grep -q 'ctrl+d' "$HERDR"; then fail "ctrl+d appears in herdr's config"; fi
say "no ctrl+d ok"

run 0 on bare desk setup
[ ! -e "$E2E/bare/config/herdr" ] || fail "setup created a herdr config where there was none"
say "no herdr config: left alone ok"
pass
