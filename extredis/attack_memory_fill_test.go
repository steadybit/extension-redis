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

func TestMemoryFillAttack_Describe(t *testing.T) {
	// Given
	action := &memoryFillAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.memory-fill", desc.Id)
	assert.Equal(t, "Fill Memory", desc.Label)
	assert.Contains(t, desc.Description, "Fills Redis memory")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.GreaterOrEqual(t, len(desc.Parameters), 3)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "duration")
	assert.Contains(t, paramNames, "valueSize")
	assert.Contains(t, paramNames, "maxMemory")
}

func TestMemoryFillAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &memoryFillAttack{}
	state := MemoryFillState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":  float64(60000),
			"valueSize": float64(10240),
			"fillRate":  float64(10),
			"maxMemory": float64(100),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestMemoryFillAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &memoryFillAttack{}
	state := MemoryFillState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":  float64(60000),
			"valueSize": float64(10240),
			"fillRate":  float64(10),
			"maxMemory": float64(100),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 10240, state.ValueSize)
	assert.Equal(t, 10, state.FillRate)
	assert.Equal(t, 100, state.MaxMemory)
	assert.NotEmpty(t, state.KeyPrefix)
	assert.Contains(t, state.KeyPrefix, "steadybit-memfill-")
}

func TestMemoryFillAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &memoryFillAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, MemoryFillState{}, state)
}

func TestGenerateRandomValue(t *testing.T) {
	// Given
	size := 100

	// When
	value := generateRandomValue(size)

	// Then
	assert.Len(t, value, size)
	// Verify it contains only valid characters
	for _, c := range value {
		assert.True(t,
			(c >= 'a' && c <= 'z') ||
				(c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9'),
			"unexpected character: %c", c)
	}
}

func TestGenerateRandomValue_DifferentValues(t *testing.T) {
	// Given
	size := 50

	// When
	value1 := generateRandomValue(size)
	value2 := generateRandomValue(size)

	// Then - values should be different (with very high probability)
	assert.NotEqual(t, value1, value2)
}

func TestMemoryFillAttack_Start(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   "steadybit-memfill-test-",
		ValueSize:   100,
		FillRate:    1,
		MaxMemory:   1,
		EndTime:     time.Now().Add(1 * time.Second).Unix(),
		CreatedKeys: []string{},
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Messages)
	assert.Len(t, *result.Messages, 1)
}

func TestMemoryFillAttack_Status(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   "steadybit-memfill-test-",
		ValueSize:   100,
		FillRate:    1,
		MaxMemory:   1,
		EndTime:     time.Now().Add(60 * time.Second).Unix(),
		CreatedKeys: []string{"key1", "key2"},
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
}

func TestMemoryFillAttack_Stop(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create some keys to clean up
	mr.Set("steadybit-memfill-test-0", "value")
	mr.Set("steadybit-memfill-test-1", "value")

	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   "steadybit-memfill-test-",
		CreatedKeys: []string{"steadybit-memfill-test-0", "steadybit-memfill-test-1"},
	}

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Messages)

	// Verify keys were deleted
	exists := mr.Exists("steadybit-memfill-test-0")
	assert.False(t, exists)
}

func TestMemoryFillAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    "redis://nonexistent:6379",
		DB:          0,
		KeyPrefix:   "steadybit-memfill-test-",
		ValueSize:   100,
		CreatedKeys: []string{},
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestMemoryFillAttack_Status_Completed(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryFillAttack{}
	state := MemoryFillState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   "steadybit-memfill-test-",
		ValueSize:   100,
		FillRate:    1,
		MaxMemory:   1,
		EndTime:     time.Now().Add(-1 * time.Second).Unix(), // Already expired
		CreatedKeys: []string{},
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestNewMemoryFillAttack(t *testing.T) {
	// When
	action := NewMemoryFillAttack()

	// Then
	require.NotNil(t, action)
}
