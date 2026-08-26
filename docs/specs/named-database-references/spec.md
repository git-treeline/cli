---
slug: named-database-references
type: feature
status: building
stale_after: 2026-09-25
decider: jonathan
blast_radius: medium
size: small
created: 2026-08-26
---

# Named database references

## Intent

`.treeline.yml`'s database block currently addresses auxiliary databases by
position: `extra` is a list, and its first entry surfaces as the env token
`{database_2}` (the primary is `{database_1}`). A reader of a project's config
cannot tell what `{database_2}` is, and cannot see that `{database}` — used
inside `extra` patterns and `env:` blocks — is the rendered result of the
`pattern` field a few lines above it. The wiring between the field that mints a
value and the token that uses it is invisible; users have to leave the file and
research.

This spec makes the database block self-explanatory by (a) letting `extra` be a
map of named databases instead of a positional list, and (b) introducing
dotted self-reference tokens that spell out the config path they read from
(`{database.name}`, `{database.extra.test}`). Everything is additive: every
existing config, env template, and registry stays valid, with no warnings and
no forced migration.

Appetite: one sitting. This changes config parsing, token minting, docs, and
nothing about what treeline does to a database server.

## Goal

A `.treeline.yml` using map-form `extra` and dotted tokens round-trips through
`gtl` allocation to produce env values identical to the equivalent list-form
config using `{database}`/`{database_2}` — and both spellings resolve
side by side in one config.

## Non-goals

- **No behavioral change to databases.** Treeline still clones only the
  primary from `database.template` and still only names, tracks, and drops
  extras. No cloning or creation of extras is introduced.
- **No key renames beyond `pattern` → `name`.** `database.template` keeps its
  name; the `template` → `clone_from` idea is explicitly deferred.
- **No environment-keyed schema** (`database.development`, `database.test`).
  Considered and rejected: treeline is framework-agnostic and does not have
  environments.
- **No deprecation of the bare tokens.** `{database}` and `{database_N}` are
  permanent aliases, not legacy. No warnings are emitted for them.
- **No dotted forms for tool-minted tokens.** `{port}`, `{redis_url}`,
  `{router_url}`, etc. are unchanged; dotted tokens exist only for values the
  user authors in `.treeline.yml`, which today means database names only.
- **No config schema version field.** The repo's existing idempotent
  field-level migration pattern is used, nothing new.

## Behavior

1. `database.extra` accepts a **map** of `key: pattern` entries in addition to
   the existing list form. Keys are lowercase identifiers (same character rules
   as other treeline identifiers). A given config uses one form or the other;
   `extra` as neither list nor map is a validation error, as today.
2. Each map entry mints the token `{database.extra.<key>}`, resolving to that
   extra's rendered database name. Map-form configs mint **no** positional
   `{database_N}` tokens for extras.
3. List-form `extra` behaves exactly as today, including `{database_2}`,
   `{database_3}`, … tokens. Existing configs produce byte-identical env output
   before and after this change.
4. The token `{database.name}` resolves to the rendered primary database name,
   accepted everywhere `{database}` is accepted today (extra patterns, `env:`
   values, copy templates). `{database}` remains a permanent alias for it.
5. The config key `database.pattern` is renamed to `database.name`, migrated
   in place on load by an idempotent migration in the style of
   `default_branch` → `merge_target`: the in-memory map and the on-disk YAML
   are both rewritten; a config already using `name` is untouched; `pattern`
   continues to be read if migration cannot write.
6. Map-form extras are allocated, tracked in the registry, dropped on
   `release`/`prune --drop-db` (including parallel-test shard discovery), and
   carried through rename/drift exactly like list-form extras.
7. `gtl` never creates map-form extras, exactly as with list-form — naming and
   tracking only.
8. README documents the map form, the dotted tokens, and the one-sentence
   naming rule: *a dotted token is a path to a field in this file; a bare token
   is minted by treeline.* Examples switch to the dotted spellings; the bare
   aliases stay documented alongside.

