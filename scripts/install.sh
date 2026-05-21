#!/bin/sh
# install.sh — install the latchet CLI on Linux or macOS.
#
# One-liner:
#   curl -sSL https://raw.githubusercontent.com/thowd22/latchet/main/scripts/install.sh | sh
#
# Optional environment variables:
#   LATCHET_VERSION       release tag to install (default: latest)
#   LATCHET_INSTALL_DIR   target directory (default: /usr/local/bin if writable,
#                         else $HOME/.local/bin)

set -eu

REPO="thowd22/latchet"
VERSION="${LATCHET_VERSION:-latest}"

die() {
    printf 'install.sh: %s\n' "$*" >&2
    exit 1
}

# Pick downloader.
if command -v curl >/dev/null 2>&1; then
    fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
    fetch() { wget -qO- "$1"; }
else
    die "neither curl nor wget found"
fi

# Detect OS.
case "$(uname -s)" in
    Linux)  os=linux ;;
    Darwin) os=darwin ;;
    *)      die "unsupported OS: $(uname -s)" ;;
esac

# Detect arch.
case "$(uname -m)" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *)             die "unsupported architecture: $(uname -m)" ;;
esac

# Resolve version.
if [ "$VERSION" = "latest" ]; then
    api="https://api.github.com/repos/${REPO}/releases/latest"
    VERSION=$(fetch "$api" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
    [ -n "$VERSION" ] || die "could not determine latest release tag"
fi

# Pick install dir.
if [ -n "${LATCHET_INSTALL_DIR:-}" ]; then
    dest_dir="$LATCHET_INSTALL_DIR"
elif [ -w /usr/local/bin ]; then
    dest_dir="/usr/local/bin"
else
    dest_dir="$HOME/.local/bin"
fi
mkdir -p "$dest_dir"

# Pick SHA256 tool.
if command -v sha256sum >/dev/null 2>&1; then
    sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
    sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
    die "no SHA256 tool found (need sha256sum or shasum)"
fi

archive="latchet-${VERSION}-${os}-${arch}.tar.gz"
url_base="https://github.com/${REPO}/releases/download/${VERSION}"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

printf '==> downloading %s\n' "$archive"
fetch "${url_base}/${archive}" > "${tmp}/${archive}"
printf '==> downloading SHA256SUMS\n'
fetch "${url_base}/SHA256SUMS" > "${tmp}/SHA256SUMS"

expected=$(awk -v f="$archive" '$2 == f {print $1}' "${tmp}/SHA256SUMS")
[ -n "$expected" ] || die "no checksum entry for $archive in SHA256SUMS"
got=$(sha256 "${tmp}/${archive}")
[ "$expected" = "$got" ] || die "checksum mismatch: expected $expected, got $got"

tar -xzf "${tmp}/${archive}" -C "$tmp"
mv "${tmp}/latchet" "${dest_dir}/latchet"
chmod +x "${dest_dir}/latchet"

printf '\ninstalled: %s\n' "${dest_dir}/latchet"
case ":$PATH:" in
    *":${dest_dir}:"*) ;;
    *)
        printf '\nnote: %s is not in your $PATH\n' "$dest_dir"
        printf '      add to your shell rc:  export PATH="%s:$PATH"\n' "$dest_dir"
        ;;
esac

printf '\n'
"${dest_dir}/latchet" -version
