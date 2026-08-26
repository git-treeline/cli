# Open questions

None open.

## Resolved

### Should a degraded non-strict run exit non-zero?
Decided 2026-08-26 by jonathan: **exit 0** (option a). Scripts that need a
readiness guarantee use `--strict`; a non-zero default would break existing
callers that treat any non-zero as failure.

### Should `.treeline.yml` be able to make strict the default?
Decided 2026-08-26 by jonathan: **flag only** (option a). A `database.strict`
config key can be added later if a real repo asks; speculative config is
scope creep.
