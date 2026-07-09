// Package allocator provides resource allocation for git worktrees.
// It manages port assignment, database name generation, and Redis
// isolation (via database numbers or key prefixes) to enable parallel
// development environments.
package allocator

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/git-treeline/cli/internal/config"
	"github.com/git-treeline/cli/internal/interpolation"
	"github.com/git-treeline/cli/internal/registry"
	"github.com/git-treeline/cli/internal/supervisor"
)

// Allocator manages resource allocation using user and project configuration.
// It tracks used resources via the registry to avoid conflicts between worktrees.
type Allocator struct {
	UserConfig    *config.UserConfig
	ProjectConfig *config.ProjectConfig
	Registry      *registry.Registry
	// SupervisorLive reports whether the given worktree has its own live
	// supervisor (its control socket responds). Used during reuse so a busy
	// allocated port held by the worktree's own dev server isn't mistaken for a
	// foreign squatter. Injectable for tests; nil falls back to a socket probe.
	SupervisorLive func(worktreePath string) bool
	// DryRun selects resources against the current registry snapshot without
	// persisting them (for previewing an allocation). When false, a new-worktree
	// allocation is chosen and written atomically inside the registry lock.
	DryRun bool
}

// supervisorResponding probes the worktree's supervisor control socket. It is
// the default SupervisorLive implementation: a response (of any kind) means the
// worktree owns whatever is listening on its allocated ports.
func supervisorResponding(worktreePath string) bool {
	_, err := supervisor.Send(supervisor.SocketPath(worktreePath), "status")
	return err == nil
}

func (al *Allocator) hasLiveSupervisor(worktreePath string) bool {
	if al.SupervisorLive != nil {
		return al.SupervisorLive(worktreePath)
	}
	return supervisorResponding(worktreePath)
}

// Allocation represents the resources assigned to a worktree including
// ports, database name, and Redis configuration. Reused is true when
// an existing allocation was found rather than creating a new one.
type Allocation struct {
	Project         string
	Worktree        string
	WorktreeName    string
	Branch          string
	Port            int
	Ports           []int
	Database        string
	DatabaseAdapter string
	RedisDB         int
	RedisPrefix     string
	Reused          bool
}

func (a *Allocation) ToRegistryEntry() registry.Allocation {
	entry := registry.Allocation{
		"project":          a.Project,
		"worktree":         a.Worktree,
		"worktree_name":    a.WorktreeName,
		"branch":           a.Branch,
		"port":             a.Port,
		"ports":            intsToAny(a.Ports),
		"database":         a.Database,
		"database_adapter": a.DatabaseAdapter,
	}

	for i, p := range a.Ports {
		entry[fmt.Sprintf("port_%d", i+1)] = p
	}

	if a.RedisDB > 0 {
		entry["redis_db"] = a.RedisDB
		entry["redis_prefix"] = nil
	} else {
		entry["redis_db"] = nil
		entry["redis_prefix"] = a.RedisPrefix
	}

	return entry
}

func (a *Allocation) ToInterpolationMap() interpolation.Allocation {
	m := interpolation.Allocation{
		"port":          a.Port,
		"ports":         a.Ports,
		"database":      a.Database,
		"worktree_name": a.WorktreeName,
	}
	if a.RedisDB > 0 {
		m["redis_db"] = a.RedisDB
	}
	if a.RedisPrefix != "" {
		m["redis_prefix"] = a.RedisPrefix
	}
	for i, p := range a.Ports {
		m[fmt.Sprintf("port_%d", i+1)] = p
	}
	return m
}

func New(uc *config.UserConfig, pc *config.ProjectConfig, reg *registry.Registry) *Allocator {
	return &Allocator{UserConfig: uc, ProjectConfig: pc, Registry: reg}
}

// Allocate returns an allocation for the given worktree. If an existing
// allocation is found in the registry, it is reused (idempotent). Otherwise
// a new allocation is created. When mainWorktree is true, base resources
// (port_base, template DB, no redis prefix) are returned directly.
// Branch is optional — when provided, enables branch-specific port reservations.
func (al *Allocator) Allocate(worktreePath, worktreeName string, mainWorktree bool, branch ...string) (*Allocation, error) {
	if err := al.validatePortConfig(); err != nil {
		return nil, err
	}

	var b string
	if len(branch) > 0 {
		b = branch[0]
	}
	if existing := al.reuseExisting(worktreePath, worktreeName, mainWorktree, b); existing != nil {
		return existing, nil
	}
	if mainWorktree {
		return al.allocateMain(worktreePath, worktreeName, b)
	}
	return al.allocateNew(worktreePath, worktreeName, b)
}

