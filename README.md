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
go install github.com/git-treeline/git-treeline@latest
```

### From release binary

Download the latest binary from [GitHub Releases](https://github.com/git-treeline/git-treeline/releases), extract, and place on your `PATH`.

> **Naming:** The binary is `git-treeline`, which also works as `git treeline` (git subcommand convention). Homebrew additionally installs `gtl` as a short alias. All three invocations are equivalent.

## Quick start

### 1. Initialize your project

```bash
cd your-project
gtl init
```

`init` auto-detects your framework (Next.js, Vite, Rails, Express, Python, Rust, Go) and generates a tailored `.treeline.yml`. It also writes a treeline section to `AGENTS.md` (or `CLAUDE.md` if that exists) so AI agents know to use `gtl port` instead of assuming port 3000. This works with Cursor, Claude Code, and Codex.

After generating the config, `init` runs framework-aware diagnostics and prints actionable warnings — for example, if your Vite project needs `vite.config.js` changes to read the allocated port, or if your Node project lacks a dotenv library. Commit the config so your team shares it.

Use `--project myapp` to set the project name explicitly, or `--skip-agent-config` to skip agent context generation.

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
gtl start      # resumes the server in your original terminal
gtl restart    # bounces the server in one step — logs keep flowing
```

`stop` + `start` lets agents pause the server, do work (run migrations, install packages), and bring it back — all in your terminal. `restart` is a single-step bounce. Ctrl+C in the terminal exits the supervisor entirely.

The supervisor communicates over a Unix socket. No background processes, no log files, no PID management. Your terminal owns the process; the socket is just a remote control.

Your app starts on 3010. The main copy runs on 3000. No collisions. Some frameworks (Rails, Express) read `PORT` from the env file automatically; others (Next.js) need their dev script wired up — `gtl init` prints framework-specific guidance.

### 4. Access worktrees by name instead of port

Once you're running multiple worktrees, remembering port numbers gets old. Git Treeline has three networking commands that give you human-readable URLs.

#### `gtl serve` — local HTTPS subdomain router

```bash
gtl serve install
```

One-time setup that installs a background router mapping `https://{project}-{branch}.localhost` to the correct worktree port. After install, `https://salt-staff-reporting.localhost` just works — no port numbers, trusted HTTPS, automatic certificate management.

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

### 5. Switch branches in a worktree

```bash
gtl switch feature-payments
gtl switch 42                  # accepts a PR number (resolved via gh)
gtl switch 42 --setup          # re-run setup commands after switching
```

Fetches from origin, checks out the branch, updates the registry, and refreshes the env file. Like `git switch` but Treeline-aware — handles fetch, env refresh, and PR lookup in one step.

### 6. Review a pull request

```bash
gtl review 42 --start
```

