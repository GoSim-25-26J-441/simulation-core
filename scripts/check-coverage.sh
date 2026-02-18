#!/usr/bin/env bash
# Run tests with coverage and verify against threshold (matches CI).
# Run locally before pushing to avoid coverage failures in CI.
#
# Usage: ./scripts/check-coverage.sh [threshold]
#   ./scripts/check-coverage.sh        # uses 80.0
#   ./scripts/check-coverage.sh 80.0
set -e

cd "$(dirname "$0")/.."
THRESHOLD="${1:-80.0}"

echo "Running tests with coverage (excluding cmd/, gen/)..."
PKGS=$(go list ./... | grep -v '/cmd/' | grep -v '/gen/')
go test -race -coverprofile=coverage.out -covermode=atomic $PKGS

echo ""
echo "Coverage report:"
go tool cover -func=coverage.out | tail -5

COVERAGE=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//')
echo ""
echo "Total coverage: ${COVERAGE}%"
echo "Threshold: ${THRESHOLD}%"

if (( $(echo "$COVERAGE < $THRESHOLD" | bc -l) )); then
  echo "❌ Coverage ${COVERAGE}% is below threshold ${THRESHOLD}%"
  exit 1
else
  echo "✅ Coverage ${COVERAGE}% meets threshold ${THRESHOLD}%"
  exit 0
fi
