// Package interpolation provides template variable substitution for
// environment files and configuration values. Supported tokens include
// {port}, {database}, {redis_url}, {redis_prefix}, {project},
// {router_url}, {router_host}, {tunnel_url}, {tunnel_host}, and numbered
// ports like {port_1}, {port_2}. {router_domain} is kept as a deprecated
// alias for {router_host}.
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
		"{port}":         formatValue(allocation, "port"),
		"{database}":     getString(allocation, "database"),
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
