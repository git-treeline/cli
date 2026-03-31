# Git Treeline

Worktree environment manager — ports, databases, and Redis across parallel development environments.

## Why

Git worktrees have always let you check out multiple branches side by side. In practice, most developers kept one or two active at a time, and resource collisions were a minor annoyance you could fix with a shell alias.

That changed. AI coding agents work in worktrees. You might have three agents building features in parallel, each in its own worktree — and when they're done, you need to *run* each one to review the work. Boot the server, click through the UI, verify the behavior. You can't review what you can't run, and you can't run three copies of the same app when they're all fighting over port 3000, the same development database, and Redis DB 0.

The problem isn't new, but the scale is. What used to be a power-user edge case is now a daily workflow, and bespoke shell scripts per-repo don't hold up when you're managing worktrees across multiple projects on the same machine.

Git Treeline makes worktree resource allocation automatic and universal: every worktree gets its own port, database, and Redis namespace, tracked in a central registry that works across all your projects.

## How it works

Git Treeline has two layers of configuration and a central registry.

**Project config** (`.treeline.yml`, committed to your repo) describes what the project needs: which database to clone, which files to copy, which env vars to set, and what setup commands to run.

**User config** (`config.json`, on your machine) controls allocation policy: port range, increment, Redis strategy. This is per-developer, not per-project — it governs how resources are handed out across everything on your machine.

**The registry** (`registry.json`) is the ledger. When you run `git-treeline setup`, it allocates the next available port, clones the database from a template, assigns a Redis namespace, writes your env file, and records the allocation. When you run `git-treeline release`, it frees those resources. `git-treeline status` shows you everything that's allocated across all projects.

In a Rails app, the included Railtie reads the registry at boot and sets `ENV` vars before your initializers run — so `config/database.yml`, `config/puma.rb`, and Redis initializers pick up the right values with no code changes.

## Install

Add to your Gemfile:

```ruby
gem "git-treeline", group: :development
```

Or install standalone:

```bash
gem install git-treeline
```

## Quick start

### 1. Initialize your project

```bash
cd your-rails-app
git-treeline init --project myapp --template-db myapp_development
```

This creates `.treeline.yml` — commit it so your team shares the same config. It also creates your user-level `config.json` if it doesn't exist yet.

### 2. Set up a worktree

```bash
git worktree add ../myapp-feature-x feature-x
git-treeline setup ../myapp-feature-x
```

Git Treeline will:
- Allocate the next available port (3010, 3020, etc.)
- Clone your development database via `createdb --template`
- Assign a Redis namespace (prefixed by default, or a DB number)
- Copy credential files from the main repo
- Write your env file with all the allocated values
- Run your setup commands (`bundle install`, etc.)

### 3. Boot the worktree

```bash
cd ../myapp-feature-x
bin/dev
```

Visit http://localhost:3010. Your main app can run simultaneously on port 3000.

### 4. Check what's allocated

```bash
git-treeline status
```

```
myapp:
  :3010  feature-x   db:myapp_development_feature_x  prefix:myapp:feature-x
  :3020  bugfix-y    db:myapp_development_bugfix_y   prefix:myapp:bugfix-y

other-project:
  :3030  experiment  db:other_development_experiment  prefix:other:experiment
```

### 5. Release when done

```bash
git-treeline release ../myapp-feature-x --drop-db
git worktree remove ../myapp-feature-x
```

## Configuration

Git Treeline splits configuration into two layers:

- **User-level** (`config.json`) — allocation policy for your machine: port ranges, Redis strategy. Not project-specific.
- **Project-level** (`.treeline.yml`) — what the project needs: database template, files to copy, env mappings, setup commands. Committed to the repo.

Both files live at the platform-appropriate location:

| Platform | Path |
|---|---|
| macOS | `~/Library/Application Support/git-treeline/` |
| Linux | `$XDG_CONFIG_HOME/git-treeline/` (defaults to `~/.config/git-treeline/`) |
| Windows | `%APPDATA%/git-treeline/` |

### User config (`config.json`)

