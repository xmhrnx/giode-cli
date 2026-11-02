#!/bin/sh
# Installs the giode CLI on Linux and macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/xmhrnx/giode-cli/main/install.sh | sh
#
# It downloads the latest release (or $GIODE_VERSION, like v0.2.0) into
# $GIODE_INSTALL_DIR, /usr/local/bin when writable and ~/.local/bin
# otherwise. $GIODE_DOWNLOAD_URL replaces the GitHub release URL (a
# mirror, or a local copy of the release files).
set -eu

repo="xmhrnx/giode-cli"

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) echo "giode: unsupported system $(uname -s); on Windows use install.ps1" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) echo "giode: unsupported processor $(uname -m)" >&2; exit 1 ;;
esac

version="${GIODE_VERSION:-}"
if [ -z "$version" ] && [ -z "${GIODE_DOWNLOAD_URL:-}" ]; then
  # The newest release with archives, pre-releases included: GitHub's
  # releases/latest leaves those out, and giode's are alphas for now.
  version=$(curl -fsSL "https://api.github.com/repos/$repo/releases?per_page=20" |
    awk -v want="giode_${os}_${arch}.tar.gz" '
      /"tag_name":/ { gsub(/.*"tag_name": *"|".*/, ""); tag = $0 }
      /"name":/ && index($0, want) && tag != "" { print tag; exit }')
  if [ -z "$version" ]; then
    echo "giode: no release for ${os}/${arch} is published yet; install with: go install github.com/xmhrnx/giode-cli/cmd/giode@latest" >&2
    exit 1
  fi
fi
url="https://github.com/$repo/releases/download/$version/giode_${os}_${arch}.tar.gz"

if [ -n "${GIODE_DOWNLOAD_URL:-}" ]; then
  url="${GIODE_DOWNLOAD_URL%/}/giode_${os}_${arch}.tar.gz"
fi

dir="${GIODE_INSTALL_DIR:-}"
if [ -z "$dir" ]; then
  if [ -w /usr/local/bin ]; then dir=/usr/local/bin; else dir="$HOME/.local/bin"; fi
fi
mkdir -p "$dir"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "Downloading $url"
curl -fsSL "$url" -o "$tmp/giode.tar.gz"
tar -xzf "$tmp/giode.tar.gz" -C "$tmp"
install -m 0755 "$tmp/giode_${os}_${arch}/giode" "$dir/giode"
echo "Installed $("$dir/giode" version) to $dir/giode"

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "Add $dir to your PATH, e.g. in ~/.profile:  export PATH=\"$dir:\$PATH\"" ;;
esac
command -v go >/dev/null 2>&1 || echo "giode builds apps with Go 1.26 or later: install it from https://go.dev/dl"
