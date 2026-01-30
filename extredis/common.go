/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"fmt"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/extension-redis/config"
)

const (
	// Target types
	TargetTypeInstance = "com.steadybit.extension_redis.instance"
	TargetTypeDatabase = "com.steadybit.extension_redis.database"

	// Common attribute names
	AttrRedisURL         = "redis.url"
	AttrRedisHost        = "redis.host"
	AttrRedisPort        = "redis.port"
	AttrRedisVersion     = "redis.version"
	AttrRedisRole        = "redis.role"
	AttrRedisClusterMode = "redis.cluster.enabled"
	AttrRedisMemoryUsed  = "redis.memory.used_bytes"
	AttrRedisMemoryMax   = "redis.memory.max_bytes"
	AttrRedisClients     = "redis.connected_clients"
	AttrRedisUptime      = "redis.uptime_seconds"
	AttrRedisName        = "redis.name"

	AttrDatabaseIndex = "redis.database.index"
	AttrDatabaseKeys  = "redis.database.keys"
	AttrDatabaseName  = "redis.database.name"
)

var redisIcon = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyNCIgaGVpZ2h0PSIyNCIgdmlld0JveD0iMCAwIDI0IDI0IiBmaWxsPSJub25lIiBzdHJva2U9ImN1cnJlbnRDb2xvciIgc3Ryb2tlLXdpZHRoPSIyIiBzdHJva2UtbGluZWNhcD0icm91bmQiIHN0cm9rZS1saW5lam9pbj0icm91bmQiPjxwYXRoIGQ9Ik00IDE4VjZhMiAyIDAgMCAxIDItMmgxMmEyIDIgMCAwIDEgMiAydjEyYTIgMiAwIDAgMS0yIDJINmEyIDIgMCAwIDEtMi0yWiIvPjxwYXRoIGQ9Im04IDEyIDQtNCA0IDQiLz48cGF0aCBkPSJNMTIgMTZWOCIvPjwvc3ZnPg=="

// FetchTargetsPerEndpoint iterates through all configured endpoints and collects targets
func FetchTargetsPerEndpoint(handler func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error)) ([]discovery_kit_api.Target, error) {
	var allTargets []discovery_kit_api.Target

	for _, endpoint := range config.Config.Endpoints {
		targets, err := handler(&endpoint)
		if err != nil {
			// Log error but continue with other endpoints
			fmt.Printf("Error discovering targets for endpoint %s: %v\n", endpoint.URL, err)
			continue
		}
		allTargets = append(allTargets, targets...)
	}

	return allTargets, nil
}
