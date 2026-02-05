#!/bin/bash
# ezs - Shell wrapper for ezstack (legacy, prefer using ezs-go directly)
# For cd functionality, add to ~/.bashrc or ~/.zshrc:
#   eval "$(ezs --shell-init)"

# Find ezs-go relative to this script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
EZS_GO="${SCRIPT_DIR}/ezs-go"

if [[ ! -x "$EZS_GO" ]]; then
    echo "Error: ezs-go binary not found at $EZS_GO" >&2
    exit 1
fi

# Handle shell-init to output a function that can be eval'd
if [[ "${1:-}" == "--shell-init" ]]; then
    cat << 'EOF'
# ezs shell function for cd support
ezs() {
    case "${1:-}" in
        goto|go|new|n)
            # These commands may output "cd <path>" which we need to eval
            eval "$(command ezs-go "$@")"
            ;;
        *)
            command ezs-go "$@"
            ;;
    esac
}
EOF
    exit 0
fi

# Direct execution - just pass through to ezs-go
exec "$EZS_GO" "$@"
