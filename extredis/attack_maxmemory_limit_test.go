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

func TestMaxmemoryLimitAttack_Describe(t *testing.T) {
	// Given
	action := &maxmemoryLimitAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.maxmemory-limit", desc.Id)
	assert.Equal(t, "Limit MaxMemory", desc.Label)
	assert.Contains(t, desc.Description, "maxmemory")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.Len(t, desc.Parameters, 3)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "duration")
	assert.Contains(t, paramNames, "maxmemory")
	assert.Contains(t, paramNames, "evictionPolicy")
}

func TestMaxmemoryLimitAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":       float64(60000),
			"maxmemory":      "10mb",
			"evictionPolicy": "noeviction",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestMaxmemoryLimitAttack_Prepare_MissingMaxmemory(t *testing.T) {
	// Given
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":       float64(60000),
			"maxmemory":      "",
			"evictionPolicy": "noeviction",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxmemory is required")
}

func TestMaxmemoryLimitAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":       float64(90000),
			"maxmemory":      "50mb",
			"evictionPolicy": "allkeys-lru",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, "50mb", state.NewMaxmemory)
	assert.Equal(t, "allkeys-lru", state.NewPolicy)
	assert.WithinDuration(t, time.Now().Add(90*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestMaxmemoryLimitAttack_Prepare_KeepPolicy(t *testing.T) {
	// Given
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":       float64(60000),
			"maxmemory":      "10mb",
			"evictionPolicy": "keep",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "keep", state.NewPolicy)
}

func TestMaxmemoryLimitAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &maxmemoryLimitAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, MaxmemoryLimitState{}, state)
}

func TestMaxmemoryLimitAttack_Status(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		DB:           0,
		NewMaxmemory: "10mb",
		NewPolicy:    "noeviction",
		EndTime:      time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
}

func TestMaxmemoryLimitAttack_Status_Completed(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		DB:           0,
		NewMaxmemory: "10mb",
		NewPolicy:    "noeviction",
		EndTime:      time.Now().Add(-10 * time.Second).Unix(), // Already past
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestMaxmemoryLimitAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:     "redis://nonexistent:6379",
		DB:           0,
		NewMaxmemory: "10mb",
		NewPolicy:    "noeviction",
		EndTime:      time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestMaxmemoryLimitAttack_Stop_WithRestoreErrors(t *testing.T) {
	// Given - connection fails during ConfigSet, not during client creation
	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:          "redis://nonexistent:6379",
		DB:                0,
		OriginalMaxmemory: "0",
		OriginalPolicy:    "noeviction",
	}

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then - Stop returns result with warning messages (doesn't return error)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Messages)
}

func TestNewMaxmemoryLimitAttack(t *testing.T) {
	// When
	action := NewMaxmemoryLimitAttack()

	// Then
	require.NotNil(t, action)
}

func TestMaxmemoryLimitAttack_Stop_Success(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &maxmemoryLimitAttack{}
	state := MaxmemoryLimitState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		OriginalMaxmemory: "0",
		OriginalPolicy:    "noeviction",
	}

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then - miniredis doesn't support CONFIG SET, so this will have warnings
	require.NoError(t, err)
	require.NotNil(t, result)
}
