/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
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
		{
			Attribute: AttrRedisClusterNodeID,
			Label:     discovery_kit_api.PluralLabel{One: "Cluster node ID", Other: "Cluster node IDs"},
		},
	}
}

func (d *redisInstanceDiscovery) DiscoverTargets(ctx context.Context) ([]discovery_kit_api.Target, error) {
	return FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		return discoverInstance(ctx, endpoint)
	})
}

func discoverInstance(ctx context.Context, endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
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

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	allInfo, err := clients.GetRedisInfo(ctx, client, "")
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get Redis info")
		allInfo = make(map[string]string)
	}

	// Cluster mode: discover ALL cluster nodes
	if clusterEnabled, ok := allInfo["cluster_enabled"]; ok && clusterEnabled == "1" {
		return discoverClusterNodes(ctx, endpoint, client)
	}

	// Standalone: return the single configured instance
	return []discovery_kit_api.Target{buildInstanceTarget(endpoint, host, port, allInfo, "")}, nil
}

func discoverClusterNodes(ctx context.Context, endpoint *config.RedisEndpoint, seedClient *redis.Client) ([]discovery_kit_api.Target, error) {
	nodes, err := clients.ParseClusterNodes(ctx, seedClient)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get cluster nodes, falling back to single instance")
		return nil, fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	var targets []discovery_kit_api.Target
	for _, node := range nodes {
		nodeClient, err := clients.CreateDirectClient(endpoint, node.Addr)
		if err != nil {
			log.Warn().Err(err).Str("addr", node.Addr).Msg("Failed to create client for cluster node")
			continue
		}

		nodeInfo, err := clients.GetRedisInfo(ctx, nodeClient, "")
		nodeClient.Close()
		if err != nil {
			log.Warn().Err(err).Str("addr", node.Addr).Msg("Failed to get info for cluster node")
			nodeInfo = make(map[string]string)
		}

		// Parse host:port from the node address
		nodeHost, nodePort := parseHostPort(node.Addr)

		target := buildInstanceTarget(endpoint, nodeHost, nodePort, nodeInfo, node.ID)
		// Override the URL to point to this specific node
		scheme := "redis"
		if endpoint.InsecureSkipVerify || len(endpoint.URL) > 8 && endpoint.URL[:8] == "rediss://" {
			scheme = "rediss"
		}
		if strings.HasPrefix(endpoint.URL, "rediss://") {
			scheme = "rediss"
		}
		target.Attributes[AttrRedisURL] = []string{fmt.Sprintf("%s://%s:%s", scheme, nodeHost, nodePort)}

		targets = append(targets, target)
	}

	return targets, nil
}

func buildInstanceTarget(endpoint *config.RedisEndpoint, host, port string, info map[string]string, clusterNodeID string) discovery_kit_api.Target {
	name := endpoint.Name
	if name == "" {
		name = fmt.Sprintf("%s:%s", host, port)
	} else if clusterNodeID != "" {
		name = fmt.Sprintf("%s/%s:%s", endpoint.Name, host, port)
	}

	attributes := map[string][]string{
		AttrRedisURL:  {endpoint.URL},
		AttrRedisHost: {host},
		AttrRedisPort: {port},
		AttrRedisName: {name},
	}

	if version, ok := info["redis_version"]; ok {
		attributes[AttrRedisVersion] = []string{version}
	}
	if memMax, ok := info["maxmemory"]; ok {
		attributes[AttrRedisMemoryMax] = []string{memMax}
	}
	if role, ok := info["role"]; ok {
		attributes[AttrRedisRole] = []string{role}
	}
	if clusterEnabled, ok := info["cluster_enabled"]; ok {
		attributes[AttrRedisClusterMode] = []string{clusterEnabled}
	}
	if clusterNodeID != "" {
		attributes[AttrRedisClusterNodeID] = []string{clusterNodeID}
	}

	return discovery_kit_api.Target{
		Id:         fmt.Sprintf("%s:%s", host, port),
		TargetType: TargetTypeInstance,
		Label:      name,
		Attributes: attributes,
	}
}

func parseHostPort(addr string) (string, string) {
	idx := strings.LastIndex(addr, ":")
	if idx == -1 {
		return addr, "6379"
	}
	return addr[:idx], addr[idx+1:]
}
