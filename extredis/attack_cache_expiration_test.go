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
	// Given
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
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
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")
}

func TestCacheExpirationAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"3"},
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
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 3, state.DB)
	assert.Equal(t, "cache:*", state.Pattern)
	assert.Equal(t, 10, state.TTLSeconds)
	assert.Equal(t, 50, state.MaxKeys)
	assert.True(t, state.RestoreOnStop)
	assert.Empty(t, state.AffectedKeys)
	assert.NotNil(t, state.BackupData)
	assert.WithinDuration(t, time.Now().Add(120*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestCacheExpirationAttack_Prepare_MinimumTTL(t *testing.T) {
	// Given - TTL less than 1
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
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
	_, err := action.Prepare(context.Background(), &state, req)

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
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "test:*",
		TTLSeconds:    60,
		MaxKeys:       100,
		AffectedKeys:  []string{},
		BackupData:    make(map[string]KeyBackup),
		RestoreOnStop: true,
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
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

func TestCacheExpirationAttack_Start_NoMatchingKeys(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "nonexistent:*",
		TTLSeconds:    5,
		MaxKeys:       100,
		AffectedKeys:  []string{},
		BackupData:    make(map[string]KeyBackup),
		RestoreOnStop: false,
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then - should succeed but with warning about no keys
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Messages)
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
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{
		RedisURL:     "redis://localhost:6379",
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

func TestCacheExpirationAttack_Prepare_DefaultDB(t *testing.T) {
	// Given - no database index attribute
	action := &cacheExpirationAttack{}
	state := CacheExpirationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
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
	require.NoError(t, err)
	assert.Equal(t, 0, state.DB)
}
