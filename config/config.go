/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package config

import (
	"encoding/json"
	"maps"
	"net/url"
	"sync"

	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
)

type RedisEndpoint struct {
	URL                string `json:"url"`                          // Redis connection URL (redis:// or rediss://)
	Password           string `json:"password,omitempty"`           // Redis password
	Username           string `json:"username,omitempty"`           // Redis username (Redis 6+ ACL)
	DB                 int    `json:"db,omitempty"`                 // Database number (default 0, ignored in cluster mode)
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"` // Skip TLS verification
	Name               string `json:"name,omitempty"`               // Friendly name for this endpoint

	// Cluster support
	ClusterMode        string `json:"clusterMode,omitempty"`        // "auto" (default), "standalone", or "cluster"
	MaxBackupSizeBytes int64  `json:"maxBackupSizeBytes,omitempty"` // Max total backup size for cache expiration (default 10MB)
}

const DefaultMaxBackupSizeBytes = 10 * 1024 * 1024 // 10MB

func (e *RedisEndpoint) GetMaxBackupSizeBytes() int64 {
	if e.MaxBackupSizeBytes > 0 {
		return e.MaxBackupSizeBytes
	}
	return DefaultMaxBackupSizeBytes
}

type Specification struct {
	// JSON array of Redis endpoints
	EndpointsJSON string `json:"endpointsJson" split_words:"true" required:"true"`
	Endpoints     []RedisEndpoint

	// Discovery intervals in seconds
	DiscoveryIntervalInstanceSeconds int `json:"discoveryIntervalInstanceSeconds" split_words:"true" default:"30"`
	DiscoveryIntervalDatabaseSeconds int `json:"discoveryIntervalDatabaseSeconds" split_words:"true" default:"60"`

	// Attribute exclusion patterns
	DiscoveryAttributesExcludesInstances []string `json:"discoveryAttributesExcludesInstances" split_words:"true"`
	DiscoveryAttributesExcludesDatabases []string `json:"discoveryAttributesExcludesDatabases" split_words:"true"`
}

var (
	Config Specification

	// clusterNodes maps the sanitized URL of each discovered cluster node to an endpoint carrying the
	// credentials and TLS settings of the configured endpoint the node was discovered through.
	clusterNodes   = map[string]clusterNode{}
	clusterNodesMu sync.RWMutex
)

type clusterNode struct {
	endpoint *RedisEndpoint
	seed     string // sanitized URL of the configured endpoint the node was discovered through
}

func ParseConfiguration() {
	err := envconfig.Process("steadybit_extension", &Config)
	if err != nil {
		log.Fatal().Err(err).Msgf("Failed to parse configuration from environment.")
	}
}

func ValidateConfiguration() {
	if Config.EndpointsJSON == "" {
		log.Fatal().Msg("STEADYBIT_EXTENSION_ENDPOINTS_JSON is required")
	}

	err := json.Unmarshal([]byte(Config.EndpointsJSON), &Config.Endpoints)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse STEADYBIT_EXTENSION_ENDPOINTS_JSON")
	}

	if len(Config.Endpoints) == 0 {
		log.Fatal().Msg("At least one Redis endpoint must be configured")
	}

	for i, endpoint := range Config.Endpoints {
		if endpoint.URL == "" {
			log.Fatal().Msgf("Endpoint %d: URL is required", i)
		}
		log.Info().
			Int("index", i).
			Str("url", SanitizeRedisURL(endpoint.URL)).
			Str("name", endpoint.Name).
			Int("db", endpoint.DB).
			Msg("Configured Redis endpoint")
	}
}

// SanitizeRedisURL strips embedded credentials (userinfo) from a Redis URL so it can be safely
// published as a target attribute or used as a metric label. Scheme, host, port and path
// (database) are preserved. The full credentials remain in the endpoint configuration and are
// used for the actual connection.
func SanitizeRedisURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		// Never return the raw URL here: it may contain credentials and is published/logged.
		return "[unparseable-url]"
	}
	parsed.User = nil
	return parsed.String()
}

// GetEndpointByURL resolves the configured endpoint for a (possibly credential-stripped) URL.
// Both sides are sanitized before comparison so a published, credential-free target URL still
// resolves to its endpoint configuration. URLs of discovered cluster nodes resolve to the endpoint
// registered via SetClusterNodes.
func GetEndpointByURL(rawURL string) *RedisEndpoint {
	target := SanitizeRedisURL(rawURL)
	for i := range Config.Endpoints {
		if SanitizeRedisURL(Config.Endpoints[i].URL) == target {
			return &Config.Endpoints[i]
		}
	}
	clusterNodesMu.RLock()
	defer clusterNodesMu.RUnlock()
	if node, ok := clusterNodes[target]; ok {
		return node.endpoint
	}
	return nil
}

// SetClusterNodes records nodeURLs as the current nodes of the cluster reached through endpoint, so
// actions on a node's target connect with the endpoint's credentials and TLS settings. Nodes
// previously registered for the same endpoint but no longer listed are dropped, so an address that
// left the cluster stops resolving to its credentials. Each node endpoint is pinned to standalone
// mode and db 0: actions keep operating on that single node, and cluster nodes only support db 0.
func SetClusterNodes(endpoint *RedisEndpoint, nodeURLs []string) {
	seed := SanitizeRedisURL(endpoint.URL)
	current := make(map[string]clusterNode, len(nodeURLs))
	for _, nodeURL := range nodeURLs {
		node := *endpoint
		node.URL = withUserInfoOf(nodeURL, endpoint.URL)
		node.DB = 0
		node.ClusterMode = "standalone"
		current[SanitizeRedisURL(nodeURL)] = clusterNode{endpoint: &node, seed: seed}
	}

	clusterNodesMu.Lock()
	defer clusterNodesMu.Unlock()
	for key, node := range clusterNodes {
		if _, ok := current[key]; !ok && node.seed == seed {
			delete(clusterNodes, key)
		}
	}
	maps.Copy(clusterNodes, current)
}

// withUserInfoOf returns rawURL carrying the credentials embedded in the URL of source, if any.
func withUserInfoOf(rawURL, source string) string {
	src, err := url.Parse(source)
	if err != nil || src.User == nil {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	parsed.User = src.User
	return parsed.String()
}
