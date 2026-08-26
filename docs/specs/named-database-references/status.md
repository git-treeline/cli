# Status

## State

ready-for-review

## Done

- `database.extra` accepts a map of `key: pattern` alongside the list form
  (`internal/config/project.go`: `DatabaseExtras`, `DatabaseExtraKeys`,
  `DatabasePatterns`). Map entries are allocated in sorted-key order.
- `database.pattern` → `database.name` migration: the fold happens on the
  parsed YAML before defaults are merged, the on-disk rewrite in
  `migrateDatabaseName`. Idempotent; the legacy key is still read when the
  file cannot be rewritten.
- Dotted tokens `{database.name}` and `{database.extra.<key>}` mint alongside
  the permanent bare aliases `{database}` / `{database_N}`. Map-form extras
  mint no positional tokens.
- Validation at `Validate()` time: map key shape, empty/non-string patterns,
  dotted references to fields that do not exist (error names the path and
  lists the valid references).
- Registry keeps writing `database` and the ordered `databases` array, with
  map-form extras sorted by key after the primary; a `database_extra` map is
  written alongside so `{database.extra.<key>}` resolves from a registry entry
  alone (`gtl link`, `gtl start`).
- Allocation, tracking, drop, drift, and rename need no per-form handling:
  they all consume the `databases` array, which both forms populate.
- `gtl init` scaffolds and README examples switched to `database.name` and the
  dotted spellings; config table, token table, and the naming rule updated.

## In progress

- (nothing)

## Last green checkpoint

- `go test ./...` and `make ci` both green at commit `144c3ab` (docs) /
  `64f7df8` (implementation).

## Dead ends

- Folding `pattern` → `name` after `DeepMerge(ProjectDefaults, …)`, in the
  style of the other migrations: the defaults supply `database.name`, so a
  post-merge fold cannot distinguish a user-authored `name` from the default
  one and would drop the user's `pattern` value. The fold moved pre-merge into
  `load()`; only the disk rewrite stayed in the migration function.

## Corrections

- Unknown-dotted-token validation is scoped to the `database.` namespace
  rather than every `{a.b}` token. Nothing else mints dotted tokens today
  (§Non-goals), and rejecting arbitrary dotted braces would break existing
  configs whose env values legitimately contain `{…}` text for other tools.
