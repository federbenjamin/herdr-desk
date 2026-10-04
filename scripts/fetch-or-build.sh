#!/bin/sh
# fetch-or-build.sh: herdr [[build]] step for desk.
#
# Downloads the release archive for the version herdr-plugin.toml declares and this platform,
# verifies its SHA-256 against checksums.txt, and places the binary at $DESK_OUT. On any miss
# (no release, download error, checksum mismatch, unmapped platform) it builds from source with
# Go instead, with the version set to <version>+src. When no `desk` is on PATH it also copies the
# binary to $DESK_INSTALL_DIR.
#
# Overrides, for tests: DESK_REPO_ROOT, DESK_VERSION, DESK_BASE_URL (the folder holding
# v<version>/), DESK_OUT, DESK_INSTALL_DIR, DESK_GO.
set -u

repo="federbenjamin/desk"

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root="${DESK_REPO_ROOT:-$script_dir/..}"
base_url="${DESK_BASE_URL:-https://github.com/$repo/releases/download}"
out="${DESK_OUT:-$repo_root/bin/desk}"
install_dir="${DESK_INSTALL_DIR:-$HOME/.local/bin}"
go_cmd="${DESK_GO:-go}"
version="${DESK_VERSION:-}"
tmpdir=""

have() { command -v "$1" >/dev/null 2>&1; }

trap 'if [ -n "$tmpdir" ]; then rm -rf "$tmpdir"; fi' EXIT

# install_from_path makes the binary at $out available as `desk` when none is on PATH, or exits 1
# naming the place it could not write.
install_from_path() {
  if have desk; then
    return 0
  fi
  if ! mkdir -p "$install_dir" || ! cp -f "$out" "$install_dir/desk"; then
    echo "desk: no desk on your PATH, and it could not be installed to $install_dir/desk; copy $out onto your PATH" >&2
    exit 1
  fi
  echo "desk: no desk on your PATH, so I installed it to $install_dir/desk."
  case ":$PATH:" in
    *":$install_dir:"*) ;;
    *) echo "desk: add $install_dir to your PATH to run it." ;;
  esac
}

build_from_source() {
  if ! have "$go_cmd"; then
    echo "desk: no release to install and no go found ($go_cmd). Install Go from https://go.dev/dl, then run: herdr plugin install $repo" >&2
    exit 1
  fi
  mkdir -p "$(dirname "$out")" || exit 1
  # The "+src" suffix marks a source build, so `desk version` shows which install this is.
  ldflags="-X github.com/federbenjamin/desk/internal/version.Version=${version:-dev}+src"
  if ! (cd "$repo_root" && CGO_ENABLED=0 "$go_cmd" build -ldflags "$ldflags" -o "$out" ./cmd/desk); then
    echo "desk: go build failed" >&2
    exit 1
  fi
  echo "desk: built from source at $out."
  install_from_path
  exit 0
}

fallback() {
  echo "desk: $1; building from source instead." >&2
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

asset="desk_${version}_${os}_${arch}.tar.gz"
tmpdir=$(mktemp -d 2>/dev/null) || fallback "could not create a temp dir"

download "$base_url/v$version/$asset" "$tmpdir/$asset" || fallback "no release archive for v$version ($asset)"
download "$base_url/v$version/checksums.txt" "$tmpdir/checksums.txt" || fallback "no checksums.txt for v$version"

expected=$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1; exit }' "$tmpdir/checksums.txt")
[ -n "$expected" ] || fallback "checksums.txt does not list $asset"

actual=$(sha256_of "$tmpdir/$asset") || fallback "no sha256sum or shasum to verify the download"
[ "$actual" = "$expected" ] || fallback "checksum mismatch for $asset"

tar -xzf "$tmpdir/$asset" -C "$tmpdir" desk 2>/dev/null || fallback "the archive has no desk binary"
chmod +x "$tmpdir/desk"
mkdir -p "$(dirname "$out")" || fallback "could not create $(dirname "$out")"
mv -f "$tmpdir/desk" "$out" || fallback "could not place the binary at $out"

echo "desk: installed prebuilt v$version ($os/$arch), checksum verified."
install_from_path
exit 0
