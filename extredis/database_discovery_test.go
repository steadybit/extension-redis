// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/extension-redis/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDatabaseDiscovery_Describe(t *testing.T) {
	// Given
	discovery := &redisDatabaseDiscovery{}

	// When
	desc := discovery.Describe()

	// Then
	assert.Equal(t, TargetTypeDatabase, desc.Id)
	require.NotNil(t, desc.Discover.CallInterval)
}

func TestDatabaseDiscovery_DescribeTarget(t *testing.T) {
	// Given
	discovery := &redisDatabaseDiscovery{}

	// When
	td := discovery.DescribeTarget()

	// Then
	assert.Equal(t, TargetTypeDatabase, td.Id)
	assert.Equal(t, "Redis database", td.Label.One)
	assert.Equal(t, "Redis databases", td.Label.Other)
	require.NotNil(t, td.Category)
	assert.Equal(t, "data store", *td.Category)
	require.NotNil(t, td.Icon)

	// table columns
	require.GreaterOrEqual(t, len(td.Table.Columns), 2)

	// Verify expected columns exist
	columnAttrs := make([]string, len(td.Table.Columns))
	for i, c := range td.Table.Columns {
		columnAttrs[i] = c.Attribute
	}
	assert.Contains(t, columnAttrs, AttrRedisHost)
	assert.Contains(t, columnAttrs, AttrDatabaseIndex)

	// order by
	require.Len(t, td.Table.OrderBy, 2)
	assert.Equal(t, AttrRedisHost, td.Table.OrderBy[0].Attribute)
	assert.Equal(t, discovery_kit_api.OrderByDirection("ASC"), td.Table.OrderBy[0].Direction)
}

func TestDatabaseDiscovery_DescribeAttributes(t *testing.T) {
	// Given
	discovery := &redisDatabaseDiscovery{}

	// When
	attrs := discovery.DescribeAttributes()

	// Then
	require.NotEmpty(t, attrs)

	// Verify key attributes are present
	attrMap := make(map[string]string)
	for _, a := range attrs {
		attrMap[a.Attribute] = a.Label.One
	}

	assert.Contains(t, attrMap, AttrDatabaseIndex)
	assert.Contains(t, attrMap, AttrDatabaseName)
}

func TestDiscoverDatabases_EmptyDatabase(t *testing.T) {
	// Given - miniredis starts empty (no keys)
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL:  fmt.Sprintf("redis://%s", mr.Addr()),
		Name: "test-redis",
	}

	// When
	targets, err := discoverDatabases(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	// Should have at least db0 as default
	require.GreaterOrEqual(t, len(targets), 1)

	// Check the default db0 target
	db0Found := false
	for _, target := range targets {
		if target.Attributes[AttrDatabaseIndex][0] == "0" {
			db0Found = true
			assert.Equal(t, TargetTypeDatabase, target.TargetType)
			assert.Contains(t, target.Attributes, AttrRedisURL)
			assert.Contains(t, target.Attributes, AttrRedisHost)
			assert.Contains(t, target.Attributes, AttrDatabaseName)
		}
	}
	assert.True(t, db0Found, "db0 should be present")
}

func TestDiscoverDatabases_WithKeys(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Add some keys to db0
	mr.Set("key1", "value1")
	mr.Set("key2", "value2")

	endpoint := &config.RedisEndpoint{
		URL:  fmt.Sprintf("redis://%s", mr.Addr()),
		Name: "test-redis",
	}

	// When
	targets, err := discoverDatabases(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	// Find db0 target
	var db0Target *discovery_kit_api.Target
	for i := range targets {
		if targets[i].Attributes[AttrDatabaseIndex][0] == "0" {
			db0Target = &targets[i]
			break
		}
	}

	require.NotNil(t, db0Target)
	assert.Equal(t, TargetTypeDatabase, db0Target.TargetType)
}

func TestDiscoverDatabases_ConnectionError(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL:  "redis://nonexistent:6379",
		Name: "test-redis",
	}

	// When
	targets, err := discoverDatabases(context.Background(), endpoint)

	// Then
	require.Error(t, err)
	assert.Nil(t, targets)
	assert.Contains(t, err.Error(), "ping")
}

func TestDiscoverDatabases_InvalidURL(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL:  "not-a-valid-url",
		Name: "test-redis",
	}

	// When
	targets, err := discoverDatabases(context.Background(), endpoint)

	// Then
	require.Error(t, err)
	assert.Nil(t, targets)
}

func TestDiscoverDatabases_WithoutName(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL: fmt.Sprintf("redis://%s", mr.Addr()),
		// No Name set - should use host:port as name
	}

	// When
	targets, err := discoverDatabases(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	// Name should be derived from host:port
	assert.Contains(t, targets[0].Label, mr.Host())
}

func TestDatabaseDiscovery_DiscoverTargets(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Test using the underlying discovery function directly to avoid race conditions
	// with the cached discovery's background refresh goroutine
	endpoint := &config.RedisEndpoint{
		URL:  fmt.Sprintf("redis://%s", mr.Addr()),
		Name: "test-redis",
	}

	// When
	targets, err := discoverDatabases(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	require.NotEmpty(t, targets)
}
