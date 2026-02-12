// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

//go:build integration

package extredis

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getRedisURL returns the Redis URL for integration tests.
// Uses REDIS_URL env var or defaults to localhost:6379
func getRedisURL() string {
	if url := os.Getenv("REDIS_URL"); url != "" {
		return url
	}
	return "redis://localhost:6379"
}

// skipIfNoRedis skips the test if Redis is not available
func skipIfNoRedis(t *testing.T) string {
	url := getRedisURL()
	client, err := clients.CreateRedisClientFromURL(url, "", 0)
	if err != nil {
		t.Skipf("Redis not available: %v", err)
		return ""
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
		return ""
	}
	return url
}

// Integration tests for attacks

func TestIntegration_ClientPauseAttack_StartStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL:  redisURL,
		DB:        0,
		PauseMode: "WRITE",
		EndTime:   time.Now().Add(5 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	assert.False(t, statusResult.Completed)

	// Stop
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}

func TestIntegration_MaxmemoryLimitAttack_StartStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:     redisURL,
		DB:           0,
		NewMaxmemory: "100mb",
		NewPolicy:    "noeviction",
		EndTime:      time.Now().Add(5 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify original values were saved
	assert.NotEmpty(t, state.OriginalMaxmemory)
	assert.NotEmpty(t, state.OriginalPolicy)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)

	// Stop (restores original settings)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}

func TestIntegration_BgsaveAttack_Start(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &bgsaveAttack{}
	state := BgsaveState{
		RedisURL: redisURL,
		DB:       0,
	}

	// Start (triggers BGSAVE)
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestIntegration_MemoryFillAttack_StartStatusStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    redisURL,
		DB:          0,
		KeyPrefix:   fmt.Sprintf("steadybit-test-%s-", uuid.New().String()[:8]),
		ValueSize:   1024,
		FillRate:    100,
		MaxMemory:   1, // 1 MB
		EndTime:     time.Now().Add(2 * time.Second).Unix(),
		CreatedKeys: []string{},
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Wait for some keys to be created
	time.Sleep(500 * time.Millisecond)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)

	// Wait for completion
	time.Sleep(2 * time.Second)

	// Stop (cleans up keys)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}

func TestIntegration_ConnectionExhaustionAttack_StartStatusStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{
		RedisURL:       redisURL,
		DB:             0,
		NumConnections: 10,
		EndTime:        time.Now().Add(5 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Greater(t, state.ConnectionCount, 0)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)

	// Stop
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}

// Integration tests for checks

func TestIntegration_LatencyCheck_StartStatusStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     redisURL,
		MaxLatencyMs: 1000, // 1 second - generous threshold
		EndTime:      time.Now().Add(3 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	assert.False(t, statusResult.Completed)
	require.NotNil(t, statusResult.Metrics)
	assert.GreaterOrEqual(t, len(*statusResult.Metrics), 1)
}

