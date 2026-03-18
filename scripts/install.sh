#!/bin/sh
# Amurg install script — downloads a pre-built binary from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh -s -- --binary=amurg-hub
#   curl -fsSL https://raw.githubusercontent.com/amurg-ai/amurg/main/scripts/install.sh | sh -s -- --version=0.1.0
#
set -eu

REPO="amurg-ai/amurg"
BINARY="amurg-runtime"
VERSION=""
INSTALL_DIR=""
TMP_DIR=""

# ── Helpers ──────────────────────────────────────────────────────────────────

info()  { printf '  \033[1;34m→\033[0m %s\n' "$*"; }
ok()    { printf '  \033[1;32m✓\033[0m %s\n' "$*"; }
err()   { printf '  \033[1;31m✗\033[0m %s\n' "$*" >&2; }
fatal() { err "$@"; exit 1; }

need_cmd() {
    command -v "$1" > /dev/null 2>&1 || fatal "Required command not found: $1"
}

has_tty() {
    [ -r /dev/tty ] && [ -w /dev/tty ]
}

prompt_yes_no() {
    prompt="$1"
    default="${2:-n}"

    if ! has_tty; then
        return 1
    fi

    while :; do
        if [ "$default" = "y" ]; then
            suffix="[Y/n]"
        else
            suffix="[y/N]"
        fi
        printf '%s %s ' "$prompt" "$suffix" > /dev/tty
        if ! IFS= read -r reply < /dev/tty; then
            return 1
        fi
        case "$reply" in
            "")
                if [ "$default" = "y" ]; then
                    return 0
                fi
                return 1
                ;;
            y|Y|yes|Yes|YES)
                return 0
                ;;
            n|N|no|No|NO)
                return 1
                ;;
        esac
    done
}

# download URL DEST — uses curl or wget
download() {
    if command -v curl > /dev/null 2>&1; then
        curl -fsSL -o "$2" "$1"
    elif command -v wget > /dev/null 2>&1; then
        wget -qO "$2" "$1"
    else
        fatal "Neither curl nor wget found. Install one and try again."
    fi
}

# ── Detection ────────────────────────────────────────────────────────────────

detect_os() {
    case "$(uname -s)" in
        Linux*)  OS="linux" ;;
        Darwin*) OS="darwin" ;;
        *)       fatal "Unsupported OS: $(uname -s). Only Linux and macOS are supported." ;;
    esac
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)     ARCH="amd64" ;;
        aarch64|arm64)    ARCH="arm64" ;;
        *)                fatal "Unsupported architecture: $(uname -m). Only amd64 and arm64 are supported." ;;
    esac
}

detect_install_dir() {
    if [ -n "$INSTALL_DIR" ]; then
        return
    fi
    if [ -w /usr/local/bin ]; then
        INSTALL_DIR="/usr/local/bin"
    elif [ "$(id -u)" = "0" ]; then
        INSTALL_DIR="/usr/local/bin"
    else
        INSTALL_DIR="$HOME/.local/bin"
        mkdir -p "$INSTALL_DIR"
    fi
}

detect_tmux_install_command() {
    TMUX_INSTALL_CMD=""
    TMUX_INSTALL_DISPLAY=""

    if [ "$OS" = "darwin" ]; then
        if command -v brew > /dev/null 2>&1; then
            TMUX_INSTALL_CMD="brew install tmux"
            TMUX_INSTALL_DISPLAY="$TMUX_INSTALL_CMD"
            return 0
        fi
        return 1
    fi

    if command -v apt-get > /dev/null 2>&1; then
        TMUX_INSTALL_CMD="apt-get install -y tmux"
    elif command -v dnf > /dev/null 2>&1; then
        TMUX_INSTALL_CMD="dnf install -y tmux"
    elif command -v yum > /dev/null 2>&1; then
        TMUX_INSTALL_CMD="yum install -y tmux"
    elif command -v pacman > /dev/null 2>&1; then
        TMUX_INSTALL_CMD="pacman -Sy --noconfirm tmux"
    elif command -v apk > /dev/null 2>&1; then
        TMUX_INSTALL_CMD="apk add tmux"
    elif command -v zypper > /dev/null 2>&1; then
        TMUX_INSTALL_CMD="zypper --non-interactive install tmux"
    else
        return 1
    fi

    if [ "$(id -u)" != "0" ]; then
        if command -v sudo > /dev/null 2>&1; then
            TMUX_INSTALL_CMD="sudo ${TMUX_INSTALL_CMD}"
        else
            return 1
        fi
    fi

    TMUX_INSTALL_DISPLAY="$TMUX_INSTALL_CMD"
    return 0
}

print_tmux_manual_hint() {
    if [ "$OS" = "darwin" ]; then
        info "Install tmux later with: brew install tmux"
    else
        info "Install tmux later with your package manager, for example: sudo apt-get install -y tmux"
    fi
    info 'You can also rerun `amurg-runtime init` and it will prompt again when you choose interactive tmux.'
}

