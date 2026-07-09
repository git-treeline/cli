# Security

## Trust model

Git Treeline executes commands defined in your repository's `.treeline.yml` — setup commands, start commands, and lifecycle hooks. This is the same trust model as `Makefile`, `.envrc` (direnv), `.devcontainer.json`, and `package.json` scripts: **you are trusting the repository to run code on your machine.**

Review `.treeline.yml` before running `gtl setup`, `gtl new`, or `gtl start` in repositories you don't control.

## Privileged operations

Some operations require `sudo` and will prompt for your password:

- **CA trust** (`gtl serve install`): Adds a locally-generated certificate authority to the system trust store so `*.localhost` HTTPS works without browser warnings. Uses `/usr/bin/security` on macOS, distro-appropriate trust commands plus per-user NSS stores (`certutil`) on Linux. The CA carries X.509 **name constraints** limiting it to `localhost` and the configured router domain (plus loopback IPs), so a stolen CA key cannot mint browser-trusted certificates for arbitrary sites — the blast radius is confined to Treeline's own hostnames. Upgrading from an older, unconstrained CA regenerates and re-trusts it once (one extra password prompt).
- **Port forwarding** (`gtl serve install`): Configures OS-level port forwarding (443 → router port) so HTTPS works on the standard port. Uses `/sbin/pfctl` on macOS, `iptables`/`nft` on Linux.
- **Hosts file** (`gtl serve hosts sync`): Writes entries to `/etc/hosts` for Safari compatibility. Uses atomic copy via `/bin/cp`.

The router process itself runs unprivileged. Privilege is used once during install, not at runtime.

## Public tunnels and the same-user trust boundary

`gtl tunnel [port]` is an ngrok-style tool: it exposes a local port to the public internet via Cloudflare, and like ngrok it will tunnel **any** local port you point it at — exposing a port is treated as an authorized action by whoever can run the command.

The tunnel daemon's control socket is owned by your user (mode `0600`), so the trust boundary is the **operating-system user account**: any process running as you can open a tunnel, and the daemon cannot distinguish an intentional `gtl tunnel 5432` from a background process doing the same — they are the same user. This matters if you run untrusted code as your user (for example, AI coding agents inside worktrees): such a process could expose a local service (a database, an internal admin port) to the internet through your tunnel. It could equally exfiltrate data by other means available to your user, so the tunnel is one vector among many, not a privilege escalation.

Mitigations: only run `gtl tunnel` for ports you intend to publish; treat the machine's user account as the security boundary; don't run code you don't trust under a user that has a configured tunnel. `gtl tunnel status` shows what is currently exposed.

## Reporting vulnerabilities

If you find a security issue, please email security@productmatter.co rather than opening a public issue.
