// Package config provides user and project configuration management.
// User config (config.json) defines global allocation policy like port
// ranges and Redis strategy. Project config (.treeline.yml) defines
// per-project settings like database templates and setup commands.
package config

// DeepMerge merges override into base. Override values win for present keys;
// nested maps are merged recursively; missing keys get defaults from base.
func DeepMerge(base, override map[string]any) map[string]any {
	result := make(map[string]any, len(base))

	for k, v := range override {
		result[k] = v
	}

	for k, baseVal := range base {
		overrideVal, exists := result[k]
		if !exists {
			result[k] = baseVal
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
