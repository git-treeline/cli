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
)

const maxRedisDbs = 16

// Allocator manages resource allocation using user and project configuration.
// It tracks used resources via the registry to avoid conflicts between worktrees.
type Allocator struct {
	UserConfig    *config.UserConfig
	ProjectConfig *config.ProjectConfig
	Registry      *registry.Registry
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

	for _, p := range ports {
		if !IsPortFree(p) {
			return nil
		}
	}

	return alloc
}

func (al *Allocator) allocateMain(worktreePath, worktreeName, branch string) (*Allocation, error) {
	count := al.ProjectConfig.PortsNeeded()
	if count > al.UserConfig.PortIncrement() {
		return nil, fmt.Errorf("port_count (%d) exceeds port.increment (%d); increase port.increment in your config.json to at least %d",
			count, al.UserConfig.PortIncrement(), count)
	}

	project := al.ProjectConfig.Project()
	var ports []int
	if base, ok := al.resolveReservation(project, branch); ok {
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
	if count > al.UserConfig.PortIncrement() {
		return nil, fmt.Errorf("port_count (%d) exceeds port.increment (%d); increase port.increment in your config.json to at least %d",
			count, al.UserConfig.PortIncrement(), count)
	}

	project := al.ProjectConfig.Project()
	var ports []int
	if branch != "" {
		if base, ok := al.resolveBranchReservation(project, branch); ok {
			ports = make([]int, count)
			for i := range count {
				ports[i] = base + i
			}
		}
	}
	if ports == nil {
		ports = al.nextAvailablePortsFrom(al.UserConfig.PortBase()+al.UserConfig.PortIncrement(), count)
	}
	if ports == nil {
		return nil, fmt.Errorf("no available port block of size %d found (all ports in use or reserved)", count)
	}

	redisDB, redisPrefix := al.allocateRedis(worktreeName)
	database := al.buildDatabaseName(worktreeName)

	return &Allocation{
		Project:         project,
		Worktree:        worktreePath,
		WorktreeName:    worktreeName,
		Port:            ports[0],
		Ports:           ports,
		Database:        database,
		DatabaseAdapter: al.ProjectConfig.DatabaseAdapter(),
		RedisDB:         redisDB,
		RedisPrefix:     redisPrefix,
	}, nil
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
	usedSet := make(map[int]bool)
	for _, p := range al.Registry.UsedPorts() {
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

func (al *Allocator) allocateRedis(worktreeName string) (int, string) {
	if al.UserConfig.RedisStrategy() == "database" {
		db := al.nextAvailableRedisDB()
		return db, ""
	}
	return 0, fmt.Sprintf("%s:%s", al.ProjectConfig.Project(), worktreeName)
}

func (al *Allocator) nextAvailableRedisDB() int {
	usedSet := make(map[int]bool)
	for _, db := range al.Registry.UsedRedisDbs() {
		usedSet[db] = true
	}
	for db := 1; db < maxRedisDbs; db++ {
		if !usedSet[db] {
			return db
		}
	}
	return 1
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_]`)
var collapseRe = regexp.MustCompile(`_+`)

func (al *Allocator) buildDatabaseName(worktreeName string) string {
	template := al.ProjectConfig.DatabaseTemplate()
	if template == "" {
		return ""
	}

	sanitized := sanitizeRe.ReplaceAllString(worktreeName, "_")
	sanitized = collapseRe.ReplaceAllString(sanitized, "_")
	sanitized = strings.Trim(sanitized, "_")

	return strings.NewReplacer(
		"{template}", template,
		"{worktree}", sanitized,
		"{project}", al.ProjectConfig.Project(),
	).Replace(al.ProjectConfig.DatabasePattern())
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
