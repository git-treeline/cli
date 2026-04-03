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

### From source (requires Go 1.22+)

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

`init` auto-detects your framework (Next.js, Vite, Rails, Express, Python, Rust, Go) and generates a tailored `.treeline.yml`. It also creates agent context files (`.cursor/rules/treeline.mdc` or `CLAUDE.md`) so AI tools understand the setup.

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

### 4. Review a pull request

```bash
gtl review 42 --start
```

Fetches the PR branch via `gh`, creates a worktree, allocates resources, runs setup, and boots the app. Requires the [gh CLI](https://cli.github.com).

### 5. Check what's allocated

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

### 6. Release when done

```bash
gtl release ../myapp-feature-auth --drop-db
git worktree remove ../myapp-feature-auth
```

For bulk cleanup: `gtl release --project myapp --drop-db` or `gtl release --all --drop-db`.

### 7. Prune stale allocations

```bash
gtl prune --stale
gtl prune --merged --drop-db
```

`--stale` removes allocations for worktrees that no longer exist on disk. `--merged` targets branches already merged into the merge target branch. Treeline auto-detects the merge target via git (works with any remote host), but you can set `merge_target` in `.treeline.yml` if your repo uses something other than `main`/`master` (e.g. `develop`, `staging`).

### 8. Manage worktree databases

```bash
gtl db name                          # print the worktree's database name
gtl db reset                         # drop and re-clone from the configured template
gtl db reset --from staging_snapshot # clone from a different database instead
gtl db restore dump.sql              # drop and restore from a pg_dump file
gtl db drop                          # just drop the database
```

`db reset` re-clones from the `database.template` in `.treeline.yml`. Use `--from` to override the source — useful when a teammate has a clean database or you've pulled a sanitized staging dump.

`db restore` auto-detects the dump format (pg_dump custom format vs plain SQL) and uses `pg_restore` or `psql` accordingly.

### 9. Manage user config

```bash
gtl config list                      # dump all settings
gtl config get port.base             # read a value (dot notation)
gtl config set port.base 4000        # write a value
gtl config path                      # print the config file location
gtl config edit                      # open in $EDITOR
```

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

env_file:
  target: .env.local
  source: .env.local

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

env_file:
  target: .env.local
  source: .env.local

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

env_file:
  target: .env.local
  source: .env.local

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

env_file:
  target: .env
  source: .env.example

env:
  PORT: "{port}"

commands:
  setup:
    - npm install
  start: node server.js
```

### Rails

```yaml
project: myapp
merge_target: develop     # branch that prune --merged checks against (auto-detected if omitted)
ports_needed: 2

env_file:
  target: .env.local
  source: .env.local

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

env_file:
  target: .env.local
  source: .env.local

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
    "increment": 10
  },
  "redis": {
    "strategy": "prefixed",
    "url": "redis://localhost:6379"
  }
}
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
| `ports_needed` | Number of contiguous ports per worktree (default: 1) |
| `env_file.target` | Env file written in the worktree |
| `env_file.source` | Env file copied from main repo as a starting point |
| `database.adapter` | `postgresql` or `sqlite` |
| `database.template` | Source database to clone from (omit if no DB needed) |
| `database.pattern` | Naming pattern — `{template}_{worktree}` |
| `copy_files` | Files copied from main repo to worktree |
| `env` | Key-value pairs written to the env file, with token interpolation |
| `commands.setup` | Shell commands run in the worktree after setup |
| `commands.start` | Whatever you'd type to boot the app — `bin/dev`, `npm run dev`, `foreman start`, etc. (used by `gtl start` and `--start` on `new`/`review`) |
| `editor.vscode_title` | VS Code window title template |

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

`gtl init` auto-generates context files that teach AI agents how to use Treeline in your project:

- `.cursor/rules/treeline.mdc` for Cursor
- `CLAUDE.md` section for Claude Code

Use `--skip-agent-config` to opt out.

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
| `gtl init` | `--project` `--template-db` `--skip-agent-config` | Generate `.treeline.yml` (auto-detects framework and creates agent context files) |
| `gtl new <branch>` | `--base` `--path` `--start` `--dry-run` | Create worktree + allocate + setup in one step |
| `gtl review <PR#>` | `--path` `--start` | Check out a GitHub PR into a worktree with full setup (requires `gh`) |
| `gtl setup [PATH]` | `--main-repo` `--dry-run` | Allocate resources and configure a worktree (idempotent) |
| `gtl refresh [PATH]` | | Re-interpolate env file from existing allocation |
| `gtl release [PATH]` | `--drop-db` `--project` `--all` `--force`/`-f` `--dry-run` | Free allocated resources (single, by project, or all) |
| `gtl status` | `--project` `--json` `--check` `--watch` `--interval` | Show allocations across projects |
| `gtl prune` | `--stale` `--merged` `--drop-db` `--force` | Remove orphaned allocations |
| `gtl start` | | Run `commands.start` under supervisor (or resume a stopped server) |
| `gtl stop` | | Stop the server process (supervisor stays alive) |
| `gtl restart` | | Restart the server process in the original terminal |
| `gtl config` | | Show or initialize user-level config |
| `gtl version` | | Print version |

## License

MIT License — see [LICENSE.txt](LICENSE.txt).
