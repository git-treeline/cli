# Git Treeline

Worktree environment manager — isolated ports, databases, and services across parallel development environments.

## Why

Git worktrees let you check out multiple branches side by side. That's always been possible. What's changed is scale.

AI coding agents work in worktrees. You might have three agents building features in parallel, each in its own worktree — and when they're done, you need to *run* each one to review the work. Boot the server, click through the UI, verify the behavior. You can't review what you can't run, and you can't run three copies of the same app when they're all fighting over port 3000.

The problem gets worse when your app needs a database, Redis, or other local services — but the simplest case is just ports. If you run `next dev` in three worktrees, they all want port 3000. Git Treeline gives each one its own.

## How it works

Git Treeline has two layers of configuration and a central registry.

**Project config** (`.treeline.yml`, committed to your repo) describes what the project needs: port allocation, env vars to set, and optionally database cloning and setup commands.

**User config** (`config.json`, on your machine) controls allocation policy: port range and increment. This is per-developer, not per-project — it governs how resources are handed out across everything on your machine.

**The registry** (`registry.json`) is the ledger. When you run `gtl setup`, it allocates the next available port block, writes your env file, and records the allocation. When you run `gtl release`, it frees those resources. `gtl status` shows everything allocated across all projects.

For projects that need it, Treeline can also clone PostgreSQL or SQLite databases and assign Redis namespaces — but those are opt-in.

## Install

### Homebrew

```bash
brew install git-treeline/tap/git-treeline
```

This installs both `git-treeline` and the `gtl` shorthand alias.

### From source (requires Go 1.26+)

```bash
go install github.com/git-treeline/cli@latest
```

### From release binary

