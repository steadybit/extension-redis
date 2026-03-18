// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheExpirationAttack_Describe(t *testing.T) {
	// Given
	action := &cacheExpirationAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.database.cache-expiration", desc.Id)
	assert.Equal(t, "Force Cache Expiration", desc.Label)
	assert.Contains(t, desc.Description, "TTL")
	assert.Equal(t, TargetTypeDatabase, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.GreaterOrEqual(t, len(desc.Parameters), 4)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "duration")
	assert.Contains(t, paramNames, "pattern")
	assert.Contains(t, paramNames, "ttl")
	assert.Contains(t, paramNames, "maxKeys")
	assert.Contains(t, paramNames, "restoreOnStop")
}

func TestCacheExpirationAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestCacheExpirationAttack_Prepare_MissingPattern(t *testing.T) {
	// Given — need a real miniredis so we get past the URL check
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {fmt.Sprintf("redis://%s", mr.Addr())},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "",
			"ttl":           float64(5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err = action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")
}

func TestCacheExpirationAttack_Prepare_SetsState(t *testing.T) {
	// Given — miniredis with keys matching the pattern
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("cache:key1", "value1")
	mr.Set("cache:key2", "value2")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	redisURL := fmt.Sprintf("redis://%s", mr.Addr())
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {redisURL},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"duration":      float64(120000),
			"pattern":       "cache:*",
			"ttl":           float64(10),
			"maxKeys":       float64(50),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err = action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, redisURL, state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, "cache:*", state.Pattern)
	assert.Equal(t, 10, state.TTLSeconds)
	assert.Equal(t, 50, state.MaxKeys)
	assert.True(t, state.RestoreOnStop)
	assert.Empty(t, state.AffectedKeys)
	assert.NotNil(t, state.BackupData)
	assert.WithinDuration(t, time.Now().Add(120*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestCacheExpirationAttack_Prepare_MinimumTTL(t *testing.T) {
	// Given - TTL less than 1, miniredis with matching keys
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {fmt.Sprintf("redis://%s", mr.Addr())},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(0),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err = action.Prepare(context.Background(), &state, req)

	// Then - TTL should default to 1
	require.NoError(t, err)
	assert.Equal(t, 1, state.TTLSeconds)
}

func TestCacheExpirationAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &cacheExpirationAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, CacheExpirationState{}, state)
}

func TestCacheExpirationAttack_StartStatusStop(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create test keys
	mr.Set("test:key1", "value1")
	mr.Set("test:key2", "value2")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		Pattern:        "test:*",
		TTLSeconds:     60,
		MaxKeys:        100,
		AffectedKeys:   []string{},
		MatchedKeys:    []string{"test:key1", "test:key2"},
		BackupData:     make(map[string]KeyBackup),
		RestoreOnStop:  true,
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
	}

	// When - Start
	startResult, err := action.Start(context.Background(), &state)

	// Then - Start
	require.NoError(t, err)
	require.NotNil(t, startResult)
	assert.Len(t, state.AffectedKeys, 2)

	// When - Status
	statusResult, err := action.Status(context.Background(), &state)

	// Then - Status
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	assert.False(t, statusResult.Completed)

	// When - Stop
	stopResult, err := action.Stop(context.Background(), &state)

	// Then - Stop
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}

func TestCacheExpirationAttack_Prepare_NoMatchingKeys(t *testing.T) {
	// Given — miniredis with no keys matching the pattern
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {fmt.Sprintf("redis://%s", mr.Addr())},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "nonexistent:*",
			"ttl":           float64(5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err = action.Prepare(context.Background(), &state, req)

	// Then — Prepare now catches no-match and returns an error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no keys found matching pattern")
}

func TestCacheExpirationAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:     "redis://nonexistent:6379",
		DB:           0,
		Pattern:      "test:*",
		TTLSeconds:   5,
		MaxKeys:      100,
		AffectedKeys: []string{},
		MatchedKeys:  []string{"test:key1"},
		BackupData:   make(map[string]KeyBackup),
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestNewCacheExpirationAttack(t *testing.T) {
	// When
	action := NewCacheExpirationAttack()

	// Then
	require.NotNil(t, action)
}

func TestCacheExpirationAttack_Status_Completed(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		DB:           0,
		Pattern:      "test:*",
		TTLSeconds:   5,
		MaxKeys:      100,
		AffectedKeys: []string{"key1", "key2"},
		EndTime:      time.Now().Add(-10 * time.Second).Unix(), // Already expired
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestCacheExpirationAttack_Start_LargeNumberOfKeys(t *testing.T) {
	// Given - Redis with many string keys that require multiple SCAN iterations
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	keyCount := 1500
	matchedKeys := make([]string, keyCount)
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("expire:key:%04d", i)
		mr.Set(key, fmt.Sprintf("value-%d", i))
		matchedKeys[i] = key
	}

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		Pattern:        "expire:key:*",
		TTLSeconds:     300,
		MaxKeys:        0, // Unlimited
		AffectedKeys:   []string{},
		MatchedKeys:    matchedKeys,
		BackupData:     make(map[string]KeyBackup),
		RestoreOnStop:  false,
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.AffectedKeys, keyCount, "All %d string keys should have TTL set", keyCount)
	assert.Equal(t, 0, state.SkippedNonString)

	// Verify TTL is set on all keys
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("expire:key:%04d", i)
		ttl := mr.TTL(key)
		assert.Greater(t, ttl, time.Duration(0), "Key %s should have a TTL set", key)
	}

	// Cleanup: stop to release key locks
	_, _ = action.Stop(context.Background(), &state)
}

