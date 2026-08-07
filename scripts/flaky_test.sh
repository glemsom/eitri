#!/usr/bin/env bash
# flaky_test.sh — behavior test for `scripts/test.sh` flag modes:
# the flaky harness (`--flaky`, issue #1123) and the shuffled/repeated
# browser-E2E regression gate (`--shuffle` / `--repeat`, issue #1219).
#
# Verifies that the flaky harness:
#   1. clears the Go test cache (go clean -testcache) before running,
#   2. invokes `go test` with a reduced/controlled CPU set (-cpu 1,2) and
#      sequential package execution (-p 1),
#   3. still emits the same compact verdict line as a normal test run,
#   4. does not alter normal (non-flaky) runs.
#
# And that the regression-gate flags:
#   5. pass `-shuffle=on` to go test,
#   6. repeat each test via `-count N`,
#   7. combine with `--e2e` without dropping the e2e build tag / run filter,
#   8. reject a non-numeric `--repeat` count.
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

# ── Regression-gate mode: shuffled + repeated (issue #1219) ────────────────────
GATE_LOG="$WORK/gate.log"
GO_FAKE_LOG="$GATE_LOG"; export GO_FAKE_LOG
OUT="$("$TEST_BIN" --shuffle --repeat 2 2>&1)" || true

check "gate mode passes -shuffle=on to go test" \
    "grep -q -- '-shuffle=on' '$GATE_LOG'"
check "gate mode repeats each test via -count 2" \
    "grep -q -- '-count 2' '$GATE_LOG'"
check "gate mode still emits the compact verdict line" \
    "printf '%s' \"\$OUT\" | grep -q '^VERDICT: PASS'"

E2E_GATE_LOG="$WORK/e2e-gate.log"
GO_FAKE_LOG="$E2E_GATE_LOG"; export GO_FAKE_LOG
OUT="$("$TEST_BIN" --e2e --shuffle --repeat 2 2>&1)" || true

check "e2e gate mode keeps the e2e build tag and run filter" \
    "grep -q -- '-tags e2e' '$E2E_GATE_LOG' && grep -q -- '-run ^TestBrowser' '$E2E_GATE_LOG'"
check "e2e gate mode passes -shuffle=on and -count 2" \
    "grep -q -- '-shuffle=on' '$E2E_GATE_LOG' && grep -q -- '-count 2' '$E2E_GATE_LOG'"

if "$TEST_BIN" --repeat nope >/dev/null 2>&1; then
    FAIL=$((FAIL + 1)); echo "  FAIL - --repeat rejects a non-numeric count"
else
    PASS=$((PASS + 1)); echo "  ok - --repeat rejects a non-numeric count"
fi

echo "-- make test-browser-gate --"
MAKE_OUT="$(make -C "$ROOT" -n test-browser-gate 2>&1 || true)"
check "make test-browser-gate delegates to scripts/test.sh --e2e --shuffle --repeat 2" \
    "printf '%s' \"\$MAKE_OUT\" | grep -q -- 'scripts/test.sh --e2e --shuffle --repeat 2'"

echo ""
echo "RESULT: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
