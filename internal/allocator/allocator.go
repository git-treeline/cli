package allocator

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"github.com/git-treeline/git-treeline/internal/config"
	"github.com/git-treeline/git-treeline/internal/interpolation"
	"github.com/git-treeline/git-treeline/internal/registry"
)

const maxRedisDbs = 16

type Allocator struct {
	UserConfig    *config.UserConfig
	ProjectConfig *config.ProjectConfig
	Registry      *registry.Registry
}

type Allocation struct {
	Project      string
	Worktree     string
	WorktreeName string
	Port         int
	Ports        []int
	Database     string
	RedisDB      int
	RedisPrefix  string
	Reused       bool
}

func (a *Allocation) ToRegistryEntry() registry.Allocation {
	entry := registry.Allocation{
		"project":       a.Project,
		"worktree":      a.Worktree,
		"worktree_name": a.WorktreeName,
		"port":          a.Port,
		"ports":         intsToAny(a.Ports),
		"database":      a.Database,
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
func (al *Allocator) Allocate(worktreePath, worktreeName string, mainWorktree bool) (*Allocation, error) {
	if existing := al.reuseExisting(worktreePath, worktreeName); existing != nil {
		return existing, nil
	}
	if mainWorktree {
		return al.allocateMain(worktreePath, worktreeName)
	}
	return al.allocateNew(worktreePath, worktreeName)
}

func (al *Allocator) reuseExisting(worktreePath, worktreeName string) *Allocation {
	entry := al.Registry.Find(worktreePath)
	if entry == nil {
		return nil
	}

	ports := extractPorts(entry)
	if len(ports) == 0 {
		return nil
	}

	alloc := &Allocation{
		Project:      getString(entry, "project"),
		Worktree:     worktreePath,
		WorktreeName: worktreeName,
		Port:         ports[0],
		Ports:        ports,
		Database:     getString(entry, "database"),
		Reused:       true,
	}

	if prefix := getString(entry, "redis_prefix"); prefix != "" {
		alloc.RedisPrefix = prefix
	}
	if db := getFloat(entry, "redis_db"); db > 0 {
		alloc.RedisDB = int(db)
	}

	return alloc
}

func (al *Allocator) allocateMain(worktreePath, worktreeName string) (*Allocation, error) {
	port := al.UserConfig.PortBase()
	count := al.ProjectConfig.PortsNeeded()
	ports := make([]int, count)
	for i := range count {
		ports[i] = port + i
	}

	return &Allocation{
		Project:      al.ProjectConfig.Project(),
		Worktree:     worktreePath,
		WorktreeName: worktreeName,
		Port:         port,
		Ports:        ports,
		Database:     al.ProjectConfig.DatabaseTemplate(),
	}, nil
}

func (al *Allocator) allocateNew(worktreePath, worktreeName string) (*Allocation, error) {
	count := al.ProjectConfig.PortsNeeded()
	if count > al.UserConfig.PortIncrement() {
		return nil, fmt.Errorf("ports_needed (%d) exceeds port.increment (%d); increase port.increment in your config.json to at least %d",
			count, al.UserConfig.PortIncrement(), count)
	}

	ports := al.nextAvailablePorts(count)
	redisDB, redisPrefix := al.allocateRedis(worktreeName)
	database := al.buildDatabaseName(worktreeName)

	return &Allocation{
		Project:      al.ProjectConfig.Project(),
		Worktree:     worktreePath,
		WorktreeName: worktreeName,
		Port:         ports[0],
		Ports:        ports,
		Database:     database,
		RedisDB:      redisDB,
		RedisPrefix:  redisPrefix,
	}, nil
}

func (al *Allocator) BuildRedisURL(alloc *Allocation) string {
	m := alloc.ToInterpolationMap()
	return interpolation.BuildRedisURL(al.UserConfig.RedisURL(), m)
}

func (al *Allocator) nextAvailablePorts(count int) []int {
	usedSet := make(map[int]bool)
	for _, p := range al.Registry.UsedPorts() {
		usedSet[p] = true
	}

	candidate := al.UserConfig.PortBase() + al.UserConfig.PortIncrement()
	for {
		block := make([]int, count)
		conflict := false
		for i := range count {
			port := candidate + i
			block[i] = port
			if usedSet[port] || !IsPortFree(port) {
				conflict = true
			}
		}
		if !conflict {
			return block
		}
		candidate += al.UserConfig.PortIncrement()
	}
}

// IsPortFree attempts a TCP listen to verify nothing is bound on the port.
func IsPortFree(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
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

func extractPorts(entry registry.Allocation) []int {
	if ps, ok := entry["ports"].([]any); ok {
		result := make([]int, 0, len(ps))
		for _, p := range ps {
			if f, ok := p.(float64); ok {
				result = append(result, int(f))
			}
		}
		if len(result) > 0 {
			return result
		}
	}
	if p, ok := entry["port"].(float64); ok {
		return []int{int(p)}
	}
	return nil
}

func getString(entry registry.Allocation, key string) string {
	if v, ok := entry[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(entry registry.Allocation, key string) float64 {
	if v, ok := entry[key].(float64); ok {
		return v
	}
	return 0
}
