// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"testing"
	"time"

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

func TestFetchTargetsPerEndpoint_SkipsPausedEndpointAndServesCache(t *testing.T) {
	// Given two endpoints, one of which is paused. The paused endpoint has a
	// previously-cached target list; the handler must not be invoked for it
	// while the pause is active, and the cached targets must still be returned.
	origEndpoints := config.Config.Endpoints
	defer func() {
		config.Config.Endpoints = origEndpoints
		ResetPauseRegistry()
	}()
	ResetPauseRegistry()

	paused := config.RedisEndpoint{URL: "redis://paused:6379", Name: "paused"}
	healthy := config.RedisEndpoint{URL: "redis://healthy:6379", Name: "healthy"}
	config.Config.Endpoints = []config.RedisEndpoint{paused, healthy}

	rememberTargets(paused.URL, []discovery_kit_api.Target{{Id: "cached-paused"}})
	MarkPaused(paused.URL, time.Now().Add(30*time.Second))

	calls := map[string]int{}
	targets, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		calls[endpoint.URL]++
		return []discovery_kit_api.Target{{Id: endpoint.Name}}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 0, calls[paused.URL], "paused endpoint should not be probed")
	assert.Equal(t, 1, calls[healthy.URL])
	require.Len(t, targets, 2)
	assert.Equal(t, "cached-paused", targets[0].Id)
	assert.Equal(t, "healthy", targets[1].Id)
}

func TestFetchTargetsPerEndpoint_PausedEndpointWithoutCacheReturnsNothing(t *testing.T) {
	// A paused endpoint with no prior successful discovery contributes nothing
	// — better than a probe that would block on `i/o timeout`.
	origEndpoints := config.Config.Endpoints
	defer func() {
		config.Config.Endpoints = origEndpoints
		ResetPauseRegistry()
	}()
	ResetPauseRegistry()

	paused := config.RedisEndpoint{URL: "redis://paused:6379", Name: "paused"}
	config.Config.Endpoints = []config.RedisEndpoint{paused}
	MarkPaused(paused.URL, time.Now().Add(30*time.Second))

	calls := 0
	targets, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		calls++
		return []discovery_kit_api.Target{{Id: endpoint.Name}}, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 0, calls)
	assert.Empty(t, targets)
}

func TestFetchTargetsPerEndpoint_RemembersSuccessfulResultsForLaterRecall(t *testing.T) {
	// After a successful discovery, the same endpoint's targets must be served
	// from cache once it is subsequently paused.
	origEndpoints := config.Config.Endpoints
	defer func() {
		config.Config.Endpoints = origEndpoints
		ResetPauseRegistry()
	}()
	ResetPauseRegistry()

	endpoint := config.RedisEndpoint{URL: "redis://will-be-paused:6379", Name: "wp"}
	config.Config.Endpoints = []config.RedisEndpoint{endpoint}

	_, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		return []discovery_kit_api.Target{{Id: "first"}, {Id: "second"}}, nil
	})
	require.NoError(t, err)

	MarkPaused(endpoint.URL, time.Now().Add(30*time.Second))

	targets, err := FetchTargetsPerEndpoint(func(endpoint *config.RedisEndpoint) ([]discovery_kit_api.Target, error) {
		t.Fatalf("handler must not be called for paused endpoint")
		return nil, nil
	})

	require.NoError(t, err)
	require.Len(t, targets, 2)
	assert.Equal(t, "first", targets[0].Id)
	assert.Equal(t, "second", targets[1].Id)
}
