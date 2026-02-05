#!/bin/bash
# Stub gh CLI for integration tests
# Simulates GitHub CLI commands without network traffic
# State is persisted in $STUB_GH_STATE_DIR (defaults to /tmp/stub_gh_state)

set -e

STATE_DIR="${STUB_GH_STATE_DIR:-/tmp/stub_gh_state}"
REPO="${STUB_GH_REPO:-testorg/testrepo}"

mkdir -p "$STATE_DIR/prs"

if [ ! -f "$STATE_DIR/pr_counter" ]; then
    echo "1" > "$STATE_DIR/pr_counter"
fi

get_next_pr_number() {
    local num=$(cat "$STATE_DIR/pr_counter")
    echo $((num + 1)) > "$STATE_DIR/pr_counter"
    echo "$num"
}

COMMAND="${1:-}"
SUBCOMMAND="${2:-}"
shift 2 2>/dev/null || true

ARGS=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        -R)
            shift 2 # Skip -R and its value
            ;;
        *)
            ARGS+=("$1")
            shift
            ;;
    esac
done

case "$COMMAND" in
    pr)
        case "$SUBCOMMAND" in
            create)
                TITLE=""
                BODY=""
                BASE=""
                HEAD=""
                DRAFT="false"
                
                i=0
                while [ $i -lt ${#ARGS[@]} ]; do
                    case "${ARGS[$i]}" in
                        --title)
                            ((i++))
                            TITLE="${ARGS[$i]}"
                            ;;
                        --body)
                            ((i++))
                            BODY="${ARGS[$i]}"
                            ;;
                        --base)
                            ((i++))
                            BASE="${ARGS[$i]}"
                            ;;
                        --head)
                            ((i++))
                            HEAD="${ARGS[$i]}"
                            ;;
                        --draft)
                            DRAFT="true"
                            ;;
                    esac
                    ((i++))
                done

                if [ -z "$HEAD" ]; then
                    echo "error: --head required" >&2
                    exit 1
                fi

                PR_NUM=$(get_next_pr_number)
                PR_URL="https://github.com/${REPO}/pull/${PR_NUM}"

                cat > "$STATE_DIR/prs/${PR_NUM}.json" << EOF
{
  "number": ${PR_NUM},
  "url": "${PR_URL}",
  "title": "${TITLE:-$HEAD}",
  "body": "${BODY}",
  "state": "open",
  "baseRefName": "${BASE:-main}",
  "headRefName": "${HEAD}",
  "mergedAt": "",
  "mergeable": "MERGEABLE",
  "isDraft": ${DRAFT},
  "reviewDecision": ""
}
EOF
                echo "$PR_NUM" > "$STATE_DIR/prs/branch_${HEAD}"
                echo "${PR_URL}"
                ;;

            view)
                IDENTIFIER="${ARGS[0]}"
                PR_FILE=""
                if [[ "$IDENTIFIER" =~ ^[0-9]+$ ]]; then
                    PR_FILE="$STATE_DIR/prs/${IDENTIFIER}.json"
                elif [ -f "$STATE_DIR/prs/branch_${IDENTIFIER}" ]; then
                    PR_NUM=$(cat "$STATE_DIR/prs/branch_${IDENTIFIER}")
                    PR_FILE="$STATE_DIR/prs/${PR_NUM}.json"
                fi

                if [ ! -f "$PR_FILE" ]; then
                    echo "no pull requests found for branch \"${IDENTIFIER}\"" >&2
                    exit 1
                fi
                
                cat "$PR_FILE"
                ;;

            edit)
                PR_NUM="${ARGS[0]}"
                PR_FILE="$STATE_DIR/prs/${PR_NUM}.json"

                if [ ! -f "$PR_FILE" ]; then
                    echo "PR #${PR_NUM} not found" >&2
                    exit 1
                fi

                i=1
                while [ $i -lt ${#ARGS[@]} ]; do
                    case "${ARGS[$i]}" in
                        --body)
                            ((i++))
                            NEW_BODY="${ARGS[$i]}"
                            TMP_FILE=$(mktemp)
                            ESCAPED_BODY=$(echo "$NEW_BODY" | sed 's/"/\\"/g' | tr '\n' ' ')
                            sed "s|\"body\": \"[^\"]*\"|\"body\": \"${ESCAPED_BODY}\"|" "$PR_FILE" > "$TMP_FILE"
                            mv "$TMP_FILE" "$PR_FILE"
                            ;;
                    esac
                    ((i++))
                done
                ;;

            list)
                echo "["
                first=true
                for pr_file in "$STATE_DIR/prs"/*.json; do
                    if [ -f "$pr_file" ]; then
                        if [ "$first" = true ]; then
                            first=false
                        else
                            echo ","
                        fi
                        cat "$pr_file" | sed 's/}$/,"author":{"login":"testuser"}}/'
                    fi
                done
                echo "]"
                ;;

            checks)
                echo "CI check   pass   10s   https://example.com/check"
                ;;

            *)
                echo "stub_gh: unknown pr subcommand: $SUBCOMMAND" >&2
                exit 1
                ;;
        esac
        ;;

    auth)
        case "$SUBCOMMAND" in
            status)
                echo "github.com"
                echo "  ✓ Logged in to github.com as testuser"
                ;;
            *)
                echo "stub_gh: unknown auth subcommand: $SUBCOMMAND" >&2
                exit 1
                ;;
        esac
        ;;

    *)
        echo "stub_gh: unknown command: $COMMAND" >&2
        exit 1
        ;;
esac