func TestCacheExpirationAttack_Start_LargeNumberOfKeys_WithMaxKeys(t *testing.T) {
	// Given - Redis with many keys but maxKeys limits the affected count
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	keyCount := 1500
	maxKeys := 200
	// MatchedKeys would have been limited in Prepare to maxKeys
	matchedKeys := make([]string, maxKeys)
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("limited-expire:key:%04d", i)
		mr.Set(key, fmt.Sprintf("value-%d", i))
		if i < maxKeys {
			matchedKeys[i] = key
		}
	}

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		Pattern:        "limited-expire:key:*",
		TTLSeconds:     300,
		MaxKeys:        maxKeys,
		AffectedKeys:   []string{},
		MatchedKeys:    matchedKeys,
		BackupData:     make(map[string]KeyBackup),
		RestoreOnStop:  false,
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.AffectedKeys, maxKeys, "Only %d keys should be affected due to maxKeys limit", maxKeys)

	// Cleanup: stop to release key locks
	_, _ = action.Stop(context.Background(), &state)
}

func TestCacheExpirationAttack_Start_LargeNumberOfKeys_WithRestoreOnStop(t *testing.T) {
	// Given - Redis with many keys, all backed up and restored
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	keyCount := 500
	matchedKeys := make([]string, keyCount)
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("restore-expire:key:%04d", i)
		mr.Set(key, fmt.Sprintf("value-%d", i))
		matchedKeys[i] = key
	}

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:       fmt.Sprintf("redis://%s", mr.Addr()),
		DB:             0,
		Pattern:        "restore-expire:key:*",
		TTLSeconds:     1,
		MaxKeys:        0,
		AffectedKeys:   []string{},
		MatchedKeys:    matchedKeys,
		BackupData:     make(map[string]KeyBackup),
		RestoreOnStop:  true,
		EndTime:        time.Now().Add(60 * time.Second).Unix(),
		MaxBackupBytes: 100 * 1024 * 1024,
	}

	// When - Start
	startResult, err := action.Start(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, startResult)
	assert.Len(t, state.AffectedKeys, keyCount)
	assert.Len(t, state.BackupData, keyCount)

	// Let keys expire in miniredis
	mr.FastForward(2 * time.Second)

	// When - Stop (restores all keys)
	stopResult, err := action.Stop(context.Background(), &state)
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Then - verify all keys are restored with correct values
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("restore-expire:key:%04d", i)
		assert.True(t, mr.Exists(key), "Key %s should exist after restore", key)
		val, err := mr.Get(key)
		require.NoError(t, err, "Key %s should be readable after restore", key)
		assert.Equal(t, fmt.Sprintf("value-%d", i), val, "Key %s should have correct value", key)
	}
}

func TestCacheExpirationAttack_Start_LargeNumberOfKeys_MixedTypes(t *testing.T) {
	// Given - Redis with many keys of different types, only strings should be affected
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	stringCount := 1000
	matchedKeys := make([]string, stringCount)
	for i := 0; i < stringCount; i++ {
		key := fmt.Sprintf("mixed:key:%04d", i)
		mr.Set(key, fmt.Sprintf("value-%d", i))
		matchedKeys[i] = key
	}
	// Add non-string keys (lists) — these would have been filtered out by Prepare,
	// so they are NOT in matchedKeys
	listCount := 300
	for i := 0; i < listCount; i++ {
		mr.Lpush(fmt.Sprintf("mixed:key:list:%04d", i), "item1")
	}

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		DB:               0,
		Pattern:          "mixed:key:*",
		TTLSeconds:       300,
		MaxKeys:          0,
		AffectedKeys:     []string{},
		MatchedKeys:      matchedKeys,
		BackupData:       make(map[string]KeyBackup),
		RestoreOnStop:    false,
		EndTime:          time.Now().Add(60 * time.Second).Unix(),
		SkippedNonString: listCount, // Set by Prepare
		MaxBackupBytes:   100 * 1024 * 1024,
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, state.AffectedKeys, stringCount, "Only string keys should be affected")

	// Cleanup: stop to release key locks
	_, _ = action.Stop(context.Background(), &state)
}

func TestCacheExpirationAttack_Prepare_DefaultDB(t *testing.T) {
	// Given - no database index attribute, miniredis with matching keys
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	mr.Set("test:key1", "value1")

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {fmt.Sprintf("redis://%s", mr.Addr())},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"pattern":       "test:*",
			"ttl":           float64(5),
			"maxKeys":       float64(100),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err = action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 0, state.DB)
}
