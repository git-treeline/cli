# Contributing to Git Treeline

Thanks for considering a contribution. Here's what you need to know.

## Setup

```bash
git clone https://github.com/git-treeline/cli.git
cd cli
make ci
```

Requires Go 1.24+ and optionally `golangci-lint` and `govulncheck` (auto-installed by `make ci` if missing).

## Making changes

1. Fork the repo and create a branch from `main`.
2. Write tests for new behavior.
3. Run `make ci` and ensure all checks pass.
4. Open a pull request with a clear description of the change and why it's needed.

## Testing conventions

### Making code testable

When production code calls external processes or OS-level APIs, use the lightest
injection pattern that fits. In order of preference:

1. **Pure extraction** — split decision logic into a standalone function with no
   side effects. No injection needed. Preferred whenever possible.
   ```go
   // Public wrapper
   func TunnelExists(name string) bool { ... parseTunnelListHasName(output, name) }
   // Testable pure function
   func parseTunnelListHasName(data []byte, name string) bool { ... }
   ```

2. **Func params** — when the helper needs 1–2 injected behaviors, pass them as
   function arguments. Keep the public function as a thin wrapper.
   ```go
   func VerifyDNS(host string, timeout time.Duration) bool {
       return verifyDNSWith(host, timeout, net.LookupHost, 2*time.Second)
   }
   ```

3. **Deps struct** — when a function depends on 3+ external behaviors, group them
   into a struct with a factory that wires the production defaults.
   ```go
   type healthDeps struct { isRunning func() bool; ... }
   func defaultHealthDeps() healthDeps { return healthDeps{isRunning: IsRunning, ...} }
   func CheckHealth(...) []HealthCheck { return checkHealthWith(defaultHealthDeps(), ...) }
   ```

4. **Nil-check struct fields** — use sparingly. Existing code in
   `database/postgresql.go` and `database/sqlite.go` uses this pattern; avoid
   proliferating it in new code.

### Known test limitations

- **`run` vs `runSilent` in PostgreSQL**: both route through the same `execRun`
  mock, so tests cannot detect regressions in stdout/stderr routing. This is
  acceptable — tests verify argument correctness and error handling; stdout
  routing is a UI concern.
- **`os.Chdir` in worktree tests**: several worktree functions rely on
  process-wide cwd rather than a `dir` parameter, making those tests
  incompatible with `t.Parallel()`. The long-term fix is to thread `dir`
  through the production API.

## Pull request expectations

- One logical change per PR.
- Include a test plan in the PR description.
- Keep commits focused — squash fixups before requesting review.

## Reporting bugs

Open an issue with steps to reproduce, expected behavior, and actual behavior. Include your OS and `git-treeline version` output.

## Security vulnerabilities

Please report security issues privately — see [SECURITY.md](SECURITY.md).