## Business rules

- **Must:** The registry keeps writing the legacy `database` (string) and
  `databases` (ordered array) fields so older `gtl` binaries sharing a registry
  keep working. Map-form extras serialize into `databases` in a deterministic
  order (sorted by key) after the primary at index 0.
- **Must:** Validation rejects map keys that are not valid identifiers, empty
  pattern values, and a `{database.extra.<key>}` reference to a key that does
  not exist — at `Validate()` time, not at render time.
- **Must:** The `pattern` → `name` migration is idempotent and safe under the
  repo's existing migration harness (atomic YAML rewrite, no-op when absent).
- **Must:** `{database.name}` inside an `extra` pattern resolves to the
  *rendered* primary name (post-`{worktree}` substitution), identical to
  `{database}` semantics today.
- **Should:** Unknown dotted tokens fail with an error naming the unresolved
  path, since a dotted token is a user-authored reference and a typo is a
  config bug, not a passthrough.
- **May:** `gtl init` scaffolds may prefer the map form and dotted tokens in
  generated examples.

## Assumptions

- YAML unmarshalling into `map[string]any` does not preserve map key order in
  this codebase; deterministic registry ordering therefore cannot rely on
  declaration order. Sorted-by-key is the contract (see Business rules). If the
  parser turns out to preserve order, sorted-by-key still wins — do not switch
  silently.
- Older `gtl` binaries consume the registry's `databases` array positionally
  and tolerate reordering between allocations of *different* worktrees, but a
  single worktree's array must be stable across reads. If this assumption
  breaks, escalate rather than change the serialization shape.
- The interpolation tokenizer can accept `.` inside `{...}` without colliding
  with any existing token. If an existing token already contains a dot,
  escalate.

## Critical files

- `internal/config/project.go` — `ProjectDefaults` (~:58), `Validate()`
  (~:113), `DatabasePatterns()` (~:212), migration precedents
  `migrateDefaultBranch` (~:731) and siblings.
- `internal/allocator/allocator.go` — `buildDatabaseNames` (~:658),
  `renderExtraNames` (~:679), registry `databases` field (~:104, :350).
- `internal/interpolation/interpolation.go` — `{database_N}` minting (~:92).
- `internal/setup/setup.go` — `dropStaleExtraDatabases` (~:527).
- `cmd/drift.go`, `cmd/release.go` — extras carry-along and drop paths.
- `README.md` — `database.extra` row (~:682), token table (~:712).

## Acceptance checks

### agent-loopable

- Full suite green — run: `go test ./...`
- Table-driven tests cover: map-form parsing and validation, dotted-token
  resolution (`{database.name}`, `{database.extra.<key>}`), alias equivalence
  (`{database}` ≡ `{database.name}`; list-form `{database_2}` unchanged),
  unknown-dotted-token error, and `pattern` → `name` migration idempotence
- A fixture config using list-form `extra` produces byte-identical rendered
  env output before and after the change

### judgeable

- Implementation matches §Behavior items 1–8; in particular no code path
  creates or clones an extra database (§Non-goals, first bullet)
- README changes match §Behavior item 8 and follow the repo's README update
  policy in `CLAUDE.md` (config tables mirror the Go structs; token table
  updated)
- Registry serialization matches the ordering contract in §Business rules
  (first Must)

### human-gate

- Jonathan rewrites the Rails repo's `.treeline.yml` from the screenshot into
  map form with dotted tokens and confirms it reads as self-explanatory to the
  other engineer

## Out of scope / deferred

- `template` → `clone_from` rename (verb-naming the clone action) — deferred,
  standalone follow-up if the opacity still bothers in practice.
- Dotted tokens for any future user-authored fields outside `database` — the
  convention generalizes but nothing else mints today.
- Deprecation or removal of list-form `extra` and positional tokens — not
  planned.
