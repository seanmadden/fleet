#!/usr/bin/env bash
# fleet local-dev installer
#
# Builds fleet from the current source tree and installs it to ~/.local/bin
# (or a directory of your choosing) so you can run `fleet` from anywhere on
# your fork. By default the install is a symlink into the repo's build/
# directory — re-running `make build` after editing source instantly updates
# the installed binary without re-running this script.
#
# Usage:
#   ./install-dev.sh                     # symlink to ~/.local/bin/fleet
#   ./install-dev.sh --copy              # copy the binary instead of symlinking
#   ./install-dev.sh --dir /usr/local/bin
#   ./install-dev.sh --name fleet-dev    # install under a different name
#   ./install-dev.sh --force             # overwrite an existing install without asking

set -euo pipefail

INSTALL_DIR="${HOME}/.local/bin"
BIN_NAME="fleet"
MODE="link"
FORCE="0"

while [[ $# -gt 0 ]]; do
    case $1 in
        --copy)    MODE="copy"; shift ;;
        --link)    MODE="link"; shift ;;
        --dir)     INSTALL_DIR="$2"; shift 2 ;;
        --name)    BIN_NAME="$2"; shift 2 ;;
        --force|-f) FORCE="1"; shift ;;
        -h|--help)
            sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *) echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

# Resolve the repo root from this script's location so the script works
# regardless of where it's invoked from.
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
cd "$SCRIPT_DIR"

if [[ ! -f cmd/fleet/main.go ]]; then
    echo "Error: cmd/fleet/main.go not found — run this script from the fleet repo root." >&2
    exit 1
fi

if ! command -v go &>/dev/null; then
    echo "Error: 'go' not found in PATH (need Go 1.26+ to build fleet)." >&2
    exit 1
fi

# Build via make so we pick up the same -ldflags as a real release (notably
# the `git describe`-derived version, which lets `fleet --version` show your
# fork's commit and the `-dirty` suffix when you have uncommitted changes).
echo "==> Building fleet"
make build

BUILT="$SCRIPT_DIR/build/fleet"
if [[ ! -x "$BUILT" ]]; then
    echo "Error: build did not produce $BUILT" >&2
    exit 1
fi

mkdir -p "$INSTALL_DIR"
TARGET="$INSTALL_DIR/$BIN_NAME"

# If something is already at the target, confirm before clobbering — unless
# it's a symlink we previously created (safe to refresh) or --force was given.
if [[ -e "$TARGET" || -L "$TARGET" ]]; then
    is_our_symlink="0"
    if [[ -L "$TARGET" ]]; then
        existing=$(readlink "$TARGET")
        if [[ "$existing" == "$BUILT" ]]; then
            is_our_symlink="1"
        fi
    fi
    if [[ "$is_our_symlink" != "1" && "$FORCE" != "1" ]]; then
        printf "%s already exists. Overwrite? [y/N] " "$TARGET"
        read -r reply
        case "$reply" in
            y|Y|yes|YES) ;;
            *) echo "Aborted."; exit 1 ;;
        esac
    fi
    rm -f "$TARGET"
fi

case "$MODE" in
    link)
        ln -s "$BUILT" "$TARGET"
        echo "==> Linked $TARGET -> $BUILT"
        ;;
    copy)
        cp "$BUILT" "$TARGET"
        chmod +x "$TARGET"
        echo "==> Copied $BUILT -> $TARGET"
        ;;
esac

# PATH check.
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    echo "Note: $INSTALL_DIR is not in your PATH."
    echo "Add this to your shell config (~/.zshrc or similar):"
    echo "    export PATH=\"$INSTALL_DIR:\$PATH\""
fi

# Auto-update would happily replace a fork build with the latest official
# release. Remind the user to opt out so their fork sticks around.
echo ""
echo "Tip: disable the auto-updater so it doesn't replace your fork build with"
echo "     the upstream release. Either set in ~/.config/fleet/config.json:"
echo '         { "auto_update": false }'
echo "     or export the env var (also handy for one-off dev sessions):"
echo "         export FLEET_AUTO_UPDATE_DISABLED=1"

# Quick dependency sanity check (same checks install.sh does).
echo ""
if command -v tmux &>/dev/null; then
    echo "$(tmux -V) [OK]"
else
    echo "Warning: tmux is not installed (required)"
    echo "  brew install tmux"
fi
if command -v claude &>/dev/null; then
    echo "claude $(claude --version 2>/dev/null | head -1) [OK]"
else
    echo "Warning: claude CLI is not installed (required to create sessions)"
fi

echo ""
"$TARGET" --version 2>/dev/null || true