func TestIntegration_MemoryCheck_StartStatus(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         redisURL,
		MaxMemoryPercent: 99, // High threshold to avoid false failures
		MaxMemoryBytes:   0,
		EndTime:          time.Now().Add(3 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	require.NotNil(t, statusResult.Metrics)
}

func TestIntegration_ConnectionCountCheck_StartStatus(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          redisURL,
		MaxConnectionsPct: 99,
		MaxConnections:    10000,
		EndTime:           time.Now().Add(3 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	require.NotNil(t, statusResult.Metrics)
}

func TestIntegration_BlockedClientsCheck_StartStatus(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          redisURL,
		MaxBlockedClients: 100,
		EndTime:           time.Now().Add(3 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	require.NotNil(t, statusResult.Metrics)
}

func TestIntegration_CacheHitRateCheck_StartStatus(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	// First, generate some cache hits/misses
	client, err := clients.CreateRedisClientFromURL(redisURL, "", 0)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	// Generate hits
	client.Set(ctx, "test:hitrate:key", "value", time.Minute)
	for i := 0; i < 5; i++ {
		client.Get(ctx, "test:hitrate:key")
	}
	// Generate misses
	for i := 0; i < 2; i++ {
		client.Get(ctx, "test:hitrate:nonexistent")
	}
	// Cleanup
	defer client.Del(ctx, "test:hitrate:key")

	action := &cacheHitRateCheck{}
	state := CacheHitRateCheckState{
		RedisURL:        redisURL,
		MinHitRate:      10, // Low threshold
		MinObservedRate: 100,
		FirstCheck:      true,
		EndTime:         time.Now().Add(3 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
}

func TestIntegration_ReplicationLagCheck_StartStatus(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{
		RedisURL:      redisURL,
		MaxLagSeconds: 60,
		RequireLinkUp: false, // Don't require replication for standalone Redis
		EndTime:       time.Now().Add(3 * time.Second).Unix(),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Status - should work even for master/standalone
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
}

// Integration tests for discovery

func TestIntegration_InstanceDiscovery_DiscoverTargets(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	// Configure the endpoint
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()

	config.Config.Endpoints = []config.RedisEndpoint{
		{URL: redisURL, Name: "test-redis"},
	}

	discovery := &redisInstanceDiscovery{}
	targets, err := discovery.DiscoverTargets(context.Background())
	require.NoError(t, err)
	require.Len(t, targets, 1)

	// Verify target attributes
	target := targets[0]
	assert.Contains(t, target.Attributes, AttrRedisURL)
	assert.Contains(t, target.Attributes, AttrRedisHost)
	assert.Contains(t, target.Attributes, AttrRedisPort)
}

func TestIntegration_DatabaseDiscovery_DiscoverTargets(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	// Create a key in db 0 to ensure it's discovered
	client, err := clients.CreateRedisClientFromURL(redisURL, "", 0)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	client.Set(ctx, "test:discovery:key", "value", time.Minute)
	defer client.Del(ctx, "test:discovery:key")

	// Configure the endpoint
	origEndpoints := config.Config.Endpoints
	defer func() { config.Config.Endpoints = origEndpoints }()

	config.Config.Endpoints = []config.RedisEndpoint{
		{URL: redisURL, Name: "test-redis"},
	}

	discovery := &redisDatabaseDiscovery{}
	targets, err := discovery.DiscoverTargets(context.Background())
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(targets), 1) // At least db0 should be discovered

	// Find db0
	var db0Found bool
	for _, target := range targets {
		if dbIndex, ok := target.Attributes[AttrDatabaseIndex]; ok && len(dbIndex) > 0 && dbIndex[0] == "0" {
			db0Found = true
			break
		}
	}
	assert.True(t, db0Found, "db0 should be discovered")
}

// Integration tests for key operations

func TestIntegration_KeyDeleteAttack_StartStatusStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	// Create test keys
	client, err := clients.CreateRedisClientFromURL(redisURL, "", 0)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	testPrefix := fmt.Sprintf("test:delete:%s:", uuid.New().String()[:8])
	client.Set(ctx, testPrefix+"key1", "value1", 0)
	client.Set(ctx, testPrefix+"key2", "value2", 0)

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      redisURL,
		DB:            0,
		Pattern:       testPrefix + "*",
		MaxKeys:       100,
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]string),
		EndTime:       time.Now().Add(5 * time.Second).Unix(),
	}

	// Start (deletes keys)
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.DeletedKeys, 2)

	// Verify keys are deleted
	exists, _ := client.Exists(ctx, testPrefix+"key1").Result()
	assert.Equal(t, int64(0), exists)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)

	// Stop (restores keys)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Verify keys are restored
	val, err := client.Get(ctx, testPrefix+"key1").Result()
	require.NoError(t, err)
	assert.Equal(t, "value1", val)

	// Cleanup
	client.Del(ctx, testPrefix+"key1", testPrefix+"key2")
}

func TestIntegration_CacheExpirationAttack_StartStatusStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	// Create test keys with no expiration
	client, err := clients.CreateRedisClientFromURL(redisURL, "", 0)
	require.NoError(t, err)
	defer client.Close()

	ctx := context.Background()
	testPrefix := fmt.Sprintf("test:expire:%s:", uuid.New().String()[:8])
	client.Set(ctx, testPrefix+"key1", "value1", 0)
	client.Set(ctx, testPrefix+"key2", "value2", 0)

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:      redisURL,
		DB:            0,
		Pattern:       testPrefix + "*",
		TTLSeconds:    60, // Set 60 second TTL
		MaxKeys:       100,
		RestoreOnStop: true,
		AffectedKeys:  []string{},
		BackupData:    make(map[string]KeyBackup),
		EndTime:       time.Now().Add(5 * time.Second).Unix(),
	}

	// Start (sets TTL on keys)
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.AffectedKeys, 2)

	// Verify TTL is set
	ttl, _ := client.TTL(ctx, testPrefix+"key1").Result()
	assert.Greater(t, ttl.Seconds(), float64(0))

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)

	// Stop (restores original TTL)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Cleanup
	client.Del(ctx, testPrefix+"key1", testPrefix+"key2")
}

func TestIntegration_CachePenetrationAttack_StartStatusStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    redisURL,
		DB:          0,
		Concurrency: 5,
		KeyPrefix:   fmt.Sprintf("steadybit-penetration-miss-%s-", uuid.New().String()[:8]),
		EndTime:     time.Now().Add(3 * time.Second).Unix(),
		AttackKey:   fmt.Sprintf("%s-inttest-%d", redisURL, time.Now().UnixNano()),
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Wait for workers to send some requests
	time.Sleep(500 * time.Millisecond)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	assert.False(t, statusResult.Completed)

	// Wait for completion
	time.Sleep(3 * time.Second)

	// Stop
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}

// Integration test for BigKeyAttack
func TestIntegration_BigKeyAttack_StartStatusStop(t *testing.T) {
	redisURL := skipIfNoRedis(t)

	action := &bigKeyAttack{}
	executionId := uuid.New().String()[:8]
	state := BigKeyState{
		RedisURL:    redisURL,
		DB:          0,
		KeyPrefix:   fmt.Sprintf("steadybit-bigkey-%s-", executionId),
		KeySize:     10 * 1024, // 10KB
		NumKeys:     2,
		CreatedKeys: []string{},
		EndTime:     time.Now().Add(3 * time.Second).Unix(),
		ExecutionId: executionId,
	}

	// Start
	result, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Wait for keys to be created
	time.Sleep(500 * time.Millisecond)

	// Status
	statusResult, err := action.Status(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, statusResult)

	// Wait for completion
	time.Sleep(3 * time.Second)

	// Stop (cleans up keys)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}
