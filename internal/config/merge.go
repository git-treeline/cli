// Package config provides user and project configuration management.
// User config (config.json) defines global allocation policy like port
// ranges and Redis strategy. Project config (.treeline.yml) defines
// per-project settings like database templates and setup commands.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// DeepMerge merges override into base. Override values win for present keys;
// nested maps are merged recursively; missing keys get defaults from base.
func DeepMerge(base, override map[string]any) map[string]any {
	result := make(map[string]any, len(base))

	for k, v := range override {
		result[k] = cloneValue(v)
	}

	for k, baseVal := range base {
		overrideVal, exists := result[k]
		if !exists {
			result[k] = cloneValue(baseVal)
			continue
		}

		baseMap, baseIsMap := baseVal.(map[string]any)
		overrideMap, overrideIsMap := overrideVal.(map[string]any)
		if baseIsMap && overrideIsMap {
			result[k] = DeepMerge(baseMap, overrideMap)
		}
	}

	return result
}

func cloneValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(val))
		for k, child := range val {
			cloned[k] = cloneValue(child)
		}
		return cloned
	case []any:
		cloned := make([]any, len(val))
		for i, child := range val {
			cloned[i] = cloneValue(child)
		}
		return cloned
	default:
		return v
	}
}

// Dig traverses nested maps by keys, returning nil if any step fails.
func Dig(m map[string]any, keys ...string) any {
	var current any = m
	for _, k := range keys {
		cm, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = cm[k]
	}
	return current
}

// WarnUnknownKeys checks data for top-level keys not present in knownKeys
// and prints warnings to stderr. configName identifies the file in messages
// (e.g. "config.json", ".treeline.yml").
func WarnUnknownKeys(data map[string]any, knownKeys map[string]bool, configName string) {
	var unknown []string
	for k := range data {
		if !knownKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return
	}
	sort.Strings(unknown)

	known := make([]string, 0, len(knownKeys))
	for k := range knownKeys {
		known = append(known, k)
	}
	sort.Strings(known)

	for _, k := range unknown {
		if best := closestMatch(k, known); best != "" {
			fmt.Fprintf(os.Stderr, "Warning: unknown key %q in %s (did you mean %q?)\n", k, configName, best)
		} else {
			fmt.Fprintf(os.Stderr, "Warning: unknown key %q in %s\n", k, configName)
		}
	}
}

func closestMatch(input string, candidates []string) string {
	best := ""
	bestDist := 3 // only suggest if edit distance <= 2
	for _, c := range candidates {
		d := levenshtein(strings.ToLower(input), strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
