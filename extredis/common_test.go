// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"testing"

	"github.com/steadybit/discovery-kit/go/discovery_kit_api"
	"github.com/steadybit/extension-redis/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConstants(t *testing.T) {
	// Verify target type constants
	assert.Equal(t, "com.steadybit.extension_redis.instance", TargetTypeInstance)
	assert.Equal(t, "com.steadybit.extension_redis.database", TargetTypeDatabase)

	// Verify attribute constants
	assert.Equal(t, "redis.url", AttrRedisURL)
	assert.Equal(t, "redis.host", AttrRedisHost)
	assert.Equal(t, "redis.port", AttrRedisPort)
	assert.Equal(t, "redis.version", AttrRedisVersion)
	assert.Equal(t, "redis.role", AttrRedisRole)
	assert.Equal(t, "redis.database.index", AttrDatabaseIndex)
}

func TestFetchTargetsPerEndpoint_EmptyEndpoints(t *testing.T) {
	// Given
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()
	config.Config.Endpoints = []config.RedisEndpoint{}

	// When
	targets, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		return []discovery_kit_api.Target{{Id: "test"}}, nil
	})

	// Then
	require.NoError(t, err)
	assert.Empty(t, targets)
}

func TestFetchTargetsPerEndpoint_MultipleEndpoints(t *testing.T) {
	// Given
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()
	config.Config.Endpoints = []config.RedisEndpoint{
		{URL: "redis://host1:6379", Name: "redis1"},
		{URL: "redis://host2:6379", Name: "redis2"},
	}

	// When
	targets, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		return []discovery_kit_api.Target{
			{Id: endpoint.Name, Label: endpoint.Name},
		}, nil
	})

	// Then
	require.NoError(t, err)
	require.Len(t, targets, 2)
	assert.Equal(t, "redis1", targets[0].Id)
	assert.Equal(t, "redis2", targets[1].Id)
}

func TestFetchTargetsPerEndpoint_ContinuesOnError(t *testing.T) {
	// Given
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()
	config.Config.Endpoints = []config.RedisEndpoint{
		{URL: "redis://host1:6379", Name: "redis1"},
		{URL: "redis://host2:6379", Name: "redis2"},
		{URL: "redis://host3:6379", Name: "redis3"},
	}

	callCount := 0
	// When
	targets, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		callCount++
		if endpoint.Name == "redis2" {
			return nil, assert.AnError
		}
		return []discovery_kit_api.Target{
			{Id: endpoint.Name},
		}, nil
	})

	// Then - should continue despite error on redis2
	require.NoError(t, err)
	assert.Equal(t, 3, callCount)
	require.Len(t, targets, 2)
	assert.Equal(t, "redis1", targets[0].Id)
	assert.Equal(t, "redis3", targets[1].Id)
}

func TestRedisIcon_IsSet(t *testing.T) {
	assert.NotEmpty(t, redisIcon)
	assert.Contains(t, redisIcon, "data:image/svg+xml")
}

func TestFetchTargetsPerEndpoint_SequentialProcessing(t *testing.T) {
	// Given - endpoints processed sequentially
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()
	config.Config.Endpoints = []config.RedisEndpoint{
		{URL: "redis://host1:6379", Name: "redis1"},
	}

	callCount := 0
	// When
	targets, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		callCount++
		return []discovery_kit_api.Target{
			{Id: endpoint.Name, Label: endpoint.Name},
		}, nil
	})

	// Then
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)
	require.Len(t, targets, 1)
}

func TestAttrConstants_Values(t *testing.T) {
	// Verify additional attribute constants
	assert.Equal(t, "redis.name", AttrRedisName)
	assert.Equal(t, "redis.database.name", AttrDatabaseName)
}
