#!/usr/bin/env bash
# H27: the install script is the whole install. With claude on PATH it installs the binary, runs setup with the
# claude-code profile, creates herdr's config when herdr has none, and installs the Claude Code plugin through claude's
# CLI. With no claude it picks no profile and prints the pointers. A claude that fails is reported and the install
# still exits 0. The claude here is a fake that logs its arguments; no real claude plugin command runs.
# shellcheck source=scripts/e2e/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
build

V=9.9.9
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  arm64 | aarch64) ARCH=arm64 ;;
  x86_64 | amd64) ARCH=amd64 ;;
  *) fail "unmapped arch $(uname -m)" ;;
esac
ASSET="herdr-desk_${V}_${OS}_${ARCH}.tar.gz"
REL="$E2E/rel/v$V"
mkdir -p "$REL"
tar -czf "$REL/$ASSET" -C "$BIN" herdr-desk
(cd "$REL" && shasum -a 256 "$ASSET" >checksums.txt)

# herdr counts as found through DESK_HERDR: an executable file that is never run.
STUB_HERDR="$E2E/stub-herdr"
printf '#!/bin/sh\nexit 0\n' >"$STUB_HERDR"
chmod +x "$STUB_HERDR"

CLAUDE_LOG="$E2E/fake-claude.log"
mkdir -p "$E2E/fakebin"
cat >"$E2E/fakebin/claude" <<SH
#!/bin/sh
printf '%s\n' "\$*" >>"$CLAUDE_LOG"
exit "\${FAKE_CLAUDE_EXIT:-0}"
SH
chmod +x "$E2E/fakebin/claude"

# Every folder that holds a herdr-desk is dropped, so the script must install one; NO_CLAUDE_PATH also drops every
# folder that holds a claude, the machine's real one included.
CLEAN_PATH=$(tr ':' '\n' <<<"$PATH" | while read -r d; do [ -x "$d/herdr-desk" ] || printf '%s\n' "$d"; done | paste -sd: -)
NO_CLAUDE_PATH=$(tr ':' '\n' <<<"$CLEAN_PATH" | while read -r d; do [ -x "$d/claude" ] || printf '%s\n' "$d"; done | paste -sd: -)

# install <machine> <PATH> [extra env...]: run the install script as a person on that machine.
install() {
  local m=$1 path=$2
  shift 2
  run 0 on "$m" env PATH="$path" DESK_HERDR="$STUB_HERDR" DESK_REPO_ROOT="$REPO" DESK_VERSION="$V" \
    DESK_BASE_URL="file://$E2E/rel" DESK_OUT="$E2E/$m/out/herdr-desk" DESK_INSTALL_DIR="$E2E/$m/inst" "$@" \
    sh "$REPO/scripts/fetch-or-build.sh"
}

SLASH="Inside Claude Code: /plugin marketplace add federbenjamin/herdr-desk, then /plugin install herdr-desk@herdr-desk"

install s "$E2E/fakebin:$CLEAN_PATH"
[ -x "$E2E/s/inst/herdr-desk" ] || fail "no binary in the install dir"
out_has "claude found at $E2E/fakebin/claude; setup uses the claude-code profile."
CFG="$E2E/s/config/herdr-desk/config.toml"
[ "$(mode "$CFG")" = 600 ] || fail "the herdr-desk config's mode is $(mode "$CFG")"
python3 - "$CFG" <<'PY' || fail "the herdr-desk config has no claude-code [agent] templates"
import sys, tomllib

a = tomllib.load(open(sys.argv[1], "rb"))["agent"]
if a["worker"][0] != "claude" or a["coordinator"][0] != "claude" or a["session_env"] != "CLAUDE_CODE_SESSION_ID":
    sys.exit("agent: %r" % a)
PY
ok "claude found: profile claude-code"

HERDR="$E2E/s/config/herdr/config.toml"
[ -f "$HERDR" ] || fail "herdr's config was not created"
[ "$(mode "$HERDR")" = 600 ] || fail "herdr's config's mode is $(mode "$HERDR")"
out_has "herdr: $HERDR created"
for fence in '# >>> herdr-desk keys' '# <<< herdr-desk keys' '# >>> herdr-desk sidebar' '# <<< herdr-desk sidebar'; do
  grep -qx "$fence" "$HERDR" || fail "herdr's config lacks $fence"
done
python3 -c 'import sys, tomllib; tomllib.load(open(sys.argv[1], "rb"))' "$HERDR" || fail "herdr's created config does not parse"
if ls "$HERDR".herdr-desk-bak-* >/dev/null 2>&1; then fail "a backup was written for a config that did not exist"; fi
ok "herdr config created, 0600, both blocks, parses"

printf 'plugin marketplace add federbenjamin/herdr-desk\nplugin install herdr-desk@herdr-desk\n' >"$E2E/claude.want"
cmp -s "$CLAUDE_LOG" "$E2E/claude.want" || fail "the fake claude was not run as marketplace add, then install: $(cat "$CLAUDE_LOG" 2>/dev/null)"
out_has "herdr-desk: Claude Code plugin installed (marketplace federbenjamin/herdr-desk, plugin herdr-desk@herdr-desk)."
ok "claude: marketplace add then install"

if REAL_HERDR=$(command -v herdr); then
  run 0 env HERDR_CONFIG_PATH="$HERDR" "$REAL_HERDR" config check
  case "$OUT$ERR" in *"config: ok"*) ;; *) fail "herdr config check did not say config: ok" ;; esac
  ok "herdr config check: config: ok"
else
  ok "herdr config check skipped: no herdr"
fi

install t "$NO_CLAUDE_PATH"
out_has "herdr-desk: no claude on PATH; setup uses no profile. To start another agent, set [agent] in the herdr-desk config named below"
out_has "herdr-desk: no claude on PATH, so the Claude Code plugin was not installed. $SLASH"
python3 -c 'import sys, tomllib; sys.exit(1 if tomllib.load(open(sys.argv[1], "rb")).get("agent", {}).get("worker") else 0)' \
  "$E2E/t/config/herdr-desk/config.toml" || fail "setup with no claude wrote an [agent] worker"
cmp -s "$CLAUDE_LOG" "$E2E/claude.want" || fail "a claude ran although none was on PATH"
ok "no claude: no profile, pointer printed"

install t "$E2E/fakebin:$CLEAN_PATH" FAKE_CLAUDE_EXIT=1
out_has "herdr-desk: Claude Code plugin not installed (claude plugin marketplace add exited 1; see above). $SLASH"
out_lacks "Claude Code plugin installed"
ok "a failing claude is reported, install exits 0"
pass
