#!/usr/bin/env bash
# D3 VERIFICATION GATE — local reproduction of the linux-integration CI job.
#
# Runs the build-tagged Linux integration tests (the ones that install the real
# iptables REDIRECT, start the router, and exercise system + NSS CA trust)
# exactly as .github/workflows/linux-integration.yml does. Use this on a real
# Linux box (or in a container) to reproduce a CI failure locally.
#
# These paths CANNOT run on macOS — they need a live Linux kernel and root. On a
# non-Linux host this script exits early with a hint.
#
# Usage:
#   scripts/linux-integration.sh                # run natively (needs sudo)
#   scripts/linux-integration.sh --docker       # run inside an ubuntu container
#
set -euo pipefail

step() { printf "\n\033[1;34m▸ %s\033[0m\n" "$*"; }

run_native() {
  if [ "$(uname -s)" != "Linux" ]; then
    echo "This harness only runs on Linux (got $(uname -s))." >&2
    echo "Use --docker to run it in an ubuntu container instead." >&2
    exit 2
  fi

  step "Installing prerequisites (iptables, libnss3-tools, ca-certificates)"
  if command -v apt-get >/dev/null 2>&1; then
    sudo apt-get update
    sudo apt-get install -y iptables libnss3-tools ca-certificates
  else
    echo "  Non-Debian host: ensure iptables + certutil (nss-tools) are installed." >&2
  fi

  step "Running Linux integration tests as root"
  # -E preserves GOCACHE/GOPATH/PATH/HOME so tooling resolves as the invoking user.
  sudo -E env "PATH=$PATH" \
    go test -v -count=1 -tags linux_integration \
    -run Integration \
    ./internal/service/... ./internal/proxy/...

  step "Done — the Linux 443/HTTPS + CA-trust stack passed on this host."
}

run_docker() {
  step "Running the integration suite inside an ubuntu:24.04 container"
  local repo
  repo="$(cd "$(dirname "$0")/.." && pwd)"
  docker run --rm --privileged \
    -v "$repo":/src -w /src \
    ubuntu:24.04 \
    bash -c '
      set -euo pipefail
      apt-get update
      apt-get install -y golang-go iptables libnss3-tools ca-certificates
      # Already root inside the container, so sudo is unnecessary.
      go test -v -count=1 -tags linux_integration -run Integration \
        ./internal/service/... ./internal/proxy/...
    '
}

case "${1:-}" in
  --docker) run_docker ;;
  ""|--native) run_native ;;
  *) echo "usage: $0 [--native|--docker]" >&2; exit 2 ;;
esac
