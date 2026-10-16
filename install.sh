#!/bin/sh
# Installs the agtop binary from the GitHub releases (macOS and Linux, amd64 and arm64).
#
#   curl -fsSL https://raw.githubusercontent.com/danielfnfaria/agtop/main/install.sh | sh
#
# Environment:
#   AGTOP_VERSION      version to install, e.g. 0.1.0 (default: the latest release)
#   AGTOP_INSTALL_DIR  directory for the binary (default: ~/.local/bin)
#   AGTOP_RELEASES_URL base URL of the releases (default: GitHub; useful for mirrors and tests)
set -eu

# Everything runs from main, called on the last line, so a truncated download runs nothing.
main() {
  releases="${AGTOP_RELEASES_URL:-https://github.com/danielfnfaria/agtop/releases}"
  install_dir="${AGTOP_INSTALL_DIR:-$HOME/.local/bin}"

  need curl
  need tar
  need uname
  need mktemp

  os="$(detect_os)"
  arch="$(detect_arch)"
  version="$(resolve_version)"
  archive="agtop_${version}_${os}_${arch}.tar.gz"

  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT INT TERM

  say "downloading agtop $version ($os/$arch)"
  fetch "$releases/download/v$version/$archive" "$tmp/$archive"
  fetch "$releases/download/v$version/checksums.txt" "$tmp/checksums.txt"
  verify "$tmp" "$archive"

  tar -xzf "$tmp/$archive" -C "$tmp" agtop
  mkdir -p "$install_dir"
  # Install through a temporary name so a running agtop is never left half-written.
  cp "$tmp/agtop" "$install_dir/.agtop.new"
  chmod 755 "$install_dir/.agtop.new"
  mv -f "$install_dir/.agtop.new" "$install_dir/agtop"

  say "installed $install_dir/agtop"
  case ":$PATH:" in
    *":$install_dir:"*) ;;
    *) say "note: $install_dir is not in your PATH; add it to your shell profile:" \
      && say "  export PATH=\"$install_dir:\$PATH\"" ;;
  esac
  say "run 'agtop setup' to show the 5h/7d plan usage"
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    *) die "unsupported OS: $(uname -s) (agtop supports macOS and Linux)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) echo amd64 ;;
    arm64 | aarch64) echo arm64 ;;
    *) die "unsupported architecture: $(uname -m) (agtop supports amd64 and arm64)" ;;
  esac
}

# Prints the version without the leading "v". The latest one comes from the redirect of
# releases/latest, which avoids the GitHub API and its rate limit.
resolve_version() {
  if [ -n "${AGTOP_VERSION:-}" ]; then
    echo "${AGTOP_VERSION#v}"
    return
  fi
  url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$releases/latest")" ||
    die "could not reach $releases/latest"
  tag="${url##*/}"
  case "$tag" in
    v[0-9]*) echo "${tag#v}" ;;
    *) die "could not find the latest release (got $url)" ;;
  esac
}

# Also checks the file: some curl releases exit 0 on an HTTP error when --retry is set.
fetch() {
  curl -fsSL --retry 3 -o "$2" "$1" && [ -s "$2" ] || die "download failed: $1"
}

verify() {
  expected="$(awk -v f="$2" '$2 == f { print $1 }' "$1/checksums.txt")"
  [ -n "$expected" ] || die "$2 is not listed in checksums.txt"
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$1/$2" | awk '{ print $1 }')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$1/$2" | awk '{ print $1 }')"
  else
    die "sha256sum or shasum is required to verify the download"
  fi
  [ "$expected" = "$actual" ] || die "checksum mismatch for $2"
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

say() {
  echo "agtop: $*"
}

die() {
  echo "agtop: error: $*" >&2
  exit 1
}

main "$@"
