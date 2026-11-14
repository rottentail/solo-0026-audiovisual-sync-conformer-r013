#!/usr/bin/env bash
# Reproducible fuzz entry points for the WAV and Y4M decoder boundaries.
#
# Usage:
#   test/fuzz/run.sh regression   # replay committed corpus + all unit tests (CI mode)
#   test/fuzz/run.sh fuzz [secs]  # live fuzzing, default 60s per target
#   test/fuzz/run.sh defects      # strict spec-assertion evidence suite
#
# The script never changes production code. Live fuzzing writes new findings
# into internal/media/testdata/fuzz/<target>/ so they become permanent
# regression corpus on the next commit.
set -euo pipefail

cd "$(dirname "$0")/../.."

MODE="${1:-regression}"
SECS="${2:-60}"
PKG="./internal/media/"

case "$MODE" in
  regression)
    echo "== full default test suite (table corpus + fuzz seed/testdata replay) =="
    go test ./... -count=1
    echo "== race detector on the media boundary =="
    go test "$PKG" -race -count=1
    ;;
  fuzz)
    for target in FuzzInspectAudio FuzzInspectVideo; do
      echo "== fuzzing $target for ${SECS}s =="
      go test "$PKG" -run '^$' -fuzz "$target" -fuzztime "${SECS}s" -fuzzminimizetime 5s
    done
    echo "== replay every corpus file after fuzzing =="
    go test "$PKG" -run 'FuzzInspect' -v -count=1
    ;;
  defects)
    echo "== strict spec assertions (expected to fail while defects are open) =="
    go test -tags defectevidence "$PKG" -run TestDefect -v
    ;;
  evidence)
    go run ./cmd/defectverify
    ;;
  *)
    echo "unknown mode: $MODE" >&2
    exit 2
    ;;
esac
