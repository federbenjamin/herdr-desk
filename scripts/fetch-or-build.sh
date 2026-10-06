#!/bin/sh
# fetch-or-build.sh: herdr [[build]] step for herdr-desk.
#
# Downloads the release archive for the version herdr-plugin.toml declares and this platform,
# verifies its SHA-256 against checksums.txt, and places the binary at $DESK_OUT. On any miss
# (no release, download error, checksum mismatch, unmapped platform) it builds from source with
# Go instead, with the version set to <version>+src. It also copies the binary to
# $DESK_INSTALL_DIR when no `herdr-desk` is on PATH, or when the one there is the copy it made before.
# Then it runs `herdr-desk setup`, with the claude-code profile when `claude` is on PATH, and with
# `claude` on PATH installs the Claude Code plugin. A step after the binary is placed that does not
# finish is reported, and the script still exits 0. herdr shows a build command's output only when it
# fails, so the report of those steps is also written to <XDG_STATE_HOME or ~/.local/state>/herdr-desk/install.log.
#
# Overrides, for tests: DESK_REPO_ROOT, DESK_VERSION, DESK_BASE_URL (the folder holding
# v<version>/), DESK_OUT, DESK_INSTALL_DIR, DESK_GO. `claude` and `herdr-desk` are found on PATH only.
set -u

repo="federbenjamin/herdr-desk"
# The Claude Code plugin's id, <plugins[0].name>@<name> of .claude-plugin/marketplace.json.
plugin="herdr-desk@herdr-desk"
slash="Inside Claude Code: /plugin marketplace add $repo, then /plugin install $plugin"

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root="${DESK_REPO_ROOT:-$script_dir/..}"
base_url="${DESK_BASE_URL:-https://github.com/$repo/releases/download}"
out="${DESK_OUT:-$repo_root/bin/herdr-desk}"
install_dir="${DESK_INSTALL_DIR:-$HOME/.local/bin}"
install_log="${XDG_STATE_HOME:-$HOME/.local/state}/herdr-desk/install.log"
go_cmd="${DESK_GO:-go}"
version="${DESK_VERSION:-}"
tmpdir=""
log_tmp=""

have() { command -v "$1" >/dev/null 2>&1; }

trap 'if [ -n "$tmpdir" ]; then rm -rf "$tmpdir"; fi; if [ -n "$log_tmp" ]; then rm -f "$log_tmp"; fi' EXIT

# place copies $out to $install_dir/herdr-desk through a temp file and a rename. A copy over the file in
# place would change a binary a running ticker has open, and macOS kills a process whose signed
# file changed under it.
place() {
  mkdir -p "$install_dir" || return 1
  if cp "$out" "$install_dir/.herdr-desk.new.$$" && mv -f "$install_dir/.herdr-desk.new.$$" "$install_dir/herdr-desk"; then
    return 0
  fi
  rm -f "$install_dir/.herdr-desk.new.$$"
  return 1
}

# install_from_path makes the binary at $out the `herdr-desk` on PATH: it installs one when none is
# there, and replaces the one this script installed before. A herdr-desk from anywhere else (Homebrew,
# go install) is left alone. It exits 1 naming the place it could not write.
install_from_path() {
  on_path=$(command -v herdr-desk 2>/dev/null || true)
  if [ -n "$on_path" ] && [ "$on_path" != "$install_dir/herdr-desk" ]; then
    echo "herdr-desk: the herdr-desk on your PATH is $on_path, which this install does not manage; this build is at $out."
    return 0
  fi
  if ! place; then
    echo "herdr-desk: it could not be installed to $install_dir/herdr-desk; copy $out onto your PATH" >&2
    exit 1
  fi
  if [ -n "$on_path" ]; then
    echo "herdr-desk: updated $install_dir/herdr-desk. Run \`herdr-desk ticker stop\`; herdr's next start runs the new ticker."
    return 0
  fi
  echo "herdr-desk: no herdr-desk on your PATH, so I installed it to $install_dir/herdr-desk."
  case ":$PATH:" in
    *":$install_dir:"*) ;;
    *) echo "herdr-desk: add $install_dir to your PATH to run it." ;;
  esac
}

# claude_step <claude> <argument> <step words...> runs `claude plugin <step words> <argument>` with stdin
# from /dev/null, so a prompt fails instead of hanging an install that has no terminal. It returns 1,
# naming the step, when the command fails.
claude_step() {
  bin=$1 arg=$2
  shift 2
  rc=0
  "$bin" plugin "$@" "$arg" </dev/null || rc=$?
  if [ "$rc" -eq 0 ]; then return 0; fi
  echo "herdr-desk: Claude Code plugin not installed (claude plugin $* exited $rc; see above). $slash"
  return 1
}

# claude_plugin <claude path or empty> installs the Claude Code plugin through claude's own CLI.
claude_plugin() {
  if [ -z "$1" ]; then
    echo "herdr-desk: no claude on PATH, so the Claude Code plugin was not installed. $slash"
    return 0
  fi
  claude_step "$1" "$repo" marketplace add || return 0
  claude_step "$1" "$plugin" install || return 0
  echo "herdr-desk: Claude Code plugin installed (marketplace $repo, plugin $plugin)."
}

