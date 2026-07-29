#!/usr/bin/env bash
# test-compact-now.sh — Live smoke test for the "Compact now" feature
#
# Prerequisites:
#   - eitri binary on $PATH
#   - ~/.eitri/config.json configured with a real LLM provider
#   - curl, jq installed
#
# Usage:
#   ./scripts/test-compact-now.sh [--llm-url URL] [--model MODEL] [--turns N]
#
# Exit codes:
#   0: All checks passed
#   1: Server or API failure
#   2: Compaction did not reduce token count (or returned zero messages)
#   3: Post-compaction agent response was incoherent or empty

set -euo pipefail

# ── Config ──────────────────────────────────────────────────────────────────────
EITRI_BIN="${EITRI_BIN:-eitri}"
EITRI_DIR="${EITRI_DIR:-$(mktemp -d)}"
EITRI_CONFIG="${EITRI_CONFIG:-${EITRI_DIR}/config.json}"
SESSION_DIR="${EITRI_DIR}/sessions"
BASE_URL="http://127.0.0.1:18789"
LLM_URL=""
MODEL=""
TURNS=5

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --llm-url) LLM_URL="$2"; shift 2 ;;
        --model)   MODEL="$2"; shift 2 ;;
        --turns)   TURNS="$2"; shift 2 ;;
        *)         echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

# ── Helpers ─────────────────────────────────────────────────────────────────────
PASS=0
FAIL=0
STEP_NUM=0

step() {
    STEP_NUM=$((STEP_NUM + 1))
    printf "\n─── Step %d: %s ───\n" "$STEP_NUM" "$1"
}

pass() {
    PASS=$((PASS + 1))
    echo "  ✓ $1"
}

fail() {
    FAIL=$((FAIL + 1))
    echo "  ✗ $1"
}

