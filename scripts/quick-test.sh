#!/usr/bin/env bash
# The short test cycle of NidaOS (milestone M-11, task T-08). It runs in
# some minutes on the computer of the developer, also on Windows (Git
# Bash), without a virtual machine:
#   1. the Go tests of nida-core, nida and nida-installer,
#   2. the types and the unit tests of the console and of the installer page,
#   3. the builds of the two pages,
#   4. the browser tests of the console (fake API) and of the installer
#      (demonstration mode).
# The full cycle (scripts/vm-test.sh in GitHub Actions) also installs the
# ISO in a virtual machine: run it before a merge.
#
# Usage: scripts/quick-test.sh [--no-browser]
# Necessary: Go (core/go.mod), Node.js 24, and "npx playwright install
# chromium" one time for the browser tests.

set -euo pipefail
REPO=$(cd "$(dirname "$0")/.." && pwd)
browser=yes
[ "${1:-}" = "--no-browser" ] && browser=no
start=$(date +%s)
step() { echo "==> $* ($(( $(date +%s) - start )) s)"; }

step "Go: vet and tests"
(cd "$REPO/core" && go vet ./... && go test ./...)

step "Desktop: the syntax of the extension and the programs"
for f in "$REPO"/desktop/lib/*.js "$REPO"/desktop/extension/*.js "$REPO"/desktop/apps/*.js; do
    node --check "$f"
done

step "Console: install the tools"
(cd "$REPO/console" && { [ -d node_modules ] || npm ci --no-audit --no-fund; })

step "Console: types and unit tests"
(cd "$REPO/console" && npx tsc --noEmit && npx vitest run)

step "Console: build the console and the installer page"
(cd "$REPO/console" && npx vite build && npx vite build --config vite.installer.config.ts)

if [ "$browser" = yes ]; then
    step "Browser tests of the console (fake API)"
    # A new port and a time limit for each run: on Windows, kill does not
    # always stop a Node program that Git Bash started.
    port=$((20000 + RANDOM % 20000))
    (cd "$REPO/console"
        MOCK_TTL=900 node dev/mock.mjs "$port" > /dev/null 2>&1 &
        mock=$!
        trap 'kill $mock 2>/dev/null' EXIT
        sleep 1
        NIDA_URL=http://127.0.0.1:$port npx playwright test)

    step "Browser test of the installer page (demonstration mode)"
    (cd "$REPO/core" && go build -o "$REPO/console/test-results-installer-bin" ./cmd/nida-installer)
    (cd "$REPO/console"
        port=$((20000 + RANDOM % 20000))
        ./test-results-installer-bin --demo --exit-after 10m --addr "127.0.0.1:$port" > /dev/null 2>&1 &
        demo=$!
        trap 'kill $demo 2>/dev/null' EXIT
        for _ in $(seq 1 30); do curl -sf "http://127.0.0.1:$port/api/state" > /dev/null && break; sleep 1; done
        NIDA_INSTALLER_URL=http://127.0.0.1:$port npx playwright test -c playwright.installer.config.ts)
fi

step "All the quick tests passed"