func (al *Allocator) validatePortConfig() error {
	base := al.UserConfig.PortBase()
	routerPort := al.UserConfig.RouterPort()

	if base == routerPort {
		return fmt.Errorf(
			"port.base (%d) conflicts with router.port (%d)\n\n"+
				"  The router needs its own port to proxy traffic. Set a different base:\n"+
				"    gtl config set port.base %d",
			base, routerPort, routerPort+1)
	}
	return nil
}

func (al *Allocator) reuseExisting(worktreePath, worktreeName string, mainWorktree bool, branch string) *Allocation {
	entry := al.Registry.Find(worktreePath)
	if entry == nil {
		return nil
	}

	ports := registry.ExtractPorts(entry)
	if len(ports) == 0 {
		return nil
	}

	alloc := &Allocation{
		Project:         registry.GetString(entry, "project"),
		Worktree:        worktreePath,
		WorktreeName:    worktreeName,
		Branch:          registry.GetString(entry, "branch"),
		Port:            ports[0],
		Ports:           ports,
		Database:        registry.GetString(entry, "database"),
		DatabaseAdapter: registry.GetString(entry, "database_adapter"),
		Reused:          true,
	}

	if prefix := registry.GetString(entry, "redis_prefix"); prefix != "" {
		alloc.RedisPrefix = prefix
	}
	if db := getFloat(entry, "redis_db"); db > 0 {
		alloc.RedisDB = int(db)
	}

	if len(alloc.Ports) != al.ProjectConfig.PortsNeeded() {
		return nil
	}

	project := al.ProjectConfig.Project()
	if mainWorktree {
		if base, ok := al.resolveReservation(project, branch); ok && base != ports[0] {
			return nil
		}
	} else if branch != "" {
		if base, ok := al.resolveBranchReservation(project, branch); ok && base != ports[0] {
			return nil
		}
	}

	reserved := al.UserConfig.ReservedPorts()
	if reserved[ports[0]] {
		isOwnReservation := false
		if mainWorktree {
			if base, ok := al.resolveReservation(project, branch); ok && base == ports[0] {
				isOwnReservation = true
			}
		} else if branch != "" {
			if base, ok := al.resolveBranchReservation(project, branch); ok && base == ports[0] {
				isOwnReservation = true
			}
		}
		if !isOwnReservation {
			return nil
		}
	}

	// A previously-allocated port that is busy is only a conflict when someone
	// *other* than this worktree holds it. When the worktree's own supervisor is
	// live, a busy port is the expected steady state (its dev server is running)
	// — reusing the same ports keeps routes pointing at the right place instead
	// of rewriting the env file out from under a running server.
	ownSupervisorChecked := false
	ownSupervisorLive := false
	for _, p := range ports {
		if IsPortFree(p) {
			continue
		}
		if !ownSupervisorChecked {
			ownSupervisorLive = al.hasLiveSupervisor(worktreePath)
			ownSupervisorChecked = true
		}
		if !ownSupervisorLive {
			return nil
		}
	}

	return alloc
}

func (al *Allocator) allocateMain(worktreePath, worktreeName, branch string) (*Allocation, error) {
	count := al.ProjectConfig.PortsNeeded()
	if err := al.validatePortCount(count); err != nil {
		return nil, err
	}

	project := al.ProjectConfig.Project()
	var ports []int
	if base, ok := al.resolveReservation(project, branch); ok {
		if err := al.validateReservedPorts(base, count, al.Registry.UsedPorts()); err != nil {
			return nil, err
		}
		ports = make([]int, count)
		for i := range count {
			ports[i] = base + i
		}
	} else {
		ports = al.nextAvailablePortsFrom(al.UserConfig.PortBase(), count)
	}
	if ports == nil {
		return nil, fmt.Errorf("no available port block of size %d found (all ports in use or reserved)", count)
	}

	return &Allocation{
		Project:         project,
		Worktree:        worktreePath,
		WorktreeName:    worktreeName,
		Port:            ports[0],
		Ports:           ports,
		Database:        al.ProjectConfig.DatabaseTemplate(),
		DatabaseAdapter: al.ProjectConfig.DatabaseAdapter(),
	}, nil
}

