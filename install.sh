#!/bin/bash
set -euo pipefail

REPO="volodymyrsmirnov/choix"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="choix"

info() { printf '\033[1;34m%s\033[0m\n' "$*"; }
error() { printf '\033[1;31mError: %s\033[0m\n' "$*" >&2; exit 1; }

detect_asset() {
    local os
    os="$(uname -s)"

    case "$os" in
        Darwin)
            echo "choix-osx-universal"
            ;;
        *)
            error "choix only supports macOS. Detected OS: $os"
            ;;
    esac
}

get_latest_version() {
    local url="https://api.github.com/repos/${REPO}/releases/latest"
    if command -v curl &>/dev/null; then
        curl -fsSL "$url" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/'
    elif command -v wget &>/dev/null; then
        wget -qO- "$url" | grep '"tag_name"' | sed -E 's/.*"([^"]+)".*/\1/'
    else
        error "curl or wget is required"
    fi
}

download() {
    local url="$1" dest="$2"
    if command -v curl &>/dev/null; then
        curl -fsSL -o "$dest" "$url"
    elif command -v wget &>/dev/null; then
        wget -qO "$dest" "$url"
    else
        error "curl or wget is required"
    fi
}

main() {
    local version="${1:-}"
    local asset download_url tmpfile

    asset="$(detect_asset)"
    info "Detected platform: $asset"

    if [ -z "$version" ]; then
        version="$(get_latest_version)"
    fi
    info "Installing choix $version"

    download_url="https://github.com/${REPO}/releases/download/${version}/${asset}"

    tmpfile="$(mktemp)"
    trap 'rm -f "${tmpfile:-}"' EXIT

    info "Downloading $download_url"
    download "$download_url" "$tmpfile"

    info "Installing to ${INSTALL_DIR}/${BINARY_NAME}"
    chmod +x "$tmpfile"

    if [ -w "$INSTALL_DIR" ]; then
        mv "$tmpfile" "${INSTALL_DIR}/${BINARY_NAME}"
    else
        sudo mv "$tmpfile" "${INSTALL_DIR}/${BINARY_NAME}"
    fi

    # Remove quarantine attribute on macOS so the unsigned binary runs.
    info "Removing quarantine attribute"
    if [ -w "${INSTALL_DIR}/${BINARY_NAME}" ]; then
        xattr -d com.apple.quarantine "${INSTALL_DIR}/${BINARY_NAME}" 2>/dev/null || true
    else
        sudo xattr -d com.apple.quarantine "${INSTALL_DIR}/${BINARY_NAME}" 2>/dev/null || true
    fi

    info "choix $version installed successfully!"
    "${INSTALL_DIR}/${BINARY_NAME}" --version
}

main "$@"
