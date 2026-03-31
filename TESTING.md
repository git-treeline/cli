# Testing git-treeline locally

End-to-end test of git-treeline against a real Rails app, installed from source.

## Prerequisites

- PostgreSQL running locally
- A Rails app with a working `_development` database
- The app reads `PORT`, `DATABASE_NAME`, and `REDIS_URL` from ENV (standard Rails conventions via dotenv, `config/database.yml`, `config/puma.rb`, etc.)

## Setup

### Install the gem from source

```bash
cd ~/git/product-matter/git-treeline
gem build git-treeline.gemspec
gem install ./git-treeline-0.1.0.gem
git-treeline version
```

### Add it to your Rails app's Gemfile

In your Rails app, add the gem pointing at your local source so the Railtie loads at boot:

```ruby
# Gemfile
gem "git-treeline", path: "~/git/product-matter/git-treeline", group: :development
```

```bash
bundle install
```

### Initialize the project

```bash
git-treeline init --project yourapp --template-db yourapp_development
```

Review what it generated:

```bash
cat .treeline.yml
git-treeline config
```

Edit `.treeline.yml` if needed. The defaults assume:
- PostgreSQL with `createdb --template` for DB cloning
- `.env.local` as the env file
- `bundle install --quiet` as a setup command

Adjust `copy_files`, `setup_commands`, and `env` to match your app.

## Test it

### Create a branch and worktree

```bash
git checkout -b test/treeline-smoke
git checkout -  # go back to your working branch

git worktree add ../yourapp-treeline-test test/treeline-smoke
```

### Run setup

```bash
git-treeline setup ../yourapp-treeline-test
```

Expected output:

```
==> Allocating port 3010 for 'yourapp-treeline-test'
==> Database: yourapp_development_yourapp_treeline_test
==> Terminating connections to yourapp_development
==> Cloning database yourapp_development -> yourapp_development_yourapp_treeline_test
==> Database cloned
==> .env.local written
==> Done! Worktree 'yourapp-treeline-test' ready:
==>   Port:     3010
==>   Database: yourapp_development_yourapp_treeline_test
==>   Redis:    redis://localhost:6379
==>   URL:      http://localhost:3010
==>   Dir:      .../yourapp-treeline-test
```

### Verify the allocation

```bash
git-treeline status
cat ../yourapp-treeline-test/.env.local
psql -lqt | grep yourapp_development_yourapp_treeline_test
```

### Verify the Railtie injects ENV vars

```bash
cd ../yourapp-treeline-test
bin/rails runner 'puts "PORT=#{ENV["PORT"]} DATABASE_NAME=#{ENV["DATABASE_NAME"]} REDIS_URL=#{ENV["REDIS_URL"]}"'
```

You should see the allocated values, not the main app's defaults. This confirms the Railtie read the registry and set ENV before your initializers ran.

### Boot both apps simultaneously

Terminal 1 — main app:

```bash
cd ~/path/to/yourapp
bin/dev
# Starts on port 3000
```

Terminal 2 — worktree:

```bash
cd ../yourapp-treeline-test
bin/dev
# Starts on port 3010
```

Visit both in your browser. They should run independently with separate databases.

### Create a second worktree (optional)

```bash
cd ~/path/to/yourapp
git checkout -b test/treeline-smoke-2
git checkout -
git worktree add ../yourapp-treeline-test2 test/treeline-smoke-2
git-treeline setup ../yourapp-treeline-test2
git-treeline status
```

Should show two worktrees with ports 3010 and 3020, separate databases.

## Clean up

```bash
cd ~/path/to/yourapp

# Release resources (--drop-db removes the cloned database)
git-treeline release ../yourapp-treeline-test --drop-db
git-treeline release ../yourapp-treeline-test2 --drop-db

# Remove worktrees
git worktree remove ../yourapp-treeline-test
git worktree remove ../yourapp-treeline-test2

# Delete test branches
git branch -D test/treeline-smoke
git branch -D test/treeline-smoke-2

# Confirm everything is clean
git-treeline status
psql -lqt | grep yourapp_development_yourapp

# Remove .treeline.yml and Gemfile change if you don't want to keep them
git checkout Gemfile Gemfile.lock
rm .treeline.yml
```

## Troubleshooting

**`git-treeline: command not found`** — `gem install` didn't put the executable on your PATH. Use the full path instead: `ruby -Ilib ~/git/product-matter/git-treeline/exe/git-treeline`.

**Database clone fails with "source database is being accessed"** — Close any open connections to the template database: `rails console`, `bin/dev`, database GUIs, etc.

**Worktree boots on wrong port** — Check that your `config/puma.rb` reads from `ENV["PORT"]` (e.g. `port ENV.fetch("PORT", 3000)`). If it's hardcoded, the Railtie can set the var but Puma won't read it.

**Railtie doesn't set vars** — Confirm the gem is in your Gemfile and `bundle install` succeeded. The Railtie only runs in `Rails.env.development?` and only when an allocation exists in the registry for `Dir.pwd`.

**`bin/dev` uses foreman/overmind with a Procfile** — Make sure the Procfile uses `$PORT` or the env var, not a hardcoded port number.