offer_tmux_install() {
    if [ "${BINARY}" != "amurg-runtime" ] || command -v tmux > /dev/null 2>&1; then
        return 0
    fi

    info 'Optional: tmux enables codex.transport="tmux" and other native interactive transports'

    if ! has_tty; then
        info 'No interactive terminal detected. Install tmux later, or run `amurg-runtime init` to be prompted again.'
        echo ""
        return 0
    fi

    if ! detect_tmux_install_command; then
        info "Couldn't determine an automatic tmux install command for this host."
        print_tmux_manual_hint
        echo ""
        return 0
    fi

    if ! prompt_yes_no "Install tmux now for interactive sessions?" y; then
        info "Skipping tmux install."
        print_tmux_manual_hint
        echo ""
        return 0
    fi

    info "Installing tmux using: ${TMUX_INSTALL_DISPLAY}"
    if sh -c "${TMUX_INSTALL_CMD}" < /dev/tty; then
        if command -v tmux > /dev/null 2>&1; then
            ok "tmux installed successfully"
        else
            err "tmux install command finished, but tmux is still not on your PATH."
            print_tmux_manual_hint
        fi
    else
        err "tmux installation failed."
        print_tmux_manual_hint
    fi
    echo ""
}

# ── Version ──────────────────────────────────────────────────────────────────

get_latest_version() {
    if [ -n "$VERSION" ]; then
        return
    fi

    info "Fetching latest version..."
    local url="https://api.github.com/repos/${REPO}/releases/latest"
    local tmp
    tmp=$(mktemp)

    download "$url" "$tmp" || fatal "Failed to fetch latest release info from GitHub."

    # Extract tag_name without jq — look for "tag_name": "v..."
    VERSION=$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\([^"]*\)".*/\1/p' "$tmp" | head -1)
    rm -f "$tmp"

    if [ -z "$VERSION" ]; then
        fatal "Could not determine latest version. Specify one with --version=X.Y.Z"
    fi
}

# ── Install ──────────────────────────────────────────────────────────────────

do_install() {
    local base_url="https://github.com/${REPO}/releases/download/v${VERSION}"
    local archive="${BINARY}_${VERSION}_${OS}_${ARCH}.tar.gz"
    local archive_url="${base_url}/${archive}"
    local checksum_url="${base_url}/checksums.txt"

    TMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_DIR"' EXIT

    info "Downloading ${BINARY} v${VERSION} for ${OS}/${ARCH}..."
    download "$archive_url" "${TMP_DIR}/${archive}" \
        || fatal "Download failed. Check that v${VERSION} exists at https://github.com/${REPO}/releases"

    info "Verifying checksum..."
    download "$checksum_url" "${TMP_DIR}/checksums.txt" \
        || fatal "Failed to download checksums."

    local expected
    expected=$(grep "${archive}" "${TMP_DIR}/checksums.txt" | awk '{print $1}')
    if [ -z "$expected" ]; then
        fatal "Checksum not found for ${archive} in checksums.txt"
    fi

    local actual
    if command -v sha256sum > /dev/null 2>&1; then
        actual=$(sha256sum "${TMP_DIR}/${archive}" | awk '{print $1}')
    elif command -v shasum > /dev/null 2>&1; then
        actual=$(shasum -a 256 "${TMP_DIR}/${archive}" | awk '{print $1}')
    else
        fatal "Neither sha256sum nor shasum found. Cannot verify checksum."
    fi

    if [ "$expected" != "$actual" ]; then
        fatal "Checksum mismatch!\n  Expected: ${expected}\n  Actual:   ${actual}"
    fi

    info "Extracting..."
    tar -xzf "${TMP_DIR}/${archive}" -C "${TMP_DIR}"

    info "Installing to ${INSTALL_DIR}/${BINARY}..."
    install -m 755 "${TMP_DIR}/${BINARY}" "${INSTALL_DIR}/${BINARY}"

    # Warn if install dir is not in PATH.
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) ;;
        *)
            echo ""
            err "${INSTALL_DIR} is not in your PATH."
            info "Add it with:  export PATH=\"${INSTALL_DIR}:\$PATH\""
            echo ""
            ;;
    esac

    echo ""
    ok "${BINARY} v${VERSION} installed to ${INSTALL_DIR}/${BINARY}"
    echo ""
    info "Next steps:"
    echo "    ${BINARY} init      # interactive setup wizard"
    echo "    ${BINARY} run       # start with generated config"
    echo ""

    offer_tmux_install
}

# ── Argument parsing ─────────────────────────────────────────────────────────

parse_args() {
    for arg in "$@"; do
        case "$arg" in
            --binary=*)   BINARY="${arg#*=}" ;;
            --version=*)  VERSION="${arg#*=}" ;;
            --dir=*)      INSTALL_DIR="${arg#*=}" ;;
            --help|-h)
                echo "Usage: install.sh [--binary=amurg-runtime|amurg-hub] [--version=X.Y.Z] [--dir=/path]"
                exit 0
                ;;
            *)
                fatal "Unknown argument: $arg"
                ;;
        esac
    done
}

# ── Main ─────────────────────────────────────────────────────────────────────

main() {
    parse_args "$@"
    detect_os
    detect_arch
    detect_install_dir
    get_latest_version
    do_install
}

main "$@"
