// Package internal is the root for git-treeline's domain packages.
//
// # Concurrency Model
//
// The codebase uses four distinct concurrency mechanisms. Each is
// purpose-built to avoid contention at a specific layer.
//
// ## 1. Registry file lock (advisory flock, 5s timeout)
//
// All mutations to registry.json acquire an exclusive flock on
// registry.json.lock. The lock uses a non-blocking retry loop with
// 100ms polling and a 5-second timeout. This supports concurrent
// CLI invocations (e.g. two terminals running `gtl setup`) without
// corrupting the shared state file. Writes use atomic temp+rename.
//
// See: registry.(*Registry).withLock
//
// ## 2. Router route table (sync.RWMutex)
//
// The subdomain router holds an in-memory route map protected by
// RWMutex. A background goroutine refreshes routes from the registry
// every 2 seconds (read path), while every HTTP request reads the map
// (hot read path). The write lock is only held during the swap.
//
// See: proxy.Router.mu, proxy.Router.refreshRoutes
//
// ## 3. TLS certificate cache (sync.RWMutex)
//
// CertManager caches issued leaf certificates keyed by hostname.
// Read lock for cache hits; write lock for new issuance. The cache
// is bounded to 1000 entries to prevent unbounded growth from
// garbage hostnames. Expired certs are lazily replaced on next access.
//
// See: proxy.CertManager.mu, proxy.CertManager.cache
//
// ## 4. Supervisor child lifecycle (sync.Mutex)
//
// The supervisor protects its child process pointer with a mutex.
// Start/stop/restart operations acquire the lock to prevent races
// between the Unix socket command handler and signal-driven shutdown.
//
// See: supervisor.Supervisor.mu
//
// ## 5. Localhost resolution cache (sync.Map, 5s TTL)
//
// The router probes whether each port is reachable on IPv4 or IPv6
// and caches the result for 5 seconds to avoid TCP probes on every
// proxied request. sync.Map is used because reads vastly outnumber
// writes and keys are stable (port numbers).
//
// See: proxy.localhostCache, proxy.resolveLocalhost
//
// ## What is NOT parallelized (and why)
//
// - Setup commands run sequentially. They may have ordering dependencies
//   (e.g. `bundle install` before `rails db:create`). Parallel execution
//   would require explicit dependency declaration in .treeline.yml.
//
// - Port allocation is serialized via the registry file lock. The
//   allocator reads all used ports, finds a free block, and writes back
//   atomically. Parallel allocation would require a compare-and-swap
//   protocol that adds complexity for minimal gain (allocation is fast).
//
// - `gtl status` runs health checks with a WaitGroup for parallelism
//   but does not limit concurrency. For typical setups (<20 worktrees)
//   this is fine. If worktree counts grow significantly, a semaphore
//   (like golang.org/x/sync/semaphore) would prevent fd exhaustion.
package internal
