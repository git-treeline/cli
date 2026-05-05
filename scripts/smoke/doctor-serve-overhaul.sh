#!/usr/bin/env bash
# Smoke test for the doctor/serve overhaul branch.
#
# Run AFTER `go install ./...` (or after `brew upgrade git-treeline`) on a
# real macOS machine with the router previously installed via
# `gtl serve install`. Each step prints what it's verifying and pauses so
# you can eyeball the output.
#
# Not run in CI — these tests intentionally touch launchctl and pf.

set -euo pipefail

step() { printf "\n\033[1;34m▸ %s\033[0m\n" "$*"; }
pause() { read -r -p "  Press enter to continue, or Ctrl-C to abort..." _; }

step "1. gtl serve restart should kickstart the router (no sudo, fast)"
gtl serve restart
pause

step "2. gtl serve status should show the new pid and version"
gtl serve status | head -20
pause

step "3. gtl doctor should NOT flag the running router as a 'rogue process'"
echo "  (regression check: previously substring-matched 'gtl' against 'git-treeline')"
gtl doctor | grep -A2 "Router port" || true
pause

step "4. gtl doctor's Request flow section should call out the FIRST failure"
echo "  Stop the dev server in another terminal first if you want to see this fail."
gtl doctor | sed -n '/Request flow/,$p'
pause

step "5. gtl serve reload-pf reloads pf rules without rewriting the plist"
gtl serve reload-pf
pause

step "6. gtl registry validate (read-only) reports issues, exit 0 when clean"
gtl registry validate || echo "  exit \$? = $?"
pause

step "7. gtl reallocate (dry-run by default) on the current dir"
gtl reallocate
pause

step "8. PersistentPreRun stale warning"
echo "  Forge a stale router.version to confirm the warning fires:"
VERSION_FILE="$(gtl config path 2>/dev/null | sed 's|config.json|router.version|')"
ORIG="$(cat "$VERSION_FILE" 2>/dev/null || echo "")"
echo "  saved: $ORIG"
echo "0.0.0" > "$VERSION_FILE"
echo "  ↓ should print a warning before any output:"
gtl status 2>&1 | head -5 || true
echo "$ORIG" > "$VERSION_FILE"
pause

step "9. doctor --fix dry-runs the remediation (read-only when nothing to fix)"
gtl doctor --fix | sed -n '/Auto-fix/,$p'

echo
echo "Smoke checks complete."