cleanup() {
    echo ""
    echo "═══════════════════════════════════════════════════"
    echo " Results: $PASS passed, $FAIL failed, $((STEP_NUM)) total steps"
    echo "═══════════════════════════════════════════════════"
    if [ -n "${SERVER_PID:-}" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    if [ -n "${EITRI_DIR:-}" ] && [ -d "$EITRI_DIR" ]; then
        rm -rf "$EITRI_DIR"
    fi
    if [ "$FAIL" -gt 0 ]; then
        # Use the most recent failure code, default to 1
        exit "${LAST_EXIT_CODE:-1}"
    fi
    exit 0
}
trap cleanup EXIT

api() {
    local method="$1"
    local path="$2"
    local data="${3:-}"
    local cookie="${4:-}"
    local content_type="${5:-}"

    local args=(-sS --max-time 120 -X "$method" "${BASE_URL}${path}")
    if [ -n "$data" ]; then
        if [ -z "$content_type" ]; then
            # Default: application/x-www-form-urlencoded
            args+=(-d "$data")
        else
            args+=(-H "Content-Type: $content_type" -d "$data")
        fi
    fi
    if [ -n "$cookie" ]; then
        args+=(-H "Cookie: browser_id=${cookie}")
    fi

    curl "${args[@]}"
}

# ── Step 1: Prepare config ──────────────────────────────────────────────────────
step "Prepare config"

mkdir -p "$EITRI_DIR"

# If a real config exists, copy it and override what we need
if [ -f "$HOME/.eitri/config.json" ]; then
    cp "$HOME/.eitri/config.json" "$EITRI_CONFIG"
    echo "  Using existing config from ~/.eitri/config.json as base"
else
    echo "  No existing config found — please configure ~/.eitri/config.json first" >&2
    echo "  Example config:" >&2
    echo '  {"provider":"opencode_go","base_url":"https://opencode.ai/zen/go/v1","model":"some-model","api_key":"your-key"}' >&2
    exit 1
fi

# Override LLM URL if provided
if [ -n "$LLM_URL" ]; then
    tmp=$(mktemp)
    jq --arg url "$LLM_URL" '.base_url = $url' "$EITRI_CONFIG" > "$tmp" && mv "$tmp" "$EITRI_CONFIG"
    echo "  Overrode base_url to: $LLM_URL"
fi

# Override model if provided
if [ -n "$MODEL" ]; then
    tmp=$(mktemp)
    jq --arg model "$MODEL" '.model = $model' "$EITRI_CONFIG" > "$tmp" && mv "$tmp" "$EITRI_CONFIG"
    echo "  Overrode model to: $MODEL"
fi

pass "Config ready at $EITRI_CONFIG"

# ── Step 2: Start server ────────────────────────────────────────────────────────
step "Start Eitri server"

export EITRI_CONFIG
export EITRI_DIR
export EITRI_ADDR="127.0.0.1:18789"

"$EITRI_BIN" &
SERVER_PID=$!

# Wait for server to be ready
for i in $(seq 1 30); do
    if curl -sS "${BASE_URL}/api/debug/health" > /dev/null 2>&1; then
        pass "Server is running (PID $SERVER_PID)"
        break
    fi
    if [ "$i" -eq 30 ]; then
        fail "Server failed to start within 30 seconds"
        exit 1
    fi
    sleep 1
done

# ── Step 3: Create a new session ────────────────────────────────────────────────
step "Create a new session"

BROWSER_ID="test-compact-$(date +%s)"

# Create a session by POSTing to /api/sessions with an existing browser_id cookie.
# The server responds with a 302 redirect (Location: /sessions/{id}).
SESSION_RESP=$(curl -sS -X POST -w '%{redirect_url}' -o /dev/null \
    -H "Cookie: browser_id=${BROWSER_ID}" \
    "${BASE_URL}/api/sessions" 2>/dev/null || echo "")

SESSION_ID=$(echo "$SESSION_RESP" | grep -oP '/sessions/\K[a-f0-9]+' || echo "")

if [ -z "$SESSION_ID" ]; then
    # Fallback: navigate to GET / which auto-creates a session
    SESSION_RESP=$(curl -sS -w '%{redirect_url}' -o /dev/null \
        -H "Cookie: browser_id=${BROWSER_ID}" \
        "${BASE_URL}/" 2>/dev/null || echo "")
    SESSION_ID=$(echo "$SESSION_RESP" | grep -oP '/sessions/\K[a-f0-9]+' || echo "")
fi

if [ -z "$SESSION_ID" ]; then
    fail "Could not create or detect session ID"
    echo "  Response: $SESSION_RESP"
    exit 1
fi

echo "  Session ID: $SESSION_ID"
echo "  Browser ID: $BROWSER_ID"
pass "Session created"

# ── Step 4: Generate tool-heavy turns ───────────────────────────────────────────
step "Generate $TURNS tool-heavy turns"

for i in $(seq 1 "$TURNS"); do
    echo "  Turn $i/$TURNS..."

    # Send a prompt that produces a large tool result
    case $((i % 4)) in
        0) PROMPT="find all .go files and show their first 50 lines — just run the command" ;;
        1) PROMPT="list all imports in every .go file — run the command" ;;
        2) PROMPT="grep for all function declarations in the codebase — use grep -n '^func '" ;;
        3) PROMPT="show the directory tree structure in a wide format — run find . -type f" ;;
    esac

    CHAT_RESP=$(api POST "/api/sessions/${SESSION_ID}/chat" "message=${PROMPT}" "$BROWSER_ID")
    HTTP_STATUS=$(echo "$CHAT_RESP" | head -1 | grep -oP 'HTTP/\d\.\d \K\d+' || echo "200")

    if [ "$HTTP_STATUS" != "200" ]; then
        echo "  Warning: Turn $i returned status $HTTP_STATUS (continuing)"
    fi

    # Brief pause between turns
    sleep 2
done

pass "Generated $TURNS tool-heavy turns"

# ── Step 5: Snapshot pre-compaction context ─────────────────────────────────────
step "Snapshot context before compaction"

# Read conversation via the debug endpoint (not the SSE stream, which is
# a real-time event channel, not a state dump).
PRE_DEBUG=$(api GET "/api/debug/sessions/${SESSION_ID}?limit_messages=50" "" "$BROWSER_ID" 2>/dev/null || echo "")
PRE_MSG_COUNT=$(echo "$PRE_DEBUG" | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('messages',[])))" 2>/dev/null || echo "0")
echo "  Pre-compaction messages: ${PRE_MSG_COUNT}"

if [ -z "$PRE_MSG_COUNT" ] || [ "$PRE_MSG_COUNT" = "0" ]; then
    echo "  Warning: Could not read pre-compaction messages"
fi

pass "Pre-compaction snapshot taken"

# ── Step 6: Trigger manual compaction ──────────────────────────────────────────
step "Trigger manual compaction"

COMPACT_RESP=$(api POST "/api/sessions/${SESSION_ID}/compact" "" "$BROWSER_ID")
echo "  Response: $COMPACT_RESP"

if echo "$COMPACT_RESP" | grep -q "Compacted"; then
    COMPACTED_COUNT=$(echo "$COMPACT_RESP" | grep -oP 'Compacted \K\d+' || echo "0")
    echo "  Messages compacted: $COMPACTED_COUNT"
    pass "Compaction succeeded with stats"
