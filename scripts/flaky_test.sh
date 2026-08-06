#!/usr/bin/env bash
# flaky_test.sh — behavior test for `scripts/test.sh --flaky` and
# `make test-flaky` (issue #1123).
#
# Verifies that the flaky harness:
#   1. clears the Go test cache (go clean -testcache) before running,
#   2. invokes `go test` with a reduced/controlled CPU set (-cpu 1,2) and
#      sequential package execution (-p 1),
#   3. still emits the same compact verdict line as a normal test run,
#   4. does not alter normal (non-flaky) runs.
#
# Runs with a fake `go` on PATH so no real tests are executed. It is not part
# of the `go test` suite; run it directly (see docs/TESTING.md).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEST_BIN="${ROOT}/scripts/test.sh"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# Fake `go` that records invocations and emits canned package summary lines so
# test.sh's compact-verdict parser has something to parse.
cat > "$WORK/go" <<'EOF'
#!/usr/bin/env bash
echo "called:go:$*" >> "$GO_FAKE_LOG"
case "$1" in
    clean)
        exit 0
        ;;
    test)
        printf 'ok \tgithub.com/eitri/pkg\t0.5s\n'
        printf '?   \tgithub.com/eitri/notest\t[no test files]\n'
        exit 0
        ;;
    *)
        exit 0
        ;;
esac
EOF
chmod +x "$WORK/go"

PASS=0
FAIL=0

check() { # desc, cond
    local desc="$1" cond="$2"
    if eval "$cond"; then
        PASS=$((PASS + 1)); echo "  ok - $desc"
    else
        FAIL=$((FAIL + 1)); echo "  FAIL - $desc"
    fi
}

# ── Normal (non-flaky) run: must not clear the cache ───────────────────────────
NORMAL_LOG="$WORK/normal.log"
GO_FAKE_LOG="$NORMAL_LOG"; export GO_FAKE_LOG; export PATH="$WORK:$PATH"
"$TEST_BIN" >/dev/null 2>&1 || true

check "normal run does not clear the test cache" \
    "! grep -q 'clean -testcache' '$NORMAL_LOG'"
check "normal run uses default go test flags" \
    "grep -q 'called:go:test ./\.\.\.' '$NORMAL_LOG'"

# ── Flaky run: cache-cleared, CPU-constrained, sequential ──────────────────────
FLAKY_LOG="$WORK/flaky.log"
GO_FAKE_LOG="$FLAKY_LOG"; export GO_FAKE_LOG
OUT="$("$TEST_BIN" --flaky 2>&1)" || true

check "flaky mode clears the test cache" \
    "grep -q 'called:go:clean -testcache' '$FLAKY_LOG'"
check "flaky mode constrains CPU (-cpu 1,2)" \
    "grep -q -- '-cpu 1,2' '$FLAKY_LOG'"
check "flaky mode forces sequential packages (-p 1)" \
    "grep -q -- '-p 1' '$FLAKY_LOG'"
echo "-- make test-flaky --"
MAKE_OUT="$(make -C "$ROOT" -n test-flaky 2>&1 || true)"
check "make test-flaky delegates to scripts/test.sh --flaky" \
    "printf '%s' \"\$MAKE_OUT\" | grep -q -- 'scripts/test.sh --flaky'"

check "flaky mode still emits the compact verdict line" \
    "printf '%s' \"\$OUT\" | grep -q '^VERDICT: PASS'"

echo ""
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
