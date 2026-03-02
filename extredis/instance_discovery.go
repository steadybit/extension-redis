/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
)

type redisInstanceDiscovery struct{}

var (
	_ discovery_kit_sdk.TargetDescriber    = (*redisInstanceDiscovery)(nil)
	_ discovery_kit_sdk.AttributeDescriber = (*redisInstanceDiscovery)(nil)
)

func NewRedisInstanceDiscovery(ctx context.Context) discovery_kit_sdk.TargetDiscovery {
	discovery := &redisInstanceDiscovery{}
	return discovery_kit_sdk.NewCachedTargetDiscovery(discovery,
		discovery_kit_sdk.WithRefreshTargetsNow(),
		discovery_kit_sdk.WithRefreshTargetsInterval(ctx, time.Duration(config.Config.DiscoveryIntervalInstanceSeconds)*time.Second),
	)
}

func (d *redisInstanceDiscovery) Describe() discovery_kit_api.DiscoveryDescription {
	return discovery_kit_api.DiscoveryDescription{
		Id: TargetTypeInstance,
		Discover: discovery_kit_api.DescribingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr(fmt.Sprintf("%ds", config.Config.DiscoveryIntervalInstanceSeconds)),
		},
	}
}

func (d *redisInstanceDiscovery) DescribeTarget() discovery_kit_api.TargetDescription {
	return discovery_kit_api.TargetDescription{
		Id:       TargetTypeInstance,
		Label:    discovery_kit_api.PluralLabel{One: "Redis instance", Other: "Redis instances"},
		Category: extutil.Ptr("data store"),
		Version:  extbuild.GetSemverVersionStringOrUnknown(),
		Icon:     extutil.Ptr(redisIcon),
		Table: discovery_kit_api.Table{
			Columns: []discovery_kit_api.Column{
				{Attribute: AttrRedisName},
				{Attribute: AttrRedisHost},
				{Attribute: AttrRedisPort},
				{Attribute: AttrRedisRole},
			},
			OrderBy: []discovery_kit_api.OrderBy{
				{Attribute: AttrRedisHost, Direction: "ASC"},
			},
		},
	}
}

func (d *redisInstanceDiscovery) DescribeAttributes() []discovery_kit_api.AttributeDescription {
	return []discovery_kit_api.AttributeDescription{
		{
			Attribute: AttrRedisURL,
			Label:     discovery_kit_api.PluralLabel{One: "Redis URL", Other: "Redis URLs"},
		},
		{
			Attribute: AttrRedisHost,
			Label:     discovery_kit_api.PluralLabel{One: "Redis host", Other: "Redis hosts"},
		},
		{
			Attribute: AttrRedisPort,
			Label:     discovery_kit_api.PluralLabel{One: "Redis port", Other: "Redis ports"},
		},
		{
			Attribute: AttrRedisVersion,
			Label:     discovery_kit_api.PluralLabel{One: "Redis version", Other: "Redis versions"},
		},
		{
			Attribute: AttrRedisRole,
			Label:     discovery_kit_api.PluralLabel{One: "Redis role", Other: "Redis roles"},
		},
		{
			Attribute: AttrRedisClusterMode,
			Label:     discovery_kit_api.PluralLabel{One: "Cluster mode enabled", Other: "Cluster mode enabled"},
		},
		{
			Attribute: AttrRedisMemoryMax,
			Label:     discovery_kit_api.PluralLabel{One: "Max memory (bytes)", Other: "Max memory (bytes)"},
		},
		{
			Attribute: AttrRedisName,
			Label:     discovery_kit_api.PluralLabel{One: "Instance name", Other: "Instance names"},
		},
	}
}

func (d *redisInstanceDiscovery) DiscoverTargets(ctx context.Context) ([]discovery_kit_api.Target, error) {
	return FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		return discoverInstance(ctx, endpoint)
	})
}

func discoverInstance(ctx context.Context, endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
	// Parse URL first to fail fast on invalid URLs
	parsedURL, err := url.Parse(endpoint.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	host := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		port = "6379"
	}

	client, err := clients.CreateRedisClient(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Ping to verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Get all info in a single call
	allInfo, err := clients.GetRedisInfo(ctx, client, "")
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get Redis info")
		allInfo = make(map[string]string)
	}

	// Build target name
	name := endpoint.Name
	if name == "" {
		name = fmt.Sprintf("%s:%s", host, port)
	}

	attributes := map[string][]string{
		AttrRedisURL:  {endpoint.URL},
		AttrRedisHost: {host},
		AttrRedisPort: {port},
		AttrRedisName: {name},
	}

	// Add info attributes
	if version, ok := allInfo["redis_version"]; ok {
		attributes[AttrRedisVersion] = []string{version}
	}

	if memMax, ok := allInfo["maxmemory"]; ok {
		attributes[AttrRedisMemoryMax] = []string{memMax}
	}

	if role, ok := allInfo["role"]; ok {
		attributes[AttrRedisRole] = []string{role}
	}

	if clusterEnabled, ok := allInfo["cluster_enabled"]; ok {
		attributes[AttrRedisClusterMode] = []string{clusterEnabled}
	}

	target := discovery_kit_api.Target{
		Id:         fmt.Sprintf("%s:%s", host, port),
		TargetType: TargetTypeInstance,
		Label:      name,
		Attributes: attributes,
	}

	return []discovery_kit_api.Target{target}, nil
}
