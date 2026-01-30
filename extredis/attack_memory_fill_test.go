// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: 2024 Steadybit GmbH

package extredis

import (
	"context"
	"testing"

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