func (al *Allocator) allocateNew(worktreePath, worktreeName, branch string) (*Allocation, error) {
	count := al.ProjectConfig.PortsNeeded()
	if err := al.validatePortCount(count); err != nil {
		return nil, err
	}

	project := al.ProjectConfig.Project()
	database := al.buildDatabaseName(worktreeName)
	adapter := al.ProjectConfig.DatabaseAdapter()

	// build chooses ports and Redis isolation against a snapshot of already-used
	// resources. It is run inside the registry lock for a real allocation — so
	// the read of used resources, the choice, and the write are one atomic
	// transaction and two concurrent runs can't pick the same port block or
	// Redis DB — and against the current snapshot for a dry-run preview, which
	// never persists.
	build := func(used registry.UsedResources) (*Allocation, error) {
		var ports []int
		if branch != "" {
			if base, ok := al.resolveBranchReservation(project, branch); ok {
				if verr := al.validateReservedPorts(base, count, used.Ports); verr != nil {
					return nil, verr
				}
				ports = make([]int, count)
				for i := range count {
					ports[i] = base + i
				}
			}
		}
		if ports == nil {
			ports = al.nextAvailablePortsFromUsed(al.UserConfig.PortBase()+al.UserConfig.PortIncrement(), count, used.Ports)
		}
		if ports == nil {
			return nil, fmt.Errorf("no available port block of size %d found (all ports in use or reserved)", count)
		}
		redisDB, redisPrefix, rerr := al.allocateRedisFrom(worktreeName, used.RedisDbs)
		if rerr != nil {
			return nil, rerr
		}
		return &Allocation{
			Project:         project,
			Worktree:        worktreePath,
			WorktreeName:    worktreeName,
			Port:            ports[0],
			Ports:           ports,
			Branch:          branch,
			Database:        database,
			DatabaseAdapter: adapter,
			RedisDB:         redisDB,
			RedisPrefix:     redisPrefix,
		}, nil
	}

	if al.DryRun {
		return build(registry.UsedResources{
			Ports:    al.Registry.UsedPorts(),
			RedisDbs: al.Registry.UsedRedisDbs(),
		})
	}

	var alloc *Allocation
	if _, err := al.Registry.AllocateTx(worktreePath, func(used registry.UsedResources) (registry.Allocation, error) {
		a, berr := build(used)
		if berr != nil {
			return nil, berr
		}
		alloc = a
		return a.ToRegistryEntry(), nil
	}); err != nil {
		return nil, err
	}
	return alloc, nil
}

// validatePortCount rejects non-positive port counts (which would otherwise
// produce an empty port block and panic on ports[0]) and counts that exceed the
// increment (blocks would overlap the next worktree's range).
func (al *Allocator) validatePortCount(count int) error {
	if count <= 0 {
		return fmt.Errorf("port_count must be at least 1, got %d; set a positive port_count in %s",
			count, config.ProjectConfigFile)
	}
	if count > al.UserConfig.PortIncrement() {
		return fmt.Errorf("port_count (%d) exceeds port.increment (%d); increase port.increment in your config.json to at least %d",
			count, al.UserConfig.PortIncrement(), count)
	}
	return nil
}

// validateReservedPorts subjects a reserved port block to the same hard
// exclusions the scan path enforces: it must not collide with the router port,
// a browser-blocked (WHATWG bad) port, or a port already allocated to another
// worktree. A reservation tripping any of these is a real misconfiguration, so
// fail loud rather than hand out a port that can't work.
func (al *Allocator) validateReservedPorts(base, count int, usedPorts []int) error {
	usedSet := make(map[int]bool, len(usedPorts))
	for _, p := range usedPorts {
		usedSet[p] = true
	}
	routerPort := al.UserConfig.RouterPort()
	for i := range count {
		port := base + i
		switch {
		case port == routerPort:
			return fmt.Errorf("reserved port %d conflicts with router.port (%d); choose a different reservation base", port, routerPort)
		case browserBlockedPorts[port]:
			return fmt.Errorf("reserved port %d is browser-blocked (WHATWG bad port) and browsers will refuse it; choose a different reservation base", port)
		case usedSet[port]:
			return fmt.Errorf("reserved port %d is already allocated to another worktree; choose a different reservation base", port)
		}
	}
	return nil
}

