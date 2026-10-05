#!/usr/bin/env bash
# H48: setup writes the sidebar block once, backs herdr's config up, and leaves a hand-written rows list alone. The
# row's bold rule must match both of the row texts that need a person and none that do not.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
HCFG="$E2E/s/config/herdr/config.toml"
mkdir -p "$E2E/s/config/herdr" "$E2E/t/config/herdr"
printf '[keys]\nprefix = "ctrl+b"\n' >"$HCFG"

backups() { find "$E2E/s/config/herdr" -name 'config.toml.herdr-desk-bak-*' | wc -l | tr -d ' '; }

run 0 on s herdr-desk setup
grep -qxF '# >>> herdr-desk sidebar' "$HCFG" || fail "no herdr-desk sidebar block"
grep -qxF '# <<< herdr-desk sidebar' "$HCFG" || fail "the herdr-desk sidebar block is not closed"
python3 - "$HCFG" <<'PY' || fail "the sidebar block is wrong"
import sys, tomllib

c = tomllib.load(open(sys.argv[1], "rb"))
rows = c["ui"]["sidebar"]["agents"]["rows"]
if rows[0] != ["state_icon", "machine", "workspace", "tab"] or rows[1] != ["agent"]:
    sys.exit("herdr's default rows are not kept: %r" % rows)
desk = [r for r in rows if any(isinstance(t, dict) and t.get("token") == "$desk" for t in r)]
if len(desk) != 1 or rows[-1] != desk[0]:
    sys.exit("not exactly one $desk row, last: %r" % rows)
rules = desk[0][0].get("rules", [])
if len(rules) != 1 or not rules[0].get("bold") or not rules[0].get("contains"):
    sys.exit("the row's rule is not one bold contains rule: %r" % rules)
needle = rules[0]["contains"]
for text in ("T23 needs you · blocked", "2 need you · 2 running"):
    if needle not in text:
        sys.exit("the rule %r does not match %r" % (needle, text))
for text in ("2 running", "T21 running · since 14:05", "T22 review · PR #12", "idle"):
    if needle in text:
        sys.exit("the rule %r matches %r" % (needle, text))
PY
ok "a config with no sidebar table gets the fenced block with herdr's default rows and the \$desk row"

[ "$(backups)" = 1 ] || fail "$(backups) backups of herdr's config, not 1"
grep -qxF 'prefix = "ctrl+b"' "$(find "$E2E/s/config/herdr" -name 'config.toml.herdr-desk-bak-*')" || fail "the backup is not the config before setup"
ok "a backup was written"

SUM=$(shasum "$HCFG")
run 0 on s herdr-desk setup
[ "$(shasum "$HCFG")" = "$SUM" ] || fail "a second setup changed herdr's config"
[ "$(backups)" = 1 ] || fail "a second setup wrote a backup"
[ "$(grep -c '# >>> herdr-desk sidebar' "$HCFG")" = 1 ] || fail "a second setup wrote a second sidebar block"
ok "a second setup changes nothing"

OWN="$E2E/t/config/herdr/config.toml"
cat >"$OWN" <<'TOML'
[ui.sidebar.agents]
rows = [
  ["state_icon", "agent"],
  ["$branch"],
]
TOML
cp "$OWN" "$E2E/own.before"
run 0 on t herdr-desk setup
grep -qF 'herdr-desk sidebar' "$OWN" && fail "a sidebar block was written into a config with its own rows"
grep -qF "\$desk" "$OWN" && fail "the \$desk row was written into a config with its own rows"
sed -n '/^\[ui.sidebar.agents\]/,/^\]/p' "$OWN" >"$E2E/own.after"
cmp -s "$E2E/own.before" "$E2E/own.after" ||
  fail "the hand-written rows changed"
out_has "\$desk"
out_has "[ui.sidebar.agents]"
ok "a config with its own rows is unchanged and setup prints the row to add"
pass
