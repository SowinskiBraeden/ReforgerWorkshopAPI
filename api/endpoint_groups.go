package api

import "strings"

// EndpointGroupForCacheKey buckets a cache resource key into the same
// product-level categories used for request events.
func EndpointGroupForCacheKey(key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	switch {
	case strings.Contains(key, ":mod:"):
		return "mod_detail"
	case strings.Contains(key, ":mods:") || strings.Contains(key, "/mods"):
		if strings.Contains(key, "search=") || strings.Contains(key, "?search=") || searchStyleModsKey(key) {
			return "search"
		}
		return "mod_list"
	case strings.Contains(key, "refresh/jobs"):
		return "refresh_job"
	case strings.Contains(key, "health"):
		return "health"
	default:
		return "other"
	}
}

// searchStyleModsKey matches keys like "v1:mods:1:radio:popularity:" where the
// fourth segment carries a search term.
func searchStyleModsKey(key string) bool {
	parts := strings.Split(key, ":")
	return len(parts) >= 5 && parts[1] == "mods" && strings.TrimSpace(parts[3]) != ""
}
