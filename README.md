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

`init` auto-detects your framework (Next.js, Rails, Express, Python, Rust, Go) and generates a tailored `.treeline.yml`. It also creates agent context files (`.cursor/rules/treeline.mdc` or `CLAUDE.md`) so AI tools understand the setup. Commit the config so your team shares it.

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
npm run dev    # or bin/dev, or whatever starts your app
```

Your app reads `PORT` from the env file and starts on 3010. The main copy runs on 3000. No collisions.

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

`--stale` removes allocations for worktrees that no longer exist on disk. `--merged` targets branches already merged into the default branch. Treeline auto-detects the default branch via git (works with any remote host), but you can set `default_branch` in `.treeline.yml` if your repo uses something other than `main`/`master` (e.g. `develop`, `staging`).

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

setup_commands:
  - npm install

start_command: npm run dev
```

Next.js reads `PORT` from `.env.local` automatically. That's all most Next apps need.

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

setup_commands:
  - npm install
  - npx prisma migrate deploy

start_command: npm run dev
```

### Node.js / Express

```yaml
project: myapi

env_file:
  target: .env
  source: .env.example

env:
  PORT: "{port}"

setup_commands:
  - npm install

start_command: node server.js
```

### Rails

```yaml
project: myapp
default_branch: develop   # omit if your default branch is main
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

setup_commands:
  - bundle install --quiet
  - yarn install --silent

start_command: bin/dev
```

For automatic ENV injection at Rails boot, see [git-treeline-rails](https://github.com/git-treeline/git-treeline-rails).

### Frontend SPA (no server resources)

```yaml
project: dashboard

env_file:
  target: .env.local
  source: .env.local

env:
  PORT: "{port}"

setup_commands:
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
| `default_branch` | Default branch name for `prune --merged` (auto-detected if omitted) |
| `ports_needed` | Number of contiguous ports per worktree (default: 1) |
| `env_file.target` | Env file written in the worktree |
| `env_file.source` | Env file copied from main repo as a starting point |
| `database.adapter` | `postgresql` or `sqlite` |
| `database.template` | Source database to clone from (omit if no DB needed) |
| `database.pattern` | Naming pattern — `{template}_{worktree}` |
| `copy_files` | Files copied from main repo to worktree |
| `env` | Key-value pairs written to the env file, with token interpolation |
| `setup_commands` | Shell commands run in the worktree after setup |
| `start_command` | Command to boot the app (used by `--start` on `new` and `review`) |
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

Set `database.template` in your `.treeline.yml` to enable cloning. Omit it entirely if your project doesn't need database isolation, or if you use migrations instead (e.g. `npx prisma migrate deploy` in `setup_commands`).

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
| `gtl config` | | Show or initialize user-level config |
| `gtl version` | | Print version |

## License

Apache License 2.0 — see [LICENSE.txt](LICENSE.txt).