# finish places the binary, runs configure with its report also written to $install_log, and exits 0:
# herdr aborts the whole plugin install on a failed build command, and shows a build command's output
# only when it fails, so the log is where a step that did not finish is reported. The report goes to a new
# 0600 file that is then renamed over $install_log, so a symlink, hard link, or FIFO there is replaced, never
# written through; a folder there is left alone.
finish() {
  install_from_path
  log_dir=$(dirname "$install_log")
  if (umask 077 && mkdir -p "$log_dir") 2>/dev/null && log_tmp=$(mktemp "$log_dir/.install.log.XXXXXX" 2>/dev/null); then
    configure 2>&1 | tee "$log_tmp"
    if [ ! -d "$install_log" ]; then
      mv -f "$log_tmp" "$install_log" 2>/dev/null
    fi
  else
    configure
  fi
  exit 0
}

# configure runs setup with the herdr-desk the plugin's commands will run (the one on PATH, else the
# copy just placed), then installs the Claude Code plugin.
configure() {
  desk=$(command -v herdr-desk 2>/dev/null || true)
  if [ -z "$desk" ]; then desk="$install_dir/herdr-desk"; fi
  claude=$(command -v claude 2>/dev/null || true)
  if [ -n "$claude" ]; then
    echo "herdr-desk: claude found at $claude; setup uses the claude-code profile."
    set -- setup --profile claude-code
  else
    echo "herdr-desk: no claude on PATH; setup uses no profile. To start another agent, set [agent] in the herdr-desk config named below (README: herdr with another agent)."
    set -- setup
  fi
  rc=0
  "$desk" "$@" </dev/null || rc=$?
  if [ "$rc" -ne 0 ]; then
    echo "herdr-desk: setup did not finish (exit $rc; see above). After fixing it, run: herdr-desk $*"
  fi
  claude_plugin "$claude"
}

build_from_source() {
  if ! have "$go_cmd"; then
    echo "herdr-desk: no release to install and no go found ($go_cmd). Install Go from https://go.dev/dl, then run: herdr plugin install $repo" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$out")" || exit 1
  # The "+src" suffix marks a source build, so `herdr-desk version` shows which install this is.
  ldflags="-X github.com/federbenjamin/herdr-desk/internal/version.Version=${version:-dev}+src"
  if ! (cd "$repo_root" && CGO_ENABLED=0 "$go_cmd" build -ldflags "$ldflags" -o "$out" ./cmd/herdr-desk); then
    echo "herdr-desk: go build failed" >&2
    exit 1
  fi
  echo "herdr-desk: built from source at $out."
  finish
}

fallback() {
  echo "herdr-desk: $1; building from source instead." >&2
  build_from_source
}

download() { # download <url> <dest>
  have curl || return 127
  curl -fsSL -o "$2" "$1"
}

sha256_of() { # prints the hex digest of file $1
  if have sha256sum; then
    sha256sum "$1" | awk '{print $1}'
  elif have shasum; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    return 127
  fi
}

if [ -z "$version" ]; then
  version=$(sed -n 's/^version *= *"\([^"]*\)".*/\1/p' "$repo_root/herdr-plugin.toml" 2>/dev/null | head -n 1)
fi
[ -n "$version" ] || fallback "could not read the version from herdr-plugin.toml"

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) fallback "no release for $(uname -s)" ;;
esac
case "$(uname -m)" in
  arm64 | aarch64) arch=arm64 ;;
  x86_64 | amd64) arch=amd64 ;;
  *) fallback "no release for $(uname -m)" ;;
esac

asset="herdr-desk_${version}_${os}_${arch}.tar.gz"
tmpdir=$(mktemp -d 2>/dev/null) || fallback "could not create a temp dir"

download "$base_url/v$version/$asset" "$tmpdir/$asset" || fallback "no release archive for v$version ($asset)"
download "$base_url/v$version/checksums.txt" "$tmpdir/checksums.txt" || fallback "no checksums.txt for v$version"

expected=$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1; exit }' "$tmpdir/checksums.txt")
[ -n "$expected" ] || fallback "checksums.txt does not list $asset"

actual=$(sha256_of "$tmpdir/$asset") || fallback "no sha256sum or shasum to verify the download"
[ "$actual" = "$expected" ] || fallback "checksum mismatch for $asset"

tar -xzf "$tmpdir/$asset" -C "$tmpdir" herdr-desk 2>/dev/null || fallback "the archive has no herdr-desk binary"
chmod +x "$tmpdir/herdr-desk"
mkdir -p "$(dirname "$out")" || fallback "could not create $(dirname "$out")"
mv -f "$tmpdir/herdr-desk" "$out" || fallback "could not place the binary at $out"

echo "herdr-desk: installed prebuilt v$version ($os/$arch), checksum verified."
finish