// resolveReservation checks for a port reservation for a main repo.
// Tries project/branch first (e.g. "salt/staging"), then project-only ("salt").
func (al *Allocator) resolveReservation(project, branch string) (int, bool) {
	reservations := al.UserConfig.PortReservations()
	if len(reservations) == 0 {
		return 0, false
	}
	if branch != "" {
		if port, ok := reservations[project+"/"+branch]; ok {
			return port, true
		}
	}
	if port, ok := reservations[project]; ok {
		return port, true
	}
	return 0, false
}

// resolveBranchReservation checks for a branch-specific reservation only
// (e.g. "salt/staging"). Project-only keys don't match worktrees.
func (al *Allocator) resolveBranchReservation(project, branch string) (int, bool) {
	reservations := al.UserConfig.PortReservations()
	if branch == "" || len(reservations) == 0 {
		return 0, false
	}
	port, ok := reservations[project+"/"+branch]
	return port, ok
}

func (al *Allocator) BuildRedisURL(alloc *Allocation) string {
	m := alloc.ToInterpolationMap()
	return interpolation.BuildRedisURL(al.UserConfig.RedisURL(), m)
}

// CommonDevPorts are well-known framework default ports that should be kept
// free for the proxy to claim. Third-party services (OAuth, Mapbox, Stripe
// webhooks) are typically whitelisted for these origins — if treeline allocates
// one, the proxy can't sit on it and the origin-preservation story breaks.
var CommonDevPorts = map[int]bool{
	3000: true, // Rails, Node/Express, Create React App, Vite fallback
	4000: true, // Ember, some Express setups
	4200: true, // Angular CLI
	5000: true, // Flask, .NET
	5173: true, // Vite default
	5174: true, // Vite secondary
	8000: true, // Django, PHP built-in server
	8080: true, // Tomcat, generic HTTP alternative
	8888: true, // Jupyter
}

// IsCommonDevPort reports whether the port is a well-known framework default
// that should be kept free for the proxy.
func IsCommonDevPort(port int) bool {
	return CommonDevPorts[port]
}

// browserBlockedPorts is the WHATWG fetch spec "bad port" set. Browsers
// silently refuse connections to these ports with no useful error message.
var browserBlockedPorts = map[int]bool{
	1: true, 7: true, 9: true, 11: true, 13: true, 15: true,
	17: true, 19: true, 20: true, 21: true, 22: true, 23: true,
	25: true, 37: true, 42: true, 43: true, 53: true, 69: true,
	77: true, 79: true, 87: true, 95: true, 101: true, 102: true,
	103: true, 104: true, 109: true, 110: true, 111: true, 113: true,
	115: true, 117: true, 119: true, 123: true, 135: true, 137: true,
	139: true, 143: true, 161: true, 179: true, 389: true, 427: true,
	465: true, 512: true, 513: true, 514: true, 515: true, 526: true,
	530: true, 531: true, 532: true, 540: true, 548: true, 554: true,
	556: true, 563: true, 587: true, 601: true, 636: true, 989: true,
	990: true, 993: true, 995: true, 1719: true, 1720: true, 1723: true,
	2049: true, 3659: true, 4045: true, 4190: true, 5060: true, 5061: true,
	6000: true, 6566: true, 6665: true, 6666: true, 6667: true, 6668: true,
	6669: true, 6679: true, 6697: true, 10080: true,
}

func (al *Allocator) nextAvailablePortsFrom(start, count int) []int {
	return al.nextAvailablePortsFromUsed(start, count, al.Registry.UsedPorts())
}

// nextAvailablePortsFromUsed scans for a free contiguous port block starting at
// start, treating usedPorts as already claimed. It keeps the live IsPortFree
// bind-verify as belt-and-suspenders even when called inside the registry lock:
// the lock guarantees no other gtl run races us, but a foreign process can still
// hold a port, so a bound candidate advances to the next block.
func (al *Allocator) nextAvailablePortsFromUsed(start, count int, usedPorts []int) []int {
	usedSet := make(map[int]bool)
	for _, p := range usedPorts {
		usedSet[p] = true
	}
	reserved := al.UserConfig.ReservedPorts()
	routerPort := al.UserConfig.RouterPort()

	candidate := start
	maxPort := 65535
	for candidate+count-1 <= maxPort {
		block := make([]int, count)
		conflict := false
		for i := range count {
			port := candidate + i
			block[i] = port
			if usedSet[port] || reserved[port] || port == routerPort || browserBlockedPorts[port] || CommonDevPorts[port] || !IsPortFree(port) {
				conflict = true
			}
		}
		if !conflict {
			return block
		}
		candidate += al.UserConfig.PortIncrement()
	}
	return nil
}

