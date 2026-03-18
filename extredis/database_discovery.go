/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/discovery-kit/go/discovery_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
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

	instanceName := endpoint.Name
	if instanceName == "" {
		instanceName = fmt.Sprintf("%s:%s", host, port)
	}

	// Cluster mode: only db0 is supported
	serverInfo, err := clients.GetRedisInfo(ctx, client, "server")
	if err == nil && serverInfo["redis_mode"] == "cluster" {
		return []discovery_kit_api.Target{
			buildDatabaseTarget(host, port, instanceName, endpoint.URL, "0", "db0"),
		}, nil
	}

	// Standalone: discover databases from keyspace info
	keyspaceInfo, err := clients.GetRedisInfo(ctx, client, "keyspace")
	if err != nil {
		log.Warn().Err(err).Msg("Failed to get keyspace info")
		keyspaceInfo = make(map[string]string)
	}

	var targets []discovery_kit_api.Target
	dbPattern := regexp.MustCompile(`^db(\d+)$`)

	for key := range keyspaceInfo {
		matches := dbPattern.FindStringSubmatch(key)
		if len(matches) != 2 {
			continue
		}

		dbIndex := matches[1]
		dbIndexInt, _ := strconv.Atoi(dbIndex)
		dbName := fmt.Sprintf("db%s", dbIndex)

		target := buildDatabaseTarget(host, port, instanceName, endpoint.URL, dbIndex, dbName)
		target.Id = fmt.Sprintf("%s:%s/db%d", host, port, dbIndexInt)
		targets = append(targets, target)
	}

	if len(targets) == 0 {
		targets = append(targets, buildDatabaseTarget(host, port, instanceName, endpoint.URL, "0", "db0"))
	}

	return targets, nil
}

func buildDatabaseTarget(host, port, instanceName, redisURL, dbIndex, dbName string) discovery_kit_api.Target {
	return discovery_kit_api.Target{
		Id:         fmt.Sprintf("%s:%s/%s", host, port, dbName),
		TargetType: TargetTypeDatabase,
		Label:      fmt.Sprintf("%s/%s", instanceName, dbName),
		Attributes: map[string][]string{
			AttrRedisURL:      {redisURL},
			AttrRedisHost:     {host},
			AttrRedisPort:     {port},
			AttrRedisName:     {instanceName},
			AttrDatabaseIndex: {dbIndex},
			AttrDatabaseName:  {dbName},
		},
	}
}
