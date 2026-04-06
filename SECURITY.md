# Security

## Trust model

Git Treeline executes commands defined in your repository's `.treeline.yml` — setup commands, start commands, and lifecycle hooks. This is the same trust model as `Makefile`, `.envrc` (direnv), `.devcontainer.json`, and `package.json` scripts: **you are trusting the repository to run code on your machine.**

Review `.treeline.yml` before running `gtl setup`, `gtl new`, or `gtl start` in repositories you don't control.

## Privileged operations

Some operations require `sudo` and will prompt for your password:

- **CA trust** (`gtl serve install`): Adds a locally-generated certificate authority to the system trust store so `*.localhost` HTTPS works without browser warnings. Uses `/usr/bin/security` on macOS, distro-appropriate trust commands on Linux.
- **Port forwarding** (`gtl serve install`): Configures OS-level port forwarding (443 → router port) so HTTPS works on the standard port. Uses `/sbin/pfctl` on macOS, `/sbin/iptables` on Linux.
- **Hosts file** (`gtl serve hosts sync`): Writes entries to `/etc/hosts` for Safari compatibility. Uses atomic copy via `/bin/cp`.

The router process itself runs unprivileged. Privilege is used once during install, not at runtime.

## Reporting vulnerabilities

If you find a security issue, please email security@gittreeline.com rather than opening a public issue.
