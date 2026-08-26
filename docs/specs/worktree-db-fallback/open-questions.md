# Open questions

None open.

Resolved 2026-08-26 by jonathan:

- **Degraded non-strict exit code** → exit 0. Scripts that need a readiness
  guarantee use `--strict`; a non-zero default would break existing callers
  that treat any non-zero as failure.
- **Config key to make strict the default** → flag only. A `database.strict`
  key can be added later if a real repo asks; speculative config is scope
  creep.
