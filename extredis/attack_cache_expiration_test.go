// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"testing"
	"time"

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
