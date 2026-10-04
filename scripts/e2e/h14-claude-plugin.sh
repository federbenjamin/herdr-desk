#!/usr/bin/env bash
# H14: the Claude Code plugin and its marketplace file pass Claude Code's own validator.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build
PLUGIN="$REPO/profiles/claude-code"

run 0 claude plugin validate --strict "$PLUGIN"
say "plugin valid"
run 0 claude plugin validate --strict "$REPO/.claude-plugin/marketplace.json"
say "marketplace valid"

jq -e '.name == "desk"' "$PLUGIN/.claude-plugin/plugin.json" >/dev/null || fail "the plugin is not named desk"
jq -e '.plugins | length == 1 and .[0].name == "desk" and .[0].source == "./profiles/claude-code"' \
  "$REPO/.claude-plugin/marketplace.json" >/dev/null || fail "the marketplace does not list desk at ./profiles/claude-code"
jq -e '[.hooks.SessionStart[].hooks[].command] == ["desk hook start --format claude-code"]' \
  "$PLUGIN/hooks/hooks.json" >/dev/null || fail "the SessionStart hook is not the desk hook"
for source in startup resume clear compact fork; do
  jq -e --arg s "$source" '.hooks.SessionStart[0].matcher | split("|") | index($s) != null' \
    "$PLUGIN/hooks/hooks.json" >/dev/null || fail "the hook's matcher lacks $source"
done
say "hook wired ok"

run 0 on s desk setup --no-herdr --skill-dir "$E2E/sk"
cmp -s "$E2E/sk/desk/SKILL.md" "$PLUGIN/skills/desk/SKILL.md" || fail "the embedded skill differs from the plugin's file"
say "skill matches the embedded text"
pass
