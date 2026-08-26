# Named database references

Shipped in 0.57.0 (PR #130). Spec archived at `docs/archive/named-database-references/`.

## What exists

`.treeline.yml`'s database block addresses databases by name, not position:

```yaml
database:
  template: salt_development
  name: "salt_{worktree}"        # was `pattern`; migrated in place on load
  extra:
    test: "{database.name}_test" # map form; list form still supported
env:
  DATABASE_NAME: "{database.name}"
  TEST_DATABASE_NAME: "{database.extra.test}"
```

The token vocabulary encodes one rule: **a dotted token is a path to a field
in this file; a bare token is minted by treeline.** `{database}` and
`{database_N}` are permanent aliases, not deprecations — list-form configs
render byte-identical output and emit no warnings.

## Why it is shaped this way

- **Named over positional.** `{database_2}` for `extra[0]` was opaque; map
  keys make the env block self-documenting end to end.
- **`pattern` → `name`** because the dotted token must literally address the
  field (`{database.name}`); `{database.pattern}` would re-import the
  confusion the feature removes. The fold happens on the parsed YAML *before*
  defaults merge (defaults supply `database.name`, so a post-merge fold cannot
  tell a user-authored `name` from the default); only the disk rewrite lives
  in `migrateDatabaseName`.
- **References are rejected, not chained.** Database names render
  independently — the primary first, then each extra against it in sorted-key
  order — so an extra may reference only `{database.name}`, and `database.name`
  may reference no database. Chained substitution was rejected: it would
  contradict the sorted-by-key independent-render contract the registry
  ordering rests on.
- **Environment-keyed schemas (`database.development`) were rejected** —
  treeline is framework-agnostic; the cloned/named distinction is not about
  environments. `template` → `clone_from` was deferred, not rejected.
- **Registry compatibility.** Legacy `database`/`databases` fields are still
  written (map extras sorted by key after the primary at index 0); a
  `database_extra` map rides alongside so dotted tokens resolve from a
  registry entry alone. One caveat: once a config migrates to `database.name`
  on disk, pre-0.57 binaries fall back to the default pattern.
- **Dotted validation is scoped to the `database.` namespace** — nothing else
  mints dotted tokens, and rejecting arbitrary `{a.b}` braces would break env
  values carrying literal `{…}` for other tools.

## Corrections (promotion record)

- **Caught by adversarial review:** the first cut of dotted-reference
  validation allowed any defined token at any site, so a chained reference
  (`shard: "{database.extra.test}_0"`) validated and then survived rendering
  as a literal, sanitized into a persisted garbage name. Fixed by giving each
  site the set it can actually reach (see rationale above). Lesson: validate
  against what the *render path* resolves, not against what is defined.