Fetches the PR branch via `gh`, creates a worktree, allocates resources, runs setup, and boots the app. Requires the [gh CLI](https://cli.github.com).

`review` and `new` must be run from the main repo, not from inside a worktree. If you're in a worktree and want to change branches, use `gtl switch`.

### 7. Check project health

```bash
gtl doctor
```

Checks config, allocation, runtime, and diagnostics in one view — whether `.treeline.yml` exists, env file is on disk, ports are allocated and listening, supervisor is running, and framework-specific guidance.

### 8. Check what's allocated

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

### 9. Release when done

```bash
gtl release ../myapp-feature-auth --drop-db
git worktree remove ../myapp-feature-auth
```

For bulk cleanup: `gtl release --project myapp --drop-db` or `gtl release --all --drop-db`.

### 10. Refresh after config changes

```bash
gtl refresh --dry-run    # preview what would change
gtl refresh              # apply changes, restart supervised servers
```

After adding or changing port reservations (or `port_count`), `gtl refresh` walks every allocation and re-resolves ports. Supervised servers (`gtl start`) are restarted automatically. Servers you started manually are flagged so you know which ones to bounce.

### 11. Prune stale allocations

```bash
gtl prune --stale
gtl prune --merged --drop-db
```

`--stale` removes allocations for worktrees that no longer exist on disk. `--merged` targets branches already merged into the merge target branch. Treeline auto-detects the merge target via git (works with any remote host), but you can set `merge_target` in `.treeline.yml` if your repo uses something other than `main`/`master` (e.g. `develop`, `staging`).

### 12. Manage worktree databases

```bash
gtl db name                          # print the worktree's database name
gtl db reset                         # drop and re-clone from the configured template
gtl db reset --from staging_snapshot # clone from a different database instead
gtl db restore dump.sql              # drop and restore from a pg_dump file
gtl db drop                          # just drop the database
```

`db reset` re-clones from the `database.template` in `.treeline.yml`. Use `--from` to override the source — useful when a teammate has a clean database or you've pulled a sanitized staging dump.

`db restore` auto-detects the dump format (pg_dump custom format vs plain SQL) and uses `pg_restore` or `psql` accordingly.

### 13. Manage user config

```bash
gtl config list                      # dump all settings
gtl config get port.base             # read a value (dot notation)
gtl config set port.base 4000        # write a value
gtl config path                      # print the config file location
gtl config edit                      # open in $EDITOR
```

### 14. AI agent integration (MCP)

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
    "name": "gtl",
    "domain": "myteam.dev"
  },
  "editor": {
    "name": "cursor",
    "themes": { "salt": "Monokai" },
    "colors": { "salt/staging": "#7b241c" }
  }
}
```

**Router config:** `router.port` sets the port the subdomain router listens on (default 3001). Port 443 is forwarded here by `gtl serve install`.

**Tunnel config:** `tunnel.name` and `tunnel.domain` are set by `gtl tunnel setup`. Once configured, `gtl tunnel` automatically creates named tunnel URLs like `https://salt-staff-reporting.myteam.dev`.

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
| `project` | Project name (defaults to directory name) |
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
| `editor.title` | Window title template — `{project}`, `{port}`, `{branch}` tokens (VS Code, Cursor) |
| `editor.color` | Title/status bar color — `"auto"` (deterministic from branch), or hex like `"#1a5276"` |
| `editor.theme` | Full IDE theme override (e.g. `"Monokai"`, `"GitHub Dark"`) |

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
gtl status --json
```

This returns the full registry as JSON — allocated ports, databases, Redis namespaces, and worktree paths. Useful for agent orchestrators that need to know what's running where.

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
| `gtl init` | `--project` `--template-db` `--skip-agent-config` | Generate `.treeline.yml` (auto-detects framework, writes `AGENTS.md` section) |
| `gtl new <branch>` | `--base` `--path` `--start` `--dry-run` | Create worktree + allocate + setup in one step |
| `gtl review <PR#>` | `--path` `--start` | Check out a GitHub PR into a worktree with full setup (requires `gh`) |
| `gtl switch <branch-or-PR#>` | `--setup` | Switch worktree to a different branch or PR — fetches, checks out, refreshes env |
| `gtl setup [PATH]` | `--main-repo` `--dry-run` | Allocate resources and configure a worktree (idempotent) |
| `gtl release [PATH]` | `--drop-db` `--project` `--all` `--force`/`-f` `--dry-run` | Free allocated resources (confirms before releasing unless `--force`) |
| `gtl port` | | Print the allocated port for the current worktree |
| `gtl refresh` | `--dry-run` `--force`/`-f` | Re-allocate all worktrees with current reservations; restarts supervised servers |
| `gtl doctor` | | Check config, allocation, runtime, and diagnostics |
| `gtl status` | `--project` `--json` `--check` `--watch` `--interval` | Show allocations across projects |
| `gtl prune` | `--stale` `--merged` `--drop-db` `--force` | Remove orphaned allocations |
| `gtl start` | | Run `commands.start` under supervisor (or resume a stopped server) |
| `gtl stop` | | Stop the server process (supervisor stays alive) |
| `gtl restart` | | Restart the server process in the original terminal |
| `gtl serve` | | Local HTTPS subdomain router (`https://{branch}.localhost`) |
| `gtl serve install` | | One-time setup: CA trust, port forwarding, background service |
| `gtl serve status` | | Show router routes and service health |
| `gtl serve uninstall` | | Remove CA trust, port forwarding, and service |
| `gtl proxy <port> [target]` | `--tls` | Forward traffic from a stable port to a worktree port |
| `gtl tunnel [port]` | `--domain` | Expose a local port via Cloudflare tunnel (quick or named) |
| `gtl tunnel setup` | | Interactive setup for named tunnels with BYO domain |
| `gtl tunnel status` | | Show tunnel configuration and readiness |
| `gtl config` | | Show or initialize user-level config |
| `gtl db` | `name` `reset` `restore` `drop` | Manage worktree databases |
| `gtl mcp` | | MCP server for AI agents (started automatically by your editor) |
| `gtl version` | | Print version |

## License

MIT License — see [LICENSE.txt](LICENSE.txt).