Download the latest binary from [GitHub Releases](https://github.com/git-treeline/cli/releases), extract, and place on your `PATH`.

> **Naming:** The binary is `git-treeline`, which also works as `git treeline` (git subcommand convention). Homebrew additionally installs `gtl` as a short alias. All three invocations are equivalent.

## First-time setup on your machine

### 1. Install the CLI

Use [Homebrew](#homebrew), [Go](#from-source-requires-go-126), or a [release binary](#from-release-binary). Installing `gtl` does **not** require `sudo` and does **not** install certificates or background services by itself.

### 2. Run `gtl install`

Whether you're the first developer setting up the project or you're joining a repo that already has `.treeline.yml`:

```bash
cd your-project
gtl install
```

This single command handles everything:
1. **Creates `.treeline.yml`** if missing and detects your framework
2. **Creates user config** if missing
3. **Installs the post-checkout hook** so future worktrees auto-setup
4. **Allocates ports and writes env** for the current worktree
5. **Optionally enables local HTTPS routing** — prompted with a link to the [networking docs](https://git-treeline.dev/docs/networking/#the-https-router-gtl-serve); requires sudo for CA trust and port forwarding. Can be skipped and run later via `gtl serve install`. If you skip it once, `router.mode` stays `prompt` and Treeline may offer it again later. If you disable future offers, Treeline saves `router.mode: disabled` and stays on localhost-only workflows until you manually run `gtl serve install` or reset the mode.

The HTTPS router gives you `https://project-branch.prt.dev` URLs. Without it, worktrees are accessible at `http://localhost:{port}`. `gtl setup`, `gtl new`, `gtl clone`, and `gtl review` all work without the router. See **`gtl serve status`** and **`gtl serve uninstall`** to inspect or remove the HTTPS stack.

**Safari on macOS** may not resolve custom domain subdomains without `/etc/hosts` entries. After you have routes, run **`gtl serve hosts sync`** (asks for `sudo`) to add a managed block to `/etc/hosts`, or use another browser.

> **`GTL_HEADLESS=1`** skips HTTPS router prompts and warnings — use in CI or automation only.

---

## Quick start

### 1. Install Treeline in the repo

```bash
cd your-project
gtl install
```

`install` auto-detects your framework when `.treeline.yml` is missing and generates a tailored config. It also creates user config if needed, installs the post-checkout hook, allocates resources for the current worktree, and optionally offers the HTTPS router.

After generating the config, Treeline runs framework-aware diagnostics and prints actionable warnings — for example, if your Vite project needs `vite.config.js` changes to read the allocated port, or if your Node project lacks a dotenv library. Commit `.treeline.yml` so your team shares it.

Use `gtl init` directly only when you want to generate or regenerate `.treeline.yml` without running the rest of install.

### 2. Create and set up a worktree

```bash
gtl new feature-auth
```

This creates the worktree, allocates resources, writes your env file, and runs setup commands — all in one step. Add `--start` to boot the app immediately.

If you already have a worktree, use `gtl setup` instead:

```bash
git worktree add ../myapp-feature-x feature-x
gtl setup ../myapp-feature-x
```

### 3. Boot the worktree

```bash
cd ../myapp-feature-auth
gtl start
```

`gtl start` runs the `commands.start` from `.treeline.yml` under a lightweight supervisor. Your server runs in your terminal with full log output — exactly like running `bin/dev` directly. The difference: AI agents and scripts can now control your server without taking over your terminal.

```bash
gtl stop       # stops the server — supervisor stays alive, ready to resume
gtl stop --kill # shuts down the supervisor entirely
gtl start      # resumes the server in your original terminal
gtl restart    # bounces the server in one step — logs keep flowing
```

`stop` + `start` lets agents pause the server, do work (run migrations, install packages), and bring it back — all in your terminal. `restart` is a single-step bounce. Ctrl+C in the terminal exits the supervisor entirely.

The supervisor communicates over a Unix socket. No background processes, no log files, no PID management. Your terminal owns the process; the socket is just a remote control.

For agents and scripts that need to wait for the server:

```bash
gtl start --await                  # block until TCP-ready, then exit 0
gtl start --await --await-timeout 120   # custom timeout (default 60s)
```

Your app starts on 3010. The main copy runs on 3000. No collisions. Some frameworks (Rails, Express) read `PORT` from the env file automatically; others (Next.js) need their dev script wired up — `gtl init` prints framework-specific guidance.

### 4. Access worktrees by name instead of port

Once you're running multiple worktrees, remembering port numbers gets old. Git Treeline has three networking commands that give you human-readable URLs.

#### `gtl serve` — local HTTPS subdomain router

```bash
gtl serve install
```

One-time setup that installs a background router mapping `https://{project}-{branch}.prt.dev` to the correct worktree port. After install, `https://salt-staff-reporting.prt.dev` just works — no port numbers, trusted HTTPS, automatic certificate management.

Requires macOS or Linux. The install needs `sudo` twice: once to trust the local CA, once to forward port 443 to the router.

```bash
gtl serve status       # show routes and service health
gtl serve uninstall    # remove everything (CA, port forwarding, service)
```

#### `gtl proxy` — single-port forwarding

```bash
gtl proxy 3000              # forward :3000 → current worktree's port
gtl proxy 3000 3050         # forward :3000 → :3050
gtl proxy 3000 --tls        # HTTPS on :3000 → current worktree's port
```

Useful when an external service (OAuth provider, Stripe webhooks) is configured to call `localhost:3000` and you need that traffic routed to whichever worktree you're working on.

#### `gtl tunnel` — public HTTPS via Cloudflare

```bash
gtl tunnel              # quick tunnel (random URL, no account needed)
gtl tunnel 3050         # expose a specific port
gtl tunnel setup        # configure a named tunnel with your own domain
```

Quick tunnels give you a random `*.trycloudflare.com` URL — good for one-off testing, MCP server access, or sharing a link. Named tunnels give you stable subdomains on your own domain (e.g. `salt-staff-reporting.myteam.dev`), matching the same routes as `gtl serve`.

Requires [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/). `gtl tunnel setup` walks you through authentication, tunnel creation, and DNS configuration interactively.

#### `gtl share` — private branch sharing

```bash
gtl share               # share current worktree's port (Cloudflare)
gtl share 3050          # share a specific port
gtl share --tailscale   # share via Tailscale Serve (tailnet-only)
```

Creates a private URL for your dev server. Two backends:

**Cloudflare (default):** Token-gated reverse proxy over a Cloudflare tunnel. The recipient opens the link, gets a session cookie, and sees clean URLs from there — no accounts, no installs. Uses your domain when a named tunnel is configured; falls back to `*.trycloudflare.com` otherwise. Use `--tunnel <name>` to pick a specific tunnel config.

**Tailscale (`--tailscale`):** Exposes the port on your tailnet via `tailscale serve`. No tokens needed — Tailscale handles identity-based auth with WireGuard encryption. Only people on your tailnet can reach the URL. Requires Tailscale installed and running.

Tokens (Cloudflare mode) rotate on every invocation. Both backends clean up on Ctrl+C. Unlike `gtl tunnel`, share URLs are not guessable from branch names. Use `share` to send a link to a teammate; use `tunnel` for stable webhook/OAuth URLs.

### 5. Clone and set up a new project

```bash
gtl clone git@github.com:org/repo.git
gtl clone git@github.com:org/repo.git -- --depth 1
```

Clones the repo, auto-detects the framework, generates `.treeline.yml` if absent, and runs `gtl setup`. All flags after the URL are passed through to `git clone`. Does not auto-start the server — cloning a foreign repo and running arbitrary shell commands is a trust boundary.

### 6. Switch branches in a worktree

```bash
gtl switch feature-payments
gtl switch 42                  # accepts a PR number (resolved via gh)
gtl switch 42 --setup          # re-run setup commands after switching
```

Fetches from origin, checks out the branch, updates the registry, and refreshes the env file. Like `git switch` but Treeline-aware — handles fetch, env refresh, and PR lookup in one step.

### 7. Review a pull request

```bash
gtl review 42 --start
```

Fetches the PR branch via `gh`, creates a worktree, allocates resources, runs setup, and boots the app. Requires the [gh CLI](https://cli.github.com).

When run from inside a worktree, `review` prompts to switch the current worktree to the PR branch (same as `gtl switch`). `new` warns that the worktree will be created elsewhere and asks for confirmation; pass `--force` / `-f` to skip the prompt. From the main repo, both commands behave as before.

### 8. Open a worktree in the browser

```bash
gtl open
```

Opens the current worktree in your default browser. Prefers `https://{project}-{branch}.prt.dev` when `gtl serve` is running; falls back to `http://localhost:{port}`.

### 9. Show routing URLs

```bash
gtl routes              # human-readable
gtl routes --json       # structured output
```

Shows the routing URLs for the current worktree — one per allocated port. When `gtl serve` is running, shows `https://` URLs via `prt.dev`; otherwise falls back to `http://localhost`. Also shows the tunnel URL if configured.

### 10. Check project health

```bash
gtl doctor
```

Checks config, allocation, runtime, and diagnostics in one view — whether `.treeline.yml` exists, env file is on disk, ports are allocated and listening, supervisor is running, and framework-specific guidance.

### 11. Check what's allocated

```bash
gtl status
```

```
myapp:
  :3010  feature-auth
  :3020  pr-42

api-service:
  :3030  experiment  db:api_development_experiment
```

Use `--check` to probe ports and show which services are running. Use `--watch` for a live-updating dashboard.

### 12. Inspect the environment

```bash
gtl env                   # print env file, annotate Treeline-managed keys
gtl env --json            # structured output for agents
gtl env --template        # show unresolved interpolation tokens
```

Prints the current worktree's env file contents. Keys that Treeline manages (defined in the `env:` block of `.treeline.yml`) are annotated with `[treeline]`. Use `--template` to see the raw tokens before interpolation.

### 13. Release when done

```bash
gtl release ../myapp-feature-auth --drop-db --remove-worktree
```

`--remove-worktree` runs `git worktree remove` after freeing the allocation. If the worktree has uncommitted changes, removal is skipped unless `--force` is also set.

Without the flag, releasing and removing the directory are separate steps:

```bash
gtl release ../myapp-feature-auth --drop-db
git worktree remove ../myapp-feature-auth
```

For bulk cleanup: `gtl release --project myapp --drop-db` or `gtl release --all --drop-db`.

### 14. Refresh after config changes

```bash
gtl refresh --dry-run    # preview what would change
gtl refresh              # apply changes, restart supervised servers
```

After adding or changing port reservations (or `port_count`), `gtl refresh` walks every allocation and re-resolves ports. Supervised servers (`gtl start`) are restarted automatically. Servers you started manually are flagged so you know which ones to bounce.

### 15. Prune stale allocations

```bash
gtl prune --stale
gtl prune --merged --drop-db
```

`--stale` removes allocations for worktrees that no longer exist on disk. `--merged` targets branches already merged into the merge target branch. Treeline auto-detects the merge target via git (works with any remote host), but you can set `merge_target` in `.treeline.yml` if your repo uses something other than `main`/`master` (e.g. `develop`, `staging`).

### 16. Manage worktree databases

```bash
gtl db name                          # print the worktree's database name
gtl db reset                         # drop and re-clone from the configured template
gtl db reset --from staging_snapshot # clone from a different database instead
gtl db restore dump.sql              # drop and restore from a pg_dump file
gtl db drop                          # just drop the database
```

`db reset` re-clones from the `database.template` in `.treeline.yml`. Use `--from` to override the source — useful when a teammate has a clean database or you've pulled a sanitized staging dump.

`db restore` auto-detects the dump format (pg_dump custom format vs plain SQL) and uses `pg_restore` or `psql` accordingly.

### 17. Resolve cross-worktree services

```bash
gtl resolve api                  # URL for api project on same branch
gtl resolve api staging          # URL for api project on staging branch
gtl resolve api --json           # structured output
```

Looks up another project's allocated URL from the registry. Uses same-branch matching by default — if your frontend and API repos both have `feature-auth` checked out, `gtl resolve api` returns the API's URL without any configuration.

#### Runtime linking

```bash
gtl link api staging             # override: resolve api → staging branch
gtl link api staging --restart   # override + bounce the supervised server
gtl link                         # list active links
gtl unlink api                   # revert to same-branch default
```

Links are stored in the registry and survive restarts, refresh, and re-setup. They're visible in `gtl status` and `gtl doctor`. Use links when you want your frontend worktree to talk to a specific API branch regardless of branch naming.

#### Env template integration

Use `{resolve:project}` in your `.treeline.yml` env block:

```yaml
env:
  API_URL: "{resolve:api}"
  AUTH_URL: "{resolve:auth/main}"
```

Tokens are resolved at setup time. `{resolve:api}` checks link overrides first, then falls back to same-branch matching. `{resolve:api/main}` always resolves to the `main` branch.

### 18. Manage user config

```bash
gtl config list                      # dump all settings
gtl config get port.base             # read a value (dot notation)
gtl config set port.base 4000        # write a value
gtl config path                      # print the config file location
gtl config edit                      # open in $EDITOR
```

### 19. AI agent integration (MCP)

git-treeline speaks [MCP](https://modelcontextprotocol.io/) natively. Add this to your editor config and agents automatically get access to your worktree allocations, ports, databases, and dev server controls.

**Cursor** (`.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "gtl": { "command": "gtl", "args": ["mcp"] }
  }
}
```

**Claude Code**: `claude mcp add gtl -- gtl mcp`

Once configured, agents can query allocations (`status`, `port`, `list`), check project health (`doctor`), read config values (`config_get`), get database names (`db_name`), and control the supervised dev server (`start`, `stop`, `restart`) — all as structured JSON without parsing CLI output.

## Port-dependent data

If your app stores URLs that include the port (e.g., OAuth redirect URIs, webhook endpoints), the cloned database will have stale values pointing to the wrong port. Use `commands.setup` to patch them — setup commands run after the env file is written, so `PORT` is available:

```yaml
commands:
  setup:
    - bundle install --quiet
    - bin/rails runner "Doorkeeper::Application.update_all(redirect_uri: 'http://localhost:' + ENV['PORT'] + '/oauth/callback')"
```

This runs on every `gtl setup`, including re-setup after a release/re-allocation.

## Framework examples

Git Treeline is framework-agnostic. The `.treeline.yml` config adapts to your stack.

### Next.js

```yaml
project: myapp

env_file: .env.local

env:
  PORT: "{port}"
  NEXT_PUBLIC_APP_URL: "http://localhost:{port}"

commands:
  setup:
    - npm install
  start: npm run dev
```

Next.js loads `.env.local` into `process.env` for your app code, but the dev server **does not** use `PORT` for its listen port. Update your `package.json` dev script:

```json
"dev": "next dev --port ${PORT:-3000}"
```

`gtl init` prints this guidance automatically for Next.js projects.

### Next.js with Prisma + Postgres

```yaml
project: myapp

env_file: .env.local

env:
  PORT: "{port}"
  DATABASE_URL: "postgresql://localhost:5432/{database}"
  NEXT_PUBLIC_APP_URL: "http://localhost:{port}"

database:
  adapter: postgresql
  template: myapp_development
  pattern: "{template}_{worktree}"

commands:
  setup:
    - npm install
    - npx prisma migrate deploy
  start: npm run dev
```

### Vite (React, Vue, Svelte, etc.)

```yaml
project: website

env_file: .env.local

env:
  PORT: "{port}"

commands:
  setup:
    - npm install
  start: npx vite
```

**Port wiring:** Vite loads `.env.local` for `import.meta.env` but does _not_ use `PORT` for its dev server by default. Add this to your `vite.config.js`:

```js
import { defineConfig, loadEnv } from 'vite'
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  return {
    server: { port: parseInt(env.PORT || '5173') }
  }
})
```

### Node.js / Express

```yaml
project: myapi

env_file: .env

env:
  PORT: "{port}"

commands:
  setup:
    - npm install
  start: node server.js
```

When the env file written by Treeline differs from the one seeded from the main repo, use the extended form:

```yaml
env_file:
  path: .env
  seed_from: .env.example
```

### Rails

```yaml
project: myapp
merge_target: develop     # branch that prune --merged checks against (auto-detected if omitted)
port_count: 2

env_file: .env.local

database:
  adapter: postgresql
  template: myapp_development
  pattern: "{template}_{worktree}"

copy_files:
  - config/master.key

env:
  PORT: "{port}"
  DATABASE_NAME: "{database}"
  REDIS_URL: "{redis_url}"
  ESBUILD_PORT: "{port_2}"
  APPLICATION_HOST: "localhost:{port}"

commands:
  setup:
    - bundle install --quiet
    - yarn install --silent
  start: bin/dev
```

Rails apps using `dotenv-rails` (most do) will load the env file automatically at boot. No additional gems needed.

### Frontend SPA (no server resources)

```yaml
project: dashboard

env_file: .env.local

env:
  PORT: "{port}"

commands:
  setup:
    - npm install
```

## Configuration

### User config (`config.json`)

Controls allocation policy for your machine. Created automatically by `gtl init` or `gtl config`.

```json
{
  "port": {
    "base": 3000,
    "increment": 10,
    "reservations": {
      "salt": 3000,
      "truherd": 3002,
      "api-docs": 3004
    }
  },
  "redis": {
    "strategy": "prefixed",
    "url": "redis://localhost:6379"
  },
  "router": {
    "port": 3001
  },
  "tunnel": {
    "default": "gtl",
    "tunnels": {
      "gtl": { "domain": "myteam.dev" }
    }
  },
  "worktree": {
    "path": ".worktrees/{branch}"
  },
  "editor": {
    "name": "cursor",
    "themes": { "salt": "Monokai" },
    "colors": { "salt/staging": "#7b241c" }
  }
}
```

**Worktree path:** `worktree.path` controls where `gtl new` and `gtl review` create worktrees. Supports `{project}` and `{branch}` interpolation. Relative paths resolve from the repo root. If the resulting path is inside the repo, Treeline automatically ensures the directory is in `.gitignore`.

```json
{
  "worktree": {
    "path": ".worktrees/{branch}"
  }
}
```

Without this setting, worktrees are created as siblings of the repo (e.g. `../myapp-feature-x`).

**Router config:** `router.port` sets the port the subdomain router listens on (default 3001). Port 443 is forwarded here by `gtl serve install`.

**Tunnel config:** `tunnel.tunnels` stores named tunnel configurations, each with a domain. `tunnel.default` selects which tunnel is used when no `--tunnel` flag is passed. Run `gtl tunnel setup` to add tunnels interactively. Use `gtl tunnel default <name>` to switch the active default. Use `gtl tunnel remove <name>` to delete a tunnel from config (does not affect the Cloudflare tunnel itself). Old configs (`tunnel.name`/`tunnel.domain`) are auto-migrated on first load.

**Editor detection:** `gtl init` auto-detects your editor (Cursor, VS Code, Zed, JetBrains) and stores `editor.name`. The menulet uses this for "Open in Editor" labels. If detection fails, no name is stored — override manually with `gtl config set editor.name cursor`. The `themes` and `colors` maps are per-project or per-branch overrides for the `editor.theme` and `editor.color` settings in `.treeline.yml`.

**Port reservations** pin stable ports to specific projects or branches. A project-level key (e.g. `salt`) applies to the main repo. A `project/branch` key (e.g. `salt/staging`) pins a specific branch — useful for long-lived worktrees that need a known port. Branch-specific keys take priority over project-level keys. Reserved ports block the full `port.increment` range and are excluded from the dynamic pool so they never collide.

```bash
# Main repo always gets port 3000
gtl config set port.reservations.salt 3000

# The staging worktree always gets port 3020
gtl config set port.reservations.salt/staging 3020

# Another project's main repo
gtl config set port.reservations.truherd 3002
```

User config and registry live at the platform-appropriate location:

| Platform | Path |
|---|---|
| macOS | `~/Library/Application Support/git-treeline/` |
| Linux | `$XDG_CONFIG_HOME/git-treeline/` (defaults to `~/.config/git-treeline/`) |
| Windows | `%APPDATA%/git-treeline/` |

### Project config (`.treeline.yml`)

See [Framework examples](#framework-examples) for complete examples. Available fields:

| Field | Description |
|---|---|
| `project` | Project name (defaults to directory name). Once allocated, renaming triggers drift detection — see below |
| `merge_target` | Branch that `prune --merged` checks against (auto-detected if omitted) |
| `port_count` | Number of contiguous ports per worktree (default: 1) |
| `env_file` | Env file path (string shorthand, e.g. `.env.local`) — or map with `path` and `seed_from` when they differ |
| `database.adapter` | `postgresql` or `sqlite` |
| `database.template` | Source database to clone from (omit if no DB needed) |
| `database.pattern` | Naming pattern — `{template}_{worktree}` |
| `copy_files` | Files copied from main repo to worktree |
| `env` | Key-value pairs written to the env file, with token interpolation |
| `commands.setup` | Shell commands run in the worktree after setup |
| `commands.start` | Whatever you'd type to boot the app — `bin/dev`, `npm run dev`, `foreman start`, etc. (used by `gtl start` and `--start` on `new`/`review`) |
| `editor.title` | Window title template — `{project}`, `{port}`, `{branch}`, `{url}`, `{router_url}` tokens (VS Code, Cursor) |
| `editor.color` | Title/status bar color — `"auto"` (deterministic from branch), or hex like `"#1a5276"` |
| `editor.theme` | Full IDE theme override (e.g. `"Monokai"`, `"GitHub Dark"`) |
| `hooks.pre_setup` | Commands run before `commands.setup` — abort setup on failure |
| `hooks.post_setup` | Commands run after all setup — warn on failure |
| `hooks.pre_release` | Commands run before resource release — abort release on failure |
| `hooks.post_release` | Commands run after resource release — warn on failure |
| `hooks.<name>.auto` | `true` to run this hook on every fresh `gtl start` (default: `false`, requires `--with`) |
| `hooks.<name>.pre_start` | Shell command(s) run before the supervisor launches — string or array of strings |
| `hooks.<name>.post_stop` | Shell command(s) run in reverse order when the supervisor exits (Ctrl+C) — string or array |

### Interpolation tokens

Available in `env` values:

| Token | Value |
|---|---|
| `{port}` | First allocated port |
| `{port_N}` | Nth allocated port (e.g. `{port_2}`) |
| `{database}` | Database name (if configured) |
| `{redis_url}` | Full Redis URL |
| `{redis_prefix}` | Redis key prefix (if using prefixed strategy) |
| `{project}` | Project name |
| `{worktree}` | Worktree name |
| `{router_url}` | HTTPS router URL (e.g. `https://salt-feature.prt.dev`) — see note below |
| `{router_domain}` | Router base domain (e.g. `prt.dev` or `localhost`) |
| `{tunnel_host}` | Tunnel base domain (e.g. `gtltunnel.dev`) — empty if no tunnel configured |
| `{resolve:project}` | URL of another project's allocation (same-branch default, checks link overrides) |
| `{resolve:project/branch}` | URL of another project's allocation on a specific branch |

> **`{router_url}` vs `localhost:{port}` for your app's host:** Use `localhost:{port}` for your application's canonical host (e.g. `APPLICATION_HOST`, `APP_URL`). It works whether or not `gtl serve` is running and has zero external dependencies. Reserve `{router_url}` for values that specifically need the HTTPS router domain — CORS allowed origins, CSP directives, or cross-service display links.

### Project name drift detection

Once a worktree is allocated, the `project` name is stored in the registry and used for database names, Redis key prefixes, router URLs, and resolve links. If you rename `project:` in `.treeline.yml` without releasing and re-allocating, those persistent identifiers become stale.

`gtl start`, `gtl setup`, and `gtl env sync` detect this mismatch and prompt to revert `.treeline.yml` to the registry name. `gtl doctor` reports drift diagnostically.

To actually rename a project: release all worktrees (`gtl release --all --project <old-name>`), update `.treeline.yml`, then run `gtl setup`.

## Database cloning (optional)

If your project uses PostgreSQL or SQLite, Treeline can clone your development database per-worktree.

**PostgreSQL** uses `createdb --template` to clone your database. This copies the full schema and seed data without running migrations, so each worktree gets a complete database in seconds.

**SQLite** clones by copying the database file. Set `database.adapter: sqlite` and `database.template` to the path of your SQLite database.

Set `database.template` in your `.treeline.yml` to enable cloning. Omit it entirely if your project doesn't need database isolation, or if you use migrations instead (e.g. `npx prisma migrate deploy` in `commands.setup`).

Use `--drop-db` with `gtl release` to clean up cloned databases.

## Redis namespacing (optional)

If your project uses Redis, Treeline can assign each worktree its own namespace to prevent key collisions.

**Prefixed** (default): All worktrees share Redis DB 0, keys are namespaced (`myapp:feature-x:...`). No limit on concurrent worktrees.

**Database**: Each worktree gets its own Redis DB number (1-15). Use this if your app doesn't support key prefixing.

Configure in your user config under `redis.strategy`.

## Use with AI agents

Git Treeline is designed to support AI coding agents that work in worktrees. Any tool that creates worktrees — Conductor, Claude Code, Cursor — can use Treeline to ensure each worktree gets isolated resources.

### Agent context generation

`gtl init` writes a treeline section to `AGENTS.md` (created if it doesn't exist, appended otherwise). If `CLAUDE.md` exists but `AGENTS.md` doesn't, it appends there instead. The section tells agents to use `gtl port` for port discovery and lists the allocated env vars.

`AGENTS.md` is read by Cursor, Claude Code, and Codex — one file covers all three. Use `--skip-agent-config` to opt out.

Agents can query the port programmatically:

```bash
PORT=$(gtl port)
curl http://localhost:$PORT/health
```

### Lifecycle hooks

Most agent frameworks support setup/teardown hooks:

```bash
# On worktree creation
gtl setup .

# On worktree teardown
gtl release . --drop-db
```

### Programmatic access

Use `--json` for machine-readable output:

```bash
gtl status --json          # allocations + supervisor state + port listening
gtl doctor --json          # config, allocation, runtime, diagnostics
gtl port --json            # {"port": 3000, "ports": [3000, 3001]}
gtl db name --json         # {"database": "myapp_feature_xyz"}
```

`gtl status --json` automatically probes port listening and supervisor state for each allocation — no `--check` flag needed.

### Conductor

```json
{
  "setup": "gtl setup .",
  "archive": "gtl release . --drop-db"
}
```

## CLI reference

| Command | Flags | Description |
|---|---|---|
| `gtl install` | | Set up git-treeline for this project and machine (config, hook, setup, optional HTTPS) |
| `gtl init` | `--project` `--template-db` `--skip-agent-config` | Generate `.treeline.yml` (auto-detects framework, writes `AGENTS.md` section) |
| `gtl new <branch>` | `--base` `--path` `--start` `--open` `--dry-run` `--force`/`-f` | Create worktree + allocate + setup in one step |
| `gtl review <PR#>` | `--path` `--start` `--open` | Check out a GitHub PR into a worktree with full setup (requires `gh`) |
| `gtl switch <branch-or-PR#>` | `--setup` `--restart` | Switch worktree to a different branch or PR — fetches, checks out, refreshes env |
| `gtl setup [PATH]` | `--main-repo` `--dry-run` | Allocate resources and configure a worktree (idempotent) |
| `gtl release [PATH]` | `--drop-db` `--remove-worktree` `--project` `--all` `--force`/`-f` `--dry-run` | Free allocated resources (confirms before releasing unless `--force`) |
| `gtl port` | `--json` | Print the allocated port for the current worktree |
| `gtl refresh` | `--dry-run` `--force`/`-f` | Re-allocate all worktrees with current reservations; restarts supervised servers |
| `gtl doctor` | `--json` | Check config, allocation, runtime, and diagnostics |
| `gtl status` | `--project` `--json` `--check` `--watch` `--interval` | Show allocations across projects |
| `gtl worktrees` | aliases: `wt` | Interactive TUI picker for worktrees (grouped by project) |
| `gtl where <branch>` | | Print path to a worktree by branch name (use `project/branch` to disambiguate) |
| `gtl prune` | `--stale` `--merged` `--drop-db` `--remove-worktree` `--force` | Remove orphaned allocations |
| `gtl start` | `--await` `--await-timeout` | Run `commands.start` under supervisor (or resume). `--await` blocks until TCP-ready. |
| `gtl stop` | `--kill` | Stop the server process (supervisor stays alive). `--kill` shuts down the supervisor entirely. |
| `gtl restart` | | Restart the server process in the original terminal |
| `gtl open` | | Open current worktree in browser (prefers serve HTTPS URL) |
| `gtl clone <url>` | (passthrough to git) | Clone + detect framework + init + setup in one step |
| `gtl env` / `gtl env show` | `--json` `--template` | Print env file with Treeline-managed key annotations |
| `gtl env sync` | | Re-sync env file and editor settings from `.treeline.yml` |
| `gtl routes` | `--json` | Show routing URLs for the current worktree (all ports + tunnel) |
| `gtl resolve <project> [branch]` | `--json` | Look up another project's allocated URL |
| `gtl link <project> <branch>` | `--restart` `--json` | Override resolve target for a project |
| `gtl unlink <project>` | | Revert resolve override to same-branch default |
| `gtl completion <shell>` | | Output shell completion script (bash, zsh, fish, powershell) |
| `gtl serve` | | Local HTTPS subdomain router (`https://{branch}.prt.dev`) |
| `gtl serve install` | | One-time setup: CA trust, port forwarding, background service |
| `gtl serve status` | | Show router routes and service health |
| `gtl serve uninstall` | | Remove CA trust, port forwarding, and service |
| `gtl serve alias [name] [port]` | `--remove` | Add/remove/list static subdomain aliases. Port auto-detected from current directory when omitted |
| `gtl proxy <port> [target]` | `--tls` | Forward traffic from a stable port to a worktree port |
| `gtl tunnel [port]` | `--domain` `--tunnel` | Expose a local port via Cloudflare tunnel (quick or named) |
| `gtl tunnel setup` | | Interactive setup for named tunnels with BYO domain |
| `gtl tunnel status` | | Show all tunnel configurations and readiness |
| `gtl tunnel default [name]` | | Get or set the default tunnel configuration |
| `gtl tunnel remove <name>` | | Remove a named tunnel from local config |
| `gtl share [port]` | `--tunnel` `--tailscale` | Private share URL (Cloudflare token-gated or Tailscale tailnet) |
| `gtl config` | | Show or initialize user-level config |
| `gtl db` | `name` `reset` `restore` `drop` — `name --json` | Manage worktree databases |
| `gtl dashboard` | aliases: `dash`, `ui` | Interactive TUI for monitoring and managing all worktrees |
| `gtl mcp` | | MCP server for AI agents (started automatically by your editor) |
| `gtl version` | | Print version |

## Acknowledgments

Special thanks to [Josh Ferrara](https://github.com/joshferrara) for early feedback that shaped the direction of the project.

## License

MIT License — see [LICENSE.txt](LICENSE.txt).
