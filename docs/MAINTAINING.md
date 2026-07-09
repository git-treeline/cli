# Maintainer runbook

Operational knowledge a maintainer needs but the code doesn't capture. These
are external dependencies and single points of failure — losing access to any
of them breaks releases or users' installs, so ownership should be shared, not
held by one person.

## External single points of failure

| What | Why it matters | If it lapses |
|---|---|---|
| **`prt.dev` DNS** | The default router domain. `*.prt.dev` is wildcard-DNS'd to `127.0.0.1` so `https://project-branch.prt.dev` resolves locally. Referenced throughout the code as the default `router.domain`. | Every default install's HTTPS routing breaks (names stop resolving). Users can set a custom `router.domain`, but the default is dead. Keep the registration on auto-renew; hold the account in a shared org, not a personal one. See `docs/DOMAIN_MIGRATION.md`. |
| **`git-treeline.dev` (docs site)** | The user-facing documentation the README and `gtl serve install` link to. Its source is **not in this repo**. | Documentation 404s. Track down where the site source lives and get it into version control or a known shared location. |
| **Homebrew tap (`git-treeline/homebrew-tap`)** | `brew install git-treeline/tap/git-treeline`. The release workflow auto-publishes the formula using a token. | `brew install`/`brew upgrade` stop working. The publish token is a repo/org secret — rotate on maintainer changes. |
| **Apple Developer ID (signing + notarization)** | The release workflow signs and notarizes the macOS binaries so Gatekeeper accepts them. Requires the Developer ID cert, its private key, and an app-specific/API notarization credential (stored as CI secrets). | Unsigned/unnotarized macOS builds are blocked by Gatekeeper. These credentials expire — track the cert expiry and the Apple account. |

## Release process notes

- Releases are cut with GoReleaser (`.goreleaser.yml`) via the release workflow.
- **Keep the CHANGELOG in step with tags.** As of this writing the CHANGELOG is
  behind several tags (entries for `0.45`–`0.47` were never written). Decide
  whether a CHANGELOG entry is release-blocking and, ideally, add a CI check
  that a new tag has a matching heading.
- Version is injected via GoReleaser ldflags; a `go install`'d binary reports
  `dev` (no ldflags) — expected.

## Dependency watch

- `mark3labs/mcp-go` is pre-1.0 and has broken APIs between minor versions;
  dependabot bumps may land compile breaks — that's what CI is for, but expect
  occasional churn.

## Known follow-ups (not blocking)

- The repo is not gofmt-clean on `main` (pre-existing field-alignment padding in
  several files). A one-time `gofmt -w ./...` plus a CI gofmt gate would fix it,
  but it's a large, noisy diff kept separate from behavioral changes.
- `cmd/` test coverage is the thinnest of the large packages; the highest-value
  targets are `server.go`, `doctor.go`, and `tunnel.go`.
