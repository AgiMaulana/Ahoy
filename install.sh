#!/bin/sh
# Ahoy installer
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/AgiMaulana/Ahoy/main/install.sh | sh
#
# Or with a pinned version:
#   curl -fsSL https://raw.githubusercontent.com/AgiMaulana/Ahoy/main/install.sh | sh -s -- v0.0.1
#
# Downloads the latest (or requested) Ahoy release binary for your platform,
# verifies its cosign keyless signature, and installs it to /usr/local/bin
# (or a directory of your choice via AHOY_INSTALL_DIR).
set -eu

REPO="AgiMaulana/Ahoy"
APP="ahoy"
DEFAULT_INSTALL_DIR="/usr/local/bin"

log() { printf '%s\n' "$*" >&2; }
fail() { log "Error: $*"; exit 1; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "'$1' is required but was not found in PATH"
}

need_cmd uname
need_cmd mktemp

# ---- Detect OS / architecture ----------------------------------------------
os=$(uname -s)
arch=$(uname -m)

case "$os" in
  Linux) os_name="linux" ;;
  Darwin) os_name="darwin" ;;
  *)
    # Windows via Git Bash / MSYS2 / Cygwin
    case "$os" in
      MINGW*|MSYS*|CYGWIN*) os_name="windows" ;;
      *) fail "unsupported operating system: $os" ;;
    esac
    ;;
esac

case "$arch" in
  x86_64|amd64) arch_name="amd64" ;;
  aarch64|arm64) arch_name="arm64" ;;
  *) fail "unsupported architecture: $arch" ;;
esac

if [ "$os_name" = "windows" ]; then
  ext=".exe"
else
  ext=""
fi

asset="${APP}-${os_name}-${arch_name}${ext}"

# ---- Resolve version --------------------------------------------------------
version="${1:-}"
if [ -z "$version" ]; then
  log "Looking up the latest ${APP} release..."
  need_cmd curl
  version=$(curl -fsSL -o /dev/null -w '%{url_effective}' \
    "https://github.com/${REPO}/releases/latest" | sed 's|.*/tag/||')
  [ -n "$version" ] || fail "could not determine the latest release version"
fi
case "$version" in
  v*) ;;
  *) version="v${version}" ;;
esac

download_url="https://github.com/${REPO}/releases/download/${version}/${asset}"
log "Installing ${APP} ${version} (${asset})"

# ---- Download ---------------------------------------------------------------
need_cmd curl
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

log "Downloading ${download_url}"
curl -fsSL --retry 3 -o "${tmp_dir}/${asset}" "$download_url" \
  || fail "download failed — does release ${version} exist and contain ${asset}?"

# ---- Verify cosign signature (keyless) --------------------------------------
if command -v cosign >/dev/null 2>&1; then
  sig_url="${download_url}.sig"
  cert_url="${download_url}.pem"
  log "Verifying signature with cosign..."
  curl -fsSL --retry 3 -o "${tmp_dir}/${asset}.sig" "$sig_url" \
    || fail "could not download signature ${sig_url}"
  curl -fsSL --retry 3 -o "${tmp_dir}/${asset}.pem" "$cert_url" \
    || fail "could not download certificate ${cert_url}"
  cosign verify-blob \
    --signature "${tmp_dir}/${asset}.sig" \
    --certificate "${tmp_dir}/${asset}.pem" \
    --certificate-identity-regexp \
      "^https://github.com/${REPO}/\.github/workflows/release\.yml@refs/tags/v[0-9]+\.[0-9]+\.[0-9]+$" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "${tmp_dir}/${asset}" \
    || fail "signature verification failed"
  log "Signature verified."
else
  log "cosign not found — skipping signature verification."
  log "Install it from https://github.com/sigstore/cosign for verified installs."
fi

# ---- Install -----------------------------------------------------------------
install_dir="${AHOY_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"

# Create the install directory if it does not exist yet
if [ ! -d "$install_dir" ]; then
  mkdir -p "$install_dir" 2>/dev/null || true
fi

if [ ! -w "$install_dir" ]; then
  # Try sudo -p prints the prompt; -v validates cached credentials first.
  if command -v sudo >/dev/null 2>&1 && sudo -v 2>/dev/null; then
    log "Need permission to write to ${install_dir}, using sudo..."
    sudo mkdir -p "$install_dir"
    sudo install -m 0755 "${tmp_dir}/${asset}" "${install_dir}/${APP}${ext}"
  else
    fail "cannot write to ${install_dir}. Re-run with: AHOY_INSTALL_DIR=\$HOME/.local/bin sh install.sh"
  fi
else
  install -m 0755 "${tmp_dir}/${asset}" "${install_dir}/${APP}${ext}"
fi

# ---- Done --------------------------------------------------------------------
bin_path="${install_dir}/${APP}${ext}"
log ""
log "${APP} ${version} was installed to ${bin_path}"

if command -v "$APP" >/dev/null 2>&1; then
  log "Run 'ahoy version' to get started."
else
  log "NOTE: ${install_dir} is not in your PATH."
  log "Add it to your shell profile, e.g.:"
  log "  export PATH=\"${install_dir}:\$PATH\""
fi
