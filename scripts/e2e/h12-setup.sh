#!/usr/bin/env bash
# H42: setup writes the config with its warning, the scratch root, the claude-code profile (the worker and coordinator
# templates, no router), the skill, and the herdr keys only where they are free. With no herdr config it creates one
# only when herdr is found, and a second run leaves that one as it is.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
CFG="$E2E/s/config/herdr-desk/config.toml"
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

run 0 on s herdr-desk setup --profile claude-code --skill-dir "$E2E/s/skills"
[ "$(mode "$CFG")" = 600 ] || fail "the config's mode is $(mode "$CFG")"
say "config 0600 ok"
grep -B 3 '^start_runs' "$CFG" | grep -q 'WARNING' || fail "no WARNING comment above start_runs"
say "warning comment ok"
python3 -c 'import sys, tomllib; c = tomllib.load(open(sys.argv[1], "rb")); sys.exit(0 if c["coordinator"]["start_runs"] == "propose" else 1)' "$CFG" ||
  fail "start_runs is not propose"
say "start_runs propose ok"
[ -d "$E2E/s/data/herdr-desk/scratch/.git" ] || fail "the scratch root is not a git repo"
say "scratch root ok"

grep -q '^# >>> herdr-desk keys' "$HERDR" || fail "no herdr-desk keys block"
grep -q '^# <<< herdr-desk keys' "$HERDR" || fail "the herdr-desk keys block is not closed"
grep -q 'command = "herdr-desk.capture"' "$HERDR" || fail "prefix+a is not bound to herdr-desk.capture"
say "keys written ok"
grep -q 'command = "other.open"' "$HERDR" || fail "the other program's prefix+t binding was removed"
if grep -q 'command = "herdr-desk.open-board"' "$HERDR"; then fail "prefix+t was taken without --force"; fi
out_has "prefix+t"
say "taken keys left ok"
ls "$E2E"/s/config/herdr/config.toml.herdr-desk-bak-* >/dev/null 2>&1 || fail "no backup of herdr's config"
say "backup file ok"

grep -Eq "session_env = [\"']CLAUDE_CODE_SESSION_ID[\"']" "$CFG" || fail "no session_env from the profile"
python3 - "$CFG" <<'PY' || fail "the profile's worker or coordinator template is wrong, or a router is left"
import sys, tomllib

c = tomllib.load(open(sys.argv[1], "rb"))
a = c["agent"]
if "router" in a or "router" in c:
    sys.exit("a router is written")
w, k = a["worker"], a["coordinator"]
if w[0] != "claude" or "--permission-mode" not in w or "{model}" not in w or "{session}" not in w or "{message}" not in w:
    sys.exit("worker: %r" % w)
if k[0] != "claude" or "--permission-mode" not in k:
    sys.exit("coordinator: %r" % k)
if k[k.index("--session-id") + 1] != "{session}" or k[k.index("--append-system-prompt") + 1] != "{prompt}":
    sys.exit("coordinator: %r" % k)
PY
say "profile templates ok: a worker and a coordinator, no router"
python3 -c 'import sys, tomllib; c = tomllib.load(open(sys.argv[1], "rb")); sys.exit(0 if c["agent"]["models"] == ["sonnet", "opus"] else 1)' "$CFG" ||
  fail "the profile did not write models = [\"sonnet\", \"opus\"]"
say "profile models ok"
grep -q '^name: herdr-desk' "$E2E/s/skills/herdr-desk/SKILL.md" || fail "the skill file was not written"
cmp -s "$E2E/s/skills/herdr-desk/SKILL.md" "$REPO/profiles/claude-code/skills/herdr-desk/SKILL.md" || fail "the written skill differs from the repo's"
say "skill written ok"

cp "$HERDR" "$E2E/herdr.before"
cp "$CFG" "$E2E/cfg.before"
run 0 on s herdr-desk setup --profile claude-code --skill-dir "$E2E/s/skills"
cmp -s "$HERDR" "$E2E/herdr.before" || fail "a second setup changed herdr's config"
cmp -s "$CFG" "$E2E/cfg.before" || fail "a second setup changed the herdr-desk config"
say "second run changes nothing ok"

run 0 on s herdr-desk setup --profile claude-code --force
grep -q 'command = "herdr-desk.open-board"' "$HERDR" || fail "--force did not bind prefix+t"
if grep -q 'command = "other.open"' "$HERDR"; then fail "--force left the other prefix+t binding"; fi
grep -q 'command = "other.x"' "$HERDR" || fail "--force removed an unrelated binding"
[ "$(grep -c 'key = "prefix+t"' "$HERDR")" = 1 ] || fail "prefix+t is bound more than once"
say "force replaced ok"
if grep -q 'ctrl+d' "$HERDR"; then fail "ctrl+d appears in herdr's config"; fi
say "no ctrl+d ok"

run 0 on bare herdr-desk setup
[ ! -e "$E2E/bare/config/herdr" ] || fail "setup created a herdr config with no herdr found"
out_has "herdr: no herdr found (DESK_HERDR \"$DESK_HERDR\": "
out_has "and no config file; keys and sidebar row not written. Once herdr is found, run: herdr-desk setup"
say "no herdr config, no herdr: left alone ok"

STUB_HERDR=$(stub_herdr)
NEW="$E2E/n/config/herdr/config.toml"
run 0 on n env DESK_HERDR="$STUB_HERDR" herdr-desk setup
[ -f "$NEW" ] || fail "setup did not create herdr's config with herdr found"
[ "$(mode "$NEW")" = 600 ] || fail "the created herdr config's mode is $(mode "$NEW")"
for fence in '# >>> herdr-desk keys' '# <<< herdr-desk keys' '# >>> herdr-desk sidebar' '# <<< herdr-desk sidebar'; do
  grep -qx "$fence" "$NEW" || fail "the created herdr config lacks $fence"
done
python3 -c 'import sys, tomllib; tomllib.load(open(sys.argv[1], "rb"))' "$NEW" || fail "the created herdr config does not parse"
out_has "herdr: $NEW created"
say "no herdr config, herdr found: created ok"

cp "$NEW" "$E2E/new.before"
run 0 on n env DESK_HERDR="$STUB_HERDR" herdr-desk setup
out_has "herdr: $NEW unchanged"
cmp -s "$NEW" "$E2E/new.before" || fail "a second setup changed the created herdr config"
if ls "$NEW".herdr-desk-bak-* >/dev/null 2>&1; then fail "a backup was written for the created herdr config"; fi
say "second run on the created config changes nothing ok"
pass
