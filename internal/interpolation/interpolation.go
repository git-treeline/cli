// Package interpolation provides template variable substitution for
// environment files and configuration values. Supported tokens include
// {port}, {database}, {redis_url}, {redis_prefix}, {project}, and
// numbered ports like {port_1}, {port_2}.
package interpolation

import (
	"fmt"
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

func buildTokenMap(allocation Allocation, redisURL, project string) map[string]string {
	tokens := map[string]string{
		"{port}":         formatValue(allocation, "port"),
		"{database}":     getString(allocation, "database"),
		"{redis_url}":    redisURL,
		"{redis_prefix}": getString(allocation, "redis_prefix"),
		"{project}":      project,
		"{worktree}":     getString(allocation, "worktree_name"),
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