// IsPortFree attempts a TCP listen to verify nothing is bound on the port.
func IsPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// CheckPortsListening returns true if at least one of the given ports has
// an active TCP listener.
func CheckPortsListening(ports []int) bool {
	for _, port := range ports {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 200*1e6)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// allocateRedisFrom picks Redis isolation against a caller-supplied set of
// already-used database indices. The transactional allocateNew path passes the
// in-lock snapshot so DB selection cannot silently collide with a concurrent run.
func (al *Allocator) allocateRedisFrom(worktreeName string, usedDbs []int) (int, string, error) {
	if al.UserConfig.RedisStrategy() == "database" {
		db, err := al.nextAvailableRedisDBFrom(usedDbs)
		if err != nil {
			return 0, "", err
		}
		return db, "", nil
	}
	return 0, fmt.Sprintf("%s:%s", al.ProjectConfig.Project(), worktreeName), nil
}

// nextAvailableRedisDBFrom returns the lowest free Redis database index in the
// range 1..capacity-1 (db0 is reserved for the main/template worktree), treating
// usedDbs as taken. It fails loud when every slot is in use rather than silently
// colliding onto a shared database — capacity exhaustion is a real error, not a
// fallback. Uniqueness is guaranteed only when usedDbs is a fresh in-lock
// snapshot: a logical DB number can't be bind-tested, so the lock is the guarantee.
func (al *Allocator) nextAvailableRedisDBFrom(usedDbs []int) (int, error) {
	usedSet := make(map[int]bool)
	for _, db := range usedDbs {
		usedSet[db] = true
	}
	capacity := al.UserConfig.RedisDatabases()
	for db := 1; db < capacity; db++ {
		if !usedSet[db] {
			return db, nil
		}
	}
	return 0, al.redisExhaustedError(capacity, len(usedSet))
}

// redisExhaustedError explains that the "database" strategy has run out of
// Redis slots and points at the two durable fixes: grow the Redis database
// count, or switch to key-prefix isolation (which has no such ceiling).
func (al *Allocator) redisExhaustedError(capacity, used int) error {
	return fmt.Errorf(
		"no free Redis database: all %d slot(s) on %s are in use (%d worktree(s) allocated)\n\n"+
			"  The \"database\" strategy isolates each worktree onto its own Redis DB, but\n"+
			"  this Redis only has %d databases (indices 0–%d; db0 is reserved). Options:\n\n"+
			"    • Grow the pool — raise `databases` in redis.conf, restart Redis, then:\n"+
			"        gtl config set redis.databases <N>\n"+
			"        gtl reallocate --all-registry --apply\n\n"+
			"    • Switch to key-prefix isolation (no database ceiling):\n"+
			"        gtl config set redis.strategy prefixed\n"+
			"        gtl reallocate --all-registry --apply",
		capacity-1, al.UserConfig.RedisURL(), used, capacity, capacity-1)
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)
var collapseRe = regexp.MustCompile(`_+`)

// sanitizeIdentifier converts an arbitrary string into a database/redis-safe
// identifier: only [a-zA-Z0-9_], no leading/trailing underscores, no runs.
func sanitizeIdentifier(s string) string {
	s = sanitizeRe.ReplaceAllString(s, "_")
	s = collapseRe.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

func (al *Allocator) buildDatabaseName(worktreeName string) string {
	template := al.ProjectConfig.DatabaseTemplate()
	if template == "" {
		return ""
	}

	name := strings.NewReplacer(
		"{template}", template,
		"{worktree}", sanitizeIdentifier(worktreeName),
		"{project}", al.ProjectConfig.Project(),
	).Replace(al.ProjectConfig.DatabasePattern())

	return sanitizeIdentifier(name)
}

func intsToAny(ints []int) []any {
	result := make([]any, len(ints))
	for i, v := range ints {
		result[i] = v
	}
	return result
}

func getFloat(entry registry.Allocation, key string) float64 {
	if v, ok := entry[key].(float64); ok {
		return v
	}
	return 0
}