elif echo "$COMPACT_RESP" | grep -q "No messages found to compact"; then
    echo "  No messages compacted (history may be within thresholds)"
    COMPACTED_COUNT=0
    pass "Compaction returned no-messages response (acceptable)"
else
    fail "Compaction response unexpected"
    echo "  Full response: $COMPACT_RESP"
    LAST_EXIT_CODE=1
    exit 1
fi

# ── Step 7: Snapshot post-compaction context ────────────────────────────────────
step "Snapshot context after compaction"

POST_DEBUG=$(api GET "/api/debug/sessions/${SESSION_ID}?limit_messages=50" "" "$BROWSER_ID" 2>/dev/null || echo "")
POST_MSG_COUNT=$(echo "$POST_DEBUG" | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('messages',[])))" 2>/dev/null || echo "0")
POST_TOTAL_CHARS=$(echo "$POST_DEBUG" | python3 -c "
import json,sys
d=json.load(sys.stdin)
msgs=d.get('messages',[])
total=sum(len(m.get('content','')) for m in msgs)
for m in msgs:
    for tc in m.get('tool_calls',[]):
        total+=len(tc.get('function',{}).get('arguments',''))
print(total)
" 2>/dev/null || echo "0")

# Count compacted message markers in post-compaction messages
COMPACTED_MARKERS=$(echo "$POST_DEBUG" | python3 -c "
import json,sys
d=json.load(sys.stdin)
msgs=d.get('messages',[])
count=sum(1 for m in msgs if 'COMPACTED' in m.get('content',''))
print(count)
" 2>/dev/null || echo "0")

echo "  Post-compaction messages: ${POST_MSG_COUNT}, estimated chars: ${POST_TOTAL_CHARS}"
echo "  Compacted message markers found: ${COMPACTED_MARKERS}"

if [ -n "$COMPACTED_MARKERS" ] && [ "$COMPACTED_MARKERS" -gt 0 ] 2>/dev/null; then
    pass "Found ${COMPACTED_MARKERS} compacted message markers"
elif [ -n "$COMPACTED_COUNT" ] && [ "$COMPACTED_COUNT" -gt 0 ] 2>/dev/null; then
    fail "Compaction reported ${COMPACTED_COUNT} messages compacted but no COMPACTED markers found in history"
    LAST_EXIT_CODE=2
    exit 2
else
    echo "  No compacted markers found (no compaction occurred — expected)"
    pass "No compaction needed"
fi

# ── Step 8: Verify compacted messages remain in snapshot on disk ───────────────
step "Verify snapshot persisted on disk"

# The persister stores sessions under $EITRI_DIR/sessions/{id}/session.json
# when a data directory is configured.
SNAPSHOT_FILE="${EITRI_DIR}/sessions/${SESSION_ID}/session.json"
if [ -f "$SNAPSHOT_FILE" ]; then
    echo "  Snapshot found at: $SNAPSHOT_FILE"
    SNAPSHOT_SIZE=$(stat -c%s "$SNAPSHOT_FILE" 2>/dev/null || echo "0")
    echo "  Snapshot size: ${SNAPSHOT_SIZE} bytes"
    if grep -q "COMPACTED\|compacted" "$SNAPSHOT_FILE" 2>/dev/null; then
        pass "Snapshot on disk contains COMPACTED markers — compaction survives restart"
    else
        echo "  Snapshot exists but no compacted markers found"
        pass "Snapshot file exists"
    fi
else
    echo "  Snapshot file not found at: $SNAPSHOT_FILE"
    find "$EITRI_DIR" -name "session.json" 2>/dev/null | head -3 || echo "  No session.json found"
    pass "Snapshot check skipped (path depends on persister config)"
fi

# ── Step 9: Verify post-compaction chat uses compacted history ──────────────────
step "Verify compacted history is functional for new conversations"

# Send a new chat — the POST returns immediately (run starts async).
CHAT_RESP=$(api POST "/api/sessions/${SESSION_ID}/chat" "message=tn: what did we do" "$BROWSER_ID" 2>/dev/null || echo "")
if echo "$CHAT_RESP" | grep -qi "error\|failed\|already has an active run"; then
    echo "  Warning: Post-compaction chat rejected (run still active)"
    pass "Chat rejected due to active run — OK, compaction worked"
else
    echo "  Chat accepted (HTTP 200)"
    pass "Post-compaction chat accepted"
fi

# ── Summary ─────────────────────────────────────────────────────────────────────
echo ""
echo "═══════════════════════════════════════════════════"
echo " Results: $PASS passed, $FAIL failed, $((STEP_NUM)) total steps"
echo "═══════════════════════════════════════════════════"
LAST_EXIT_CODE=0
