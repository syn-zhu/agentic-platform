#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# Agentic Platform - Top-Level Test Runner
#
# Usage: ./tests/run-all.sh [--unit-only | --integration-only]
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

MODE="${1:-all}"  # all, --unit-only, --integration-only

UNIT_PASS=0
UNIT_FAIL=0
INTEGRATION_ROUTING_EXIT=0
INTEGRATION_MCP_EXIT=0

# ---------------------------------------------------------------------------
# Part 1: Kyverno CLI unit tests
# ---------------------------------------------------------------------------
if [[ "$MODE" != "--integration-only" ]]; then
  echo "=== Kyverno Policy Unit Tests ==="
  echo ""

  if ! command -v kyverno &>/dev/null; then
    echo "ERROR: kyverno CLI not found. Install with: brew install kyverno"
    exit 1
  fi

  for dir in "$SCRIPT_DIR"/kyverno/*/; do
    # Guard against the glob matching nothing
    [[ -d "$dir" ]] || continue

    suite=$(basename "$dir")
    echo "--- $suite ---"
    if kyverno test "$dir" 2>&1; then
      UNIT_PASS=$((UNIT_PASS + 1))
    else
      UNIT_FAIL=$((UNIT_FAIL + 1))
    fi
    echo ""
  done

  echo "=== Unit Tests: $UNIT_PASS suites passed, $UNIT_FAIL failed ==="
  echo ""
fi

# ---------------------------------------------------------------------------
# Part 2: Integration tests
# ---------------------------------------------------------------------------
if [[ "$MODE" != "--unit-only" ]]; then
  echo "=== Integration Tests: Routing ==="
  echo ""
  if bash "$SCRIPT_DIR/integration/test-routing.sh"; then
    INTEGRATION_ROUTING_EXIT=0
  else
    INTEGRATION_ROUTING_EXIT=1
  fi

  echo ""
  echo "=== Integration Tests: MCP ==="
  echo ""
  if bash "$SCRIPT_DIR/integration/test-mcp.sh"; then
    INTEGRATION_MCP_EXIT=0
  else
    INTEGRATION_MCP_EXIT=1
  fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
if [[ $UNIT_FAIL -gt 0 || $INTEGRATION_ROUTING_EXIT -ne 0 || $INTEGRATION_MCP_EXIT -ne 0 ]]; then
  exit 1
fi
