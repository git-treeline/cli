// Package interpolation provides template variable substitution for
// environment files and configuration values. Supported tokens include
// {port}, {database}, {redis_url}, {redis_prefix}, {project},
// {router_url}, {router_host}, {tunnel_url}, {tunnel_host}, and numbered
// ports like {port_1}, {port_2}. Databases also answer to dotted tokens that
// name the .treeline.yml field behind them: {database.name} for the primary
// and {database.extra.<key>} for a named auxiliary database. {router_domain}
// is kept as a deprecated alias for {router_host}.
package interpolation

import (
	"fmt"
	"regexp"
	"strings"
)

type Allocation map[string]any

func BuildRedisURL(baseURL string, allocation Allocation) string {
	base := strings.TrimRight(baseURL, "/")
	if db := getFloat(allocation, "redis_db"); db > 0 {
		return fmt.Sprintf("%s/%d", base, int(db))
	}
	return base
}

func Interpolate(pattern string, allocation Allocation, redisURL, project string) string {
	tokens := buildTokenMap(allocation, redisURL, project)
	result := pattern
	for token, value := range tokens {
		result = strings.ReplaceAll(result, token, value)
	}
	return result
}

// ResolveFunc looks up a project's URL by name and optional explicit branch.
// Used by InterpolateWithResolver to expand {resolve:...} tokens.
type ResolveFunc func(project string, branch ...string) (string, error)

var resolveTokenRe = regexp.MustCompile(`\{resolve:([^}]+)\}`)

// InterpolateWithResolver extends Interpolate with support for {resolve:project}
// and {resolve:project/branch} tokens. If resolver is nil, resolve tokens are
// left unmodified. Returns an error if any resolve target is not found.
func InterpolateWithResolver(pattern string, allocation Allocation, redisURL, project string, resolver ResolveFunc) (string, error) {
	result := Interpolate(pattern, allocation, redisURL, project)

	if resolver == nil {
		return result, nil
	}

	var resolveErr error
	result = resolveTokenRe.ReplaceAllStringFunc(result, func(match string) string {
		if resolveErr != nil {
			return match
		}
		sub := resolveTokenRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		spec := sub[1]
		parts := strings.SplitN(spec, "/", 2)
		proj := parts[0]
		var branch []string
		if len(parts) > 1 {
			branch = []string{parts[1]}
		}
		url, err := resolver(proj, branch...)
		if err != nil {
			resolveErr = fmt.Errorf("resolving {resolve:%s}: %w", spec, err)
			return match
		}
		return url
	})

	return result, resolveErr
}

func buildTokenMap(allocation Allocation, redisURL, project string) map[string]string {
	tokens := map[string]string{
		"{port}": formatValue(allocation, "port"),
		// {database} and {database.name} are permanent aliases: the bare form
		// is the token treeline mints, the dotted form is a path to the
		// database.name field in .treeline.yml that mints it.
		"{database}":      getString(allocation, "database"),
		"{database.name}": getString(allocation, "database"),
		"{redis_url}":    redisURL,
		"{redis_prefix}": getString(allocation, "redis_prefix"),
		"{project}":      project,
		"{worktree}":     getString(allocation, "worktree_name"),
		"{router_url}":    getString(allocation, "router_url"),
		"{router_host}":   getString(allocation, "router_host"),
		"{router_domain}": getString(allocation, "router_domain"), // deprecated alias for {router_host}
		"{tunnel_url}":    getString(allocation, "tunnel_url"),
		"{tunnel_host}":   getString(allocation, "tunnel_host"),
	}

	// Named extras mint {database.extra.<key>} instead of positional tokens:
	// a map-form config never numbers its auxiliary databases, so numbering
	// them here would invent an ordering the config doesn't express.
	extras := extraDatabases(allocation)
	for key, name := range extras {
		tokens["{database.extra."+key+"}"] = name
	}

	switch dbs := databaseList(allocation, len(extras) > 0).(type) {
	case []any:
		for i, d := range dbs {
			if s, ok := d.(string); ok {
				tokens[fmt.Sprintf("{database_%d}", i+1)] = s
			}
		}
	case []string:
		for i, d := range dbs {
			tokens[fmt.Sprintf("{database_%d}", i+1)] = d
		}
	default:
		// Registry entries written before the databases list existed carry
		// only the legacy singular field; {database_1} must still resolve to
		// it, as the docs promise it is interchangeable with {database}.
		if db := getString(allocation, "database"); db != "" {
			tokens["{database_1}"] = db
		}
	}

	if ports, ok := allocation["ports"].([]any); ok {
		for i, p := range ports {
			key := fmt.Sprintf("{port_%d}", i+1)
			if f, ok := p.(float64); ok {
				tokens[key] = fmt.Sprintf("%d", int(f))
			}
		}
	}
	if ports, ok := allocation["ports"].([]int); ok {
		for i, p := range ports {
			key := fmt.Sprintf("{port_%d}", i+1)
			tokens[key] = fmt.Sprintf("%d", p)
		}
	}

	return tokens
}

// extraDatabases reads the named auxiliary databases an allocation carries,
// accepting both the in-process map[string]string and the map[string]any a
// registry entry decodes into. Returns nil for list-form allocations.
func extraDatabases(a Allocation) map[string]string {
	switch raw := a["database_extra"].(type) {
	case map[string]string:
		if len(raw) == 0 {
			return nil
		}
		return raw
	case map[string]any:
		out := make(map[string]string, len(raw))
		for k, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				out[k] = s
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// databaseList returns the slice the {database_N} tokens are minted from.
// With named extras present only the primary is numbered — {database_1} stays
// interchangeable with {database}, but the extras are addressed by name.
func databaseList(a Allocation, named bool) any {
	dbs := a["databases"]
	if !named {
		return dbs
	}
	switch list := dbs.(type) {
	case []any:
		if len(list) > 0 {
			return list[:1]
		}
	case []string:
		if len(list) > 0 {
			return list[:1]
		}
	}
	return dbs
}

func getString(a Allocation, key string) string {
	if v, ok := a[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(a Allocation, key string) float64 {
	if v, ok := a[key].(float64); ok {
		return v
	}
	if v, ok := a[key].(int); ok {
		return float64(v)
	}
	return 0
}

func formatValue(a Allocation, key string) string {
	v := a[key]
	switch val := v.(type) {
	case float64:
		return fmt.Sprintf("%d", int(val))
	case int:
		return fmt.Sprintf("%d", val)
	case string:
		return val
	}
	return ""
}
