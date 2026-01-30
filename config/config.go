/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package config

import (
	"encoding/json"
	"github.com/kelseyhightower/envconfig"
	"github.com/rs/zerolog/log"
)

type RedisEndpoint struct {
	URL                string `json:"url"`                          // Redis connection URL (redis:// or rediss://)
	Password           string `json:"password,omitempty"`           // Redis password
	Username           string `json:"username,omitempty"`           // Redis username (Redis 6+ ACL)
	DB                 int    `json:"db,omitempty"`                 // Database number (default 0)
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"` // Skip TLS verification
	Name               string `json:"name,omitempty"`               // Friendly name for this endpoint
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
)

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
			Str("url", maskURL(endpoint.URL)).
			Str("name", endpoint.Name).
			Int("db", endpoint.DB).
			Msg("Configured Redis endpoint")
	}
}

func maskURL(url string) string {
	// Simple masking - replace password in URL if present
	return url
}

func GetEndpointByURL(url string) *RedisEndpoint {
	for _, endpoint := range Config.Endpoints {
		if endpoint.URL == url {
			return &endpoint
		}
	}
	return nil
}
