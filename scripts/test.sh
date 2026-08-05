#!/usr/bin/env bash
# test.sh — run `go test` and print a compact verdict line.
#
# Instead of one boilerplate line per package, this prints a single verdict
# line (packages passed/failed, failing test names, DATA RACE count) and, on
# failure, only the failing tests' error excerpts (and DATA RACE reports with
# --race). The full raw output is teed to an artifact file under the build
# artifacts directory so details can be grepped on demand.
#
# Usage:
#   scripts/test.sh            # go test ./...
#   scripts/test.sh --race     # go test -race ./...
#
# Exit code mirrors `go test` (0 all pass, 1 failures, 2 build errors).
set -uo pipefail

RACE=0
if [ "${1:-}" = "--race" ]; then
    RACE=1
    shift
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BUILD_DIR="${BUILD_DIR:-dist}"
LOG_NAME="test-output.log"
EXTRA_FLAGS=()
if [ "$RACE" -eq 1 ]; then
    LOG_NAME="test-race-output.log"
    EXTRA_FLAGS=(-race)
fi
ARTIFACT="$ROOT/$BUILD_DIR/$LOG_NAME"
mkdir -p "$ROOT/$BUILD_DIR"

START=$SECONDS
set +e
go test "${EXTRA_FLAGS[@]}" ./... 2>&1 | tee "$ARTIFACT"
GOEXIT=${PIPESTATUS[0]}
set -e
ELAPSED=$((SECONDS - START))

# ── Parse the raw output with a single awk pass ────────────────────────────────
# Emits, in order: failing-test sections (--- FAIL: Name + excerpt), build
# failure blocks, DATA RACE reports, and the final package/race verdict line.
OUT="$(
    awk '
        BEGIN {
            pkg_ok = 0; pkg_fail = 0; pkg_notest = 0
            fail_tests = 0; races = 0; builds = 0
        }
        /^(ok|FAIL)[ \t]+/ {
            # package summary line — closes any open section
            if (in_section) { print ""; in_section = 0 }
            if ($1 == "ok") pkg_ok++
            else pkg_fail++
            if ($0 ~ /\[build failed\]/) {
                builds++
                print $0
            }
            next
        }
        /^\?/ {
            # "?   pkg [no test files]" — closes any open section
            if (in_section) { print ""; in_section = 0 }
            pkg_notest++
            next
        }
        /^--- FAIL: / {
            if (in_section) print ""
            fail_tests++
            names[fail_tests] = $3
            print $0
            in_section = 1
            next
        }
        /^WARNING: DATA RACE/ {
            if (in_section) { print ""; in_section = 0 }
            races++
            print "--- DATA RACE ---"
            print $0
            in_section = 1
            in_race = 1
            next
        }
        /^==================/ {
            if (in_race) { print $0; print ""; in_section = 0; in_race = 0 }
            next
        }
        /^# / {
            # build/vet block header — surface compile errors as a section
            if (in_section) { print ""; in_section = 0 }
            builds++
            print $0
            in_section = 1
            next
        }
        /^panic:/ {
            if (in_section) print ""
            fail_tests++
            names[fail_tests] = "(panic)"
            print "--- FAIL: (panic) ---"
            print $0
            in_section = 1
            next
        }
        /^(FAIL|PASS|exit status)/ {
            if (in_section) { print ""; in_section = 0 }
            next
        }
        in_section { print $0 }
        END {
            total = pkg_ok + pkg_fail + pkg_notest
            printf "pkg_ok=%d pkg_fail=%d pkg_notest=%d total=%d fail_tests=%d races=%d builds=%d\n",
                pkg_ok, pkg_fail, pkg_notest, total, fail_tests, races, builds
            for (i = 1; i <= fail_tests; i++) printf "FAILTEST %s\n", names[i]
        }
    ' "$ARTIFACT"
)"
LAST_LINE="$(printf '%s\n' "$OUT" | grep '^pkg_ok=' | tail -1)"
if [[ "$LAST_LINE" =~ ^pkg_ok=([0-9]+)\ pkg_fail=([0-9]+)\ pkg_notest=([0-9]+)\ total=([0-9]+)\ fail_tests=([0-9]+)\ races=([0-9]+)\ builds=([0-9]+)$ ]]; then
    PKG_OK="${BASH_REMATCH[1]}"
    PKG_FAIL="${BASH_REMATCH[2]}"
    PKG_NOTEST="${BASH_REMATCH[3]}"
    TOTAL="${BASH_REMATCH[4]}"
    FAIL_TESTS="${BASH_REMATCH[5]}"
    RACES="${BASH_REMATCH[6]}"
    BUILDS="${BASH_REMATCH[7]}"
else
    echo "test.sh: could not parse go test output" >&2
    exit "$GOEXIT"
fi
SECTIONS="$(printf '%s\n' "$OUT" | sed '/^pkg_ok=/d; /^FAILTEST /d')"

# ── Verdict line ───────────────────────────────────────────────────────────────
if [ "$GOEXIT" -eq 0 ]; then
    VERDICT="PASS"
else
    VERDICT="FAIL"
fi

PASSED=$((PKG_OK + PKG_NOTEST))
if [ "$FAIL_TESTS" -gt 0 ]; then
    NAMES="$(printf '%s\n' "$OUT" | sed -n 's/^FAILTEST //p' | paste -sd, -)"
    MAX_NAMES=12
    NAME_COUNT="$(printf '%s' "$NAMES" | awk -F, '{print NF}')"
    if [ "$NAME_COUNT" -gt "$MAX_NAMES" ]; then
        NAMES="$(printf '%s' "$NAMES" | cut -d, -f1-$MAX_NAMES),+$((NAME_COUNT - MAX_NAMES)) more"
    fi
    TEST_SUMMARY=", $FAIL_TESTS failed test(s): $NAMES"
else
    TEST_SUMMARY=""
fi

RACE_SUMMARY=""
if [ "$RACES" -gt 0 ]; then
    RACE_SUMMARY=", $RACES DATA RACE warning(s)"
fi

if [ "$VERDICT" = "PASS" ]; then
    echo ""
    echo "VERDICT: PASS $TOTAL/$TOTAL packages in ${ELAPSED}s — full log: $BUILD_DIR/$LOG_NAME"
    echo ""
else
    echo ""
    echo "VERDICT: FAIL $PKG_FAIL/$TOTAL packages failed ($PASSED passed$TEST_SUMMARY$RACE_SUMMARY) in ${ELAPSED}s — full log: $BUILD_DIR/$LOG_NAME"
    echo ""
fi
echo ""

# ── Failure details (excerpts only, no passing-test spam) ──────────────────────
if [ -n "$SECTIONS" ]; then
    printf '%s\n' "$SECTIONS"
    echo ""
fi

exit "$GOEXIT"
