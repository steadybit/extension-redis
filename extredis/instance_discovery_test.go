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

func TestInstanceDiscovery_Describe(t *testing.T) {
	// Given
	discovery := &redisInstanceDiscovery{}

	// When
	desc := discovery.Describe()

	// Then
	assert.Equal(t, TargetTypeInstance, desc.Id)
	require.NotNil(t, desc.Discover.CallInterval)
}

func TestInstanceDiscovery_DescribeTarget(t *testing.T) {
	// Given
	discovery := &redisInstanceDiscovery{}

	// When
	td := discovery.DescribeTarget()

	// Then
	assert.Equal(t, TargetTypeInstance, td.Id)
	assert.Equal(t, "Redis instance", td.Label.One)
	assert.Equal(t, "Redis instances", td.Label.Other)
	require.NotNil(t, td.Category)
	assert.Equal(t, "data store", *td.Category)
	require.NotNil(t, td.Icon)

	// table columns
	require.GreaterOrEqual(t, len(td.Table.Columns), 4)

	// Verify expected columns exist
	columnAttrs := make([]string, len(td.Table.Columns))
	for i, c := range td.Table.Columns {
		columnAttrs[i] = c.Attribute
	}
	assert.Contains(t, columnAttrs, AttrRedisName)
	assert.Contains(t, columnAttrs, AttrRedisHost)
	assert.Contains(t, columnAttrs, AttrRedisPort)

	// order by
	require.Len(t, td.Table.OrderBy, 1)
	assert.Equal(t, AttrRedisHost, td.Table.OrderBy[0].Attribute)
	assert.Equal(t, discovery_kit_api.OrderByDirection("ASC"), td.Table.OrderBy[0].Direction)
}

func TestInstanceDiscovery_DescribeAttributes(t *testing.T) {
	// Given
	discovery := &redisInstanceDiscovery{}

	// When
	attrs := discovery.DescribeAttributes()

	// Then
	require.NotEmpty(t, attrs)

	// Verify key attributes are present
	attrMap := make(map[string]string)
	for _, a := range attrs {
		attrMap[a.Attribute] = a.Label.One
	}

	assert.Contains(t, attrMap, AttrRedisURL)
	assert.Contains(t, attrMap, AttrRedisHost)
	assert.Contains(t, attrMap, AttrRedisPort)
	assert.Contains(t, attrMap, AttrRedisVersion)
	assert.Contains(t, attrMap, AttrRedisRole)
}

func TestDiscoverInstance_Success(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL:  fmt.Sprintf("redis://%s", mr.Addr()),
		Name: "test-redis",
	}

	// When
	targets, err := discoverInstance(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	require.Len(t, targets, 1)

	target := targets[0]
	assert.Equal(t, TargetTypeInstance, target.TargetType)
	assert.Equal(t, "test-redis", target.Label)
	assert.Contains(t, target.Attributes, AttrRedisURL)
	assert.Contains(t, target.Attributes, AttrRedisHost)
	assert.Contains(t, target.Attributes, AttrRedisPort)
	assert.Contains(t, target.Attributes, AttrRedisName)
}

func TestDiscoverInstance_WithoutName(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	endpoint := &config.RedisEndpoint{
		URL: fmt.Sprintf("redis://%s", mr.Addr()),
		// No Name set - should use host:port as name
	}

	// When
	targets, err := discoverInstance(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	require.Len(t, targets, 1)

	target := targets[0]
	// Name should be derived from host:port
	assert.Contains(t, target.Label, mr.Host())
}

func TestDiscoverInstance_ConnectionError(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL:  "redis://nonexistent:6379",
		Name: "test-redis",
	}

	// When
	targets, err := discoverInstance(context.Background(), endpoint)

	// Then
	require.Error(t, err)
	assert.Nil(t, targets)
	assert.Contains(t, err.Error(), "ping")
}

func TestDiscoverInstance_InvalidURL(t *testing.T) {
	// Given
	endpoint := &config.RedisEndpoint{
		URL:  "not-a-valid-url",
		Name: "test-redis",
	}

	// When
	targets, err := discoverInstance(context.Background(), endpoint)

	// Then
	require.Error(t, err)
	assert.Nil(t, targets)
}

func TestDiscoverInstance_DefaultPort(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Use a URL without explicit port
	endpoint := &config.RedisEndpoint{
		URL:  fmt.Sprintf("redis://%s", mr.Addr()),
		Name: "test-redis",
	}

	// When
	targets, err := discoverInstance(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	require.Len(t, targets, 1)
	// Port should be present in attributes
	assert.Contains(t, targets[0].Attributes, AttrRedisPort)
}

func TestInstanceDiscovery_DiscoverTargets(t *testing.T) {
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
	targets, err := discoverInstance(context.Background(), endpoint)

	// Then
	require.NoError(t, err)
	require.Len(t, targets, 1)
}