Created automatically by `git-treeline init` or `git-treeline config`. Edit to change allocation policy.

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

### Project config (`.treeline.yml`)

```yaml
project: myapp

# Environment file configuration
# target: file written in the worktree
# source: file copied from main repo as a starting point (falls back to .env)
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
  APPLICATION_HOST: "localhost:{port}"

setup_commands:
  - bundle install --quiet
  - yarn install --silent

editor:
  vscode_title: "{project} (:{port}) — {branch} — ${activeEditorShort}"
```

#### `env_file` options

Different frameworks and tools expect different env file conventions:

| Convention | `target` | `source` |
|---|---|---|
| dotenv (Rails default) | `.env.local` | `.env.local` |
| dotenv development | `.env.development.local` | `.env.development.local` |
| Plain `.env` | `.env` | `.env.example` |
| Next.js | `.env.local` | `.env.local` |
| Custom | any path | any path |

The `source` file is copied from the main repo as a starting point, then the `env` vars are applied as overrides. If the `source` doesn't exist, it falls back to `.env`.

## Redis strategies

### Prefixed (default, recommended)

All worktrees share Redis DB 0, but keys are namespaced:

```
myapp:feature-x:cache:...
myapp:feature-x:sidekiq:...
```

Works with Rails cache stores, Sidekiq, and ActionCable out of the box. No Redis DB limit.

### Database

Each worktree gets its own Redis DB number (1-15). Simple but limited to 15 concurrent worktrees across all projects. Use this if your app has code that doesn't support key prefixing.

## Rails integration

### Railtie

When added to a Rails app's Gemfile, the included Railtie automatically reads the registry and sets `ENV` vars in development before other initializers run. Your existing `config/database.yml`, `config/puma.rb`, and Redis initializers that read from `ENV` will pick up worktree-specific values with no code changes.

### `bin/setup-worktree`

Rails 8 established `bin/setup`, `bin/dev`, and `bin/ci` as conventional binstubs. Git Treeline fits naturally as `bin/setup-worktree` — a script your team commits that wraps the gem with any project-specific steps:

```bash
#!/usr/bin/env bash
set -euo pipefail

WORKTREE_DIR="${1:-.}"

# Treeline handles resource allocation, env file, and DB cloning
git-treeline setup "$WORKTREE_DIR"

# Project-specific steps beyond what .treeline.yml covers
cd "$WORKTREE_DIR"
bin/rails db:migrate
bin/rails assets:precompile
```

This keeps the convention familiar to any Rails developer while letting Treeline manage the hard parts (port/Redis/DB allocation across your machine).

## Use with Conductor

Conductor manages worktrees for AI coding agents. Git Treeline plugs into its lifecycle hooks so each agent gets isolated resources automatically.

**Prerequisites:**

1. `git-treeline` is installed on the machine running Conductor agents
2. `.treeline.yml` is committed to the repo (so it's present when Conductor checks out a branch from origin)
3. User config exists on the machine (one-time: `git-treeline config`)

**`conductor.json`:**

```json
{
  "setup": "git-treeline setup .",
  "archive": "git-treeline release . --drop-db"
}
```

`setup` runs when Conductor creates a worktree — allocates a port, clones the database, writes the env file. `archive` runs when the agent finishes — frees the port, drops the cloned database, removes the registry entry.

If you use a `bin/setup-worktree` binstub with project-specific steps beyond what `.treeline.yml` covers:

```json
{
  "setup": "bin/setup-worktree .",
  "archive": "git-treeline release . --drop-db"
}
```

## CLI reference

| Command | Description |
|---|---|
| `git-treeline init` | Generate `.treeline.yml` for current project |
| `git-treeline setup [PATH]` | Allocate resources and configure a worktree |
| `git-treeline release [PATH]` | Free allocated resources |
| `git-treeline status` | Show all allocations across projects |
| `git-treeline prune` | Remove stale allocations for deleted worktrees |
| `git-treeline config` | Show or initialize user-level config |
| `git-treeline version` | Print version |

## License

Copyright (c) 2026 Product Matter. All rights reserved.
