#!/usr/bin/env bash
set -euo pipefail

# ─── Configuration ───────────────────────────────────────────────────────────
REPO="papesambandour/segura-cli"
INSTALL_DIR="$HOME/.local/bin"
BINARY="segura"

# ─── Helpers ─────────────────────────────────────────────────────────────────
info()  { printf "\033[1;34m==>\033[0m %s\n" "$*" >&2; }
ok()    { printf "\033[1;32m==>\033[0m %s\n" "$*" >&2; }
err()   { printf "\033[1;31mERR\033[0m %s\n" "$*" >&2; exit 1; }

is_interactive() {
    # Returns true when run as `bash install.sh`, false when piped
    [ -t 0 ]
}

# ─── Detect OS and Architecture ──────────────────────────────────────────────
detect_platform() {
    local uname_os uname_arch
    uname_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    uname_arch="$(uname -m)"

    case "$uname_os" in
        linux)  OS="linux" ;;
        darwin) OS="darwin" ;;
        *)      err "Unsupported OS: $uname_os (only linux and darwin are supported)" ;;
    esac

    case "$uname_arch" in
        x86_64|amd64)   ARCH="amd64" ;;
        arm64|aarch64)  ARCH="arm64" ;;
        *)              err "Unsupported architecture: $uname_arch (only amd64 and arm64 are supported)" ;;
    esac

    info "Detected platform: ${OS}/${ARCH}"
}

# ─── Fetch latest version from GitHub ────────────────────────────────────────
fetch_latest_version() {
    info "Fetching latest release from GitHub..."

    if command -v curl &>/dev/null; then
        VERSION=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" \
            | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    elif command -v wget &>/dev/null; then
        VERSION=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" \
            | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\([^"]*\)".*/\1/')
    else
        err "Neither curl nor wget found. Please install one of them."
    fi

    if [ -z "$VERSION" ]; then
        err "Could not determine latest version. Check that ${REPO} has GitHub releases."
    fi

    info "Latest version: ${VERSION}"
}

# ─── Download binary ─────────────────────────────────────────────────────────
DOWNLOAD_TMP=""
download_binary() {
    local url="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}-${OS}-${ARCH}"
    DOWNLOAD_TMP="$(mktemp)"

    info "Downloading ${BINARY}-${OS}-${ARCH} (${VERSION})..."

    if command -v curl &>/dev/null; then
        curl -fSL --progress-bar -o "$DOWNLOAD_TMP" "$url" || err "Download failed. Check that the release asset exists: $url"
    else
        wget -q --show-progress -O "$DOWNLOAD_TMP" "$url" || err "Download failed. Check that the release asset exists: $url"
    fi
}

# ─── Install binary ──────────────────────────────────────────────────────────
install_binary() {
    local tmp_file="$1"

    mkdir -p "$INSTALL_DIR"
    mv "$tmp_file" "${INSTALL_DIR}/${BINARY}"
    chmod +x "${INSTALL_DIR}/${BINARY}"

    ok "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"
}

# ─── Ensure ~/.local/bin is in PATH ──────────────────────────────────────────
ensure_path() {
    if echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
        return
    fi

    local line='export PATH="$HOME/.local/bin:$PATH"'

    # Detect shell RC file
    local rc_file=""
    case "${SHELL:-}" in
        */zsh)  rc_file="$HOME/.zshrc" ;;
        */bash) rc_file="$HOME/.bashrc" ;;
        *)
            # Fallback: try both
            if [ -f "$HOME/.zshrc" ]; then
                rc_file="$HOME/.zshrc"
            elif [ -f "$HOME/.bashrc" ]; then
                rc_file="$HOME/.bashrc"
            fi
            ;;
    esac

    if [ -n "$rc_file" ]; then
        if ! grep -qF '.local/bin' "$rc_file" 2>/dev/null; then
            echo "" >> "$rc_file"
            echo "# Added by segura installer" >> "$rc_file"
            echo "$line" >> "$rc_file"
            info "Added ~/.local/bin to PATH in ${rc_file}"
        fi
    fi

    # Export for current session
    export PATH="$INSTALL_DIR:$PATH"
}

