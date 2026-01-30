/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

type redisDatabaseDiscovery struct{}

var (
	_ discovery_kit_sdk.TargetDescriber    = (*redisDatabaseDiscovery)(nil)
	_ discovery_kit_sdk.AttributeDescriber = (*redisDatabaseDiscovery)(nil)
)

func NewRedisDatabaseDiscovery(ctx context.Context) discovery_kit_sdk.TargetDiscovery {
	discovery := &redisDatabaseDiscovery{}
	return discovery_kit_sdk.NewCachedTargetDiscovery(discovery,
		discovery_kit_sdk.WithRefreshTargetsNow(),
		discovery_kit_sdk.WithRefreshTargetsInterval(ctx, time.Duration(config.Config.DiscoveryIntervalDatabaseSeconds)*time.Second),
	)
}

func (d *redisDatabaseDiscovery) Describe() discovery_kit_api.DiscoveryDescription {
	return discovery_kit_api.DiscoveryDescription{
		Id: TargetTypeDatabase,
		Discover: discovery_kit_api.DescribingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr(fmt.Sprintf("%ds", config.Config.DiscoveryIntervalDatabaseSeconds)),
		},
	}
}

func (d *redisDatabaseDiscovery) DescribeTarget() discovery_kit_api.TargetDescription {
	return discovery_kit_api.TargetDescription{
		Id:       TargetTypeDatabase,
		Label:    discovery_kit_api.PluralLabel{One: "Redis database", Other: "Redis databases"},
		Category: extutil.Ptr("data store"),
		Version:  extbuild.GetSemverVersionStringOrUnknown(),
		Icon:     extutil.Ptr(redisIcon),
		Table: discovery_kit_api.Table{
			Columns: []discovery_kit_api.Column{
				{Attribute: AttrRedisHost},
				{Attribute: AttrDatabaseIndex},
				{Attribute: AttrDatabaseKeys},
			},
			OrderBy: []discovery_kit_api.OrderBy{
				{Attribute: AttrRedisHost, Direction: "ASC"},
				{Attribute: AttrDatabaseIndex, Direction: "ASC"},
			},
		},
	}
}

func (d *redisDatabaseDiscovery) DescribeAttributes() []discovery_kit_api.AttributeDescription {
	return []discovery_kit_api.AttributeDescription{
		{
			Attribute: AttrDatabaseIndex,
			Label:     discovery_kit_api.PluralLabel{One: "Database index", Other: "Database indices"},
		},
		{
			Attribute: AttrDatabaseKeys,
			Label:     discovery_kit_api.PluralLabel{One: "Key count", Other: "Key counts"},
		},
		{
			Attribute: AttrDatabaseName,
			Label:     discovery_kit_api.PluralLabel{One: "Database name", Other: "Database names"},
		},
	}
}

func (d *redisDatabaseDiscovery) DiscoverTargets(ctx context.Context) ([]discovery_kit_api.Target, error) {
	return FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		return discoverDatabases(ctx, endpoint)
	})
}

func discoverDatabases(ctx context.Context, endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
	client, err := clients.CreateRedisClient(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Ping to verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Get keyspace info to find databases with keys
	keyspaceInfo, err := clients.GetRedisInfo(ctx, client, "keyspace")
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get keyspace info")
		keyspaceInfo = make(map[string]string)
	}

	// Parse URL to get host and port
	parsedURL, err := url.Parse(endpoint.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %w", err)
	}

	host := parsedURL.Hostname()
	port := parsedURL.Port()
	if port == "" {
		port = "6379"
	}

	// Build target name
	instanceName := endpoint.Name
	if instanceName == "" {
		instanceName = fmt.Sprintf("%s:%s", host, port)
	}

	var targets []discovery_kit_api.Target

	// Parse keyspace info to find databases
	// Format: db0:keys=1,expires=0,avg_ttl=0
	dbPattern := regexp.MustCompile(`^db(\d+)$`)
	keysPattern := regexp.MustCompile(`keys=(\d+)`)

	for key, value := range keyspaceInfo {
		matches := dbPattern.FindStringSubmatch(key)
		if len(matches) != 2 {
			continue
		}

		dbIndex := matches[1]
		dbIndexInt, _ := strconv.Atoi(dbIndex)

		// Parse key count
		keyCount := "0"
		keysMatches := keysPattern.FindStringSubmatch(value)
		if len(keysMatches) == 2 {
			keyCount = keysMatches[1]
		}

		dbName := fmt.Sprintf("db%s", dbIndex)

		attributes := map[string][]string{
			AttrRedisURL:      {endpoint.URL},
			AttrRedisHost:     {host},
			AttrRedisPort:     {port},
			AttrRedisName:     {instanceName},
			AttrDatabaseIndex: {dbIndex},
			AttrDatabaseKeys:  {keyCount},
			AttrDatabaseName:  {dbName},
		}

		target := discovery_kit_api.Target{
			Id:         fmt.Sprintf("%s:%s/db%d", host, port, dbIndexInt),
			TargetType: TargetTypeDatabase,
			Label:      fmt.Sprintf("%s/%s", instanceName, dbName),
			Attributes: attributes,
		}

		targets = append(targets, target)
	}

	// If no databases found with keys, add db0 as default
	if len(targets) == 0 {
		attributes := map[string][]string{
			AttrRedisURL:      {endpoint.URL},
			AttrRedisHost:     {host},
			AttrRedisPort:     {port},
			AttrRedisName:     {instanceName},
			AttrDatabaseIndex: {"0"},
			AttrDatabaseKeys:  {"0"},
			AttrDatabaseName:  {"db0"},
		}

		target := discovery_kit_api.Target{
			Id:         fmt.Sprintf("%s:%s/db0", host, port),
			TargetType: TargetTypeDatabase,
			Label:      fmt.Sprintf("%s/db0", instanceName),
			Attributes: attributes,
		}

		targets = append(targets, target)
	}

	return targets, nil
}