# ─── Install shell completion ────────────────────────────────────────────────
install_completion() {
    if "${INSTALL_DIR}/${BINARY}" install-completion >/dev/null 2>&1; then
        ok "Shell completion installed (restart your shell to activate)"
    else
        info "Shell completion skipped — run 'segura install-completion' manually if wanted"
    fi
}

# ─── Interactive: prompt for env vars ────────────────────────────────────────
configure_env() {
    if ! is_interactive; then
        echo ""
        info "Running in pipe mode — skipping interactive configuration."
        echo ""
        echo "  Configure these environment variables before using segura:"
        echo ""
        echo "    export SEGURA_URL=\"https://your-senhasegura-host\""
        echo "    export SEGURA_USER=\"your-username\""
        echo "    export SEGURA_PASSWORD=\"your-password\""
        echo "    export SEGURA_MFA_TOKEN=\"your-totp-secret\""
        echo "    export SEGURA_TENANT=\"your-tenant\""
        echo ""
        return
    fi

    echo ""
    info "Interactive setup — configure senhasegura connection."
    echo "  (Press Enter to skip any variable)"
    echo ""

    local vars=("SEGURA_URL" "SEGURA_USER" "SEGURA_PASSWORD" "SEGURA_MFA_TOKEN" "SEGURA_TENANT")
    local descs=(
        "senhasegura host URL (e.g. https://pam.example.com)"
        "senhasegura username"
        "senhasegura password"
        "TOTP secret key (base32)"
        "senhasegura tenant name"
    )

    # Detect shell RC file
    local rc_file=""
    case "${SHELL:-}" in
        */zsh)  rc_file="$HOME/.zshrc" ;;
        */bash) rc_file="$HOME/.bashrc" ;;
        *)
            if [ -f "$HOME/.zshrc" ]; then
                rc_file="$HOME/.zshrc"
            elif [ -f "$HOME/.bashrc" ]; then
                rc_file="$HOME/.bashrc"
            fi
            ;;
    esac

    local wrote_any=false

    for i in "${!vars[@]}"; do
        local var="${vars[$i]}"
        local desc="${descs[$i]}"
        printf "  %s (%s): " "$var" "$desc"
        read -r val
        if [ -n "$val" ] && [ -n "$rc_file" ]; then
            # Remove existing export if present
            if grep -q "^export ${var}=" "$rc_file" 2>/dev/null; then
                sed -i'' -e "/^export ${var}=/d" "$rc_file"
            fi
            echo "export ${var}=\"${val}\"" >> "$rc_file"
            wrote_any=true
        fi
    done

    if [ "$wrote_any" = true ] && [ -n "$rc_file" ]; then
        echo ""
        ok "Environment variables written to ${rc_file}"
        info "Run: source ${rc_file}"
    fi
}

# ─── Main ────────────────────────────────────────────────────────────────────
main() {
    echo ""
    echo "  ╔═══════════════════════════════════╗"
    echo "  ║     SEGURA-CLI Installer          ║"
    echo "  ╚═══════════════════════════════════╝"
    echo ""

    detect_platform
    fetch_latest_version

    download_binary
    install_binary "$DOWNLOAD_TMP"
    ensure_path
    install_completion
    configure_env

    echo ""
    echo "  ─────────────────────────────────────"
    ok "Installation complete!"
    echo ""
    echo "  Quick start:"
    echo "    segura list                       # list available credentials"
    echo "    segura connect user@device        # SSH terminal"
    echo "    segura connect user@device --browser  # web terminal"
    echo "    segura --restart --port 8080      # start web server"
    echo ""
    echo "  Documentation: https://github.com/${REPO}"
    echo ""
}

main "$@"
