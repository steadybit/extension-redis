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

func TestConnectionExhaustionAttack_Describe(t *testing.T) {
	// Given
	action := &connectionExhaustionAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.connection-exhaustion", desc.Id)
	assert.Equal(t, "Exhaust Connections", desc.Label)
	assert.Contains(t, desc.Description, "Opens many connections")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.Len(t, desc.Parameters, 2)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "duration")
	assert.Contains(t, paramNames, "numConnections")

	// Check numConnections has min/max
	for _, p := range desc.Parameters {
		if p.Name == "numConnections" {
			require.NotNil(t, p.MinValue)
			require.NotNil(t, p.MaxValue)
			assert.Equal(t, 1, *p.MinValue)
			assert.Equal(t, 10000, *p.MaxValue)
		}
	}
}

func TestConnectionExhaustionAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":       float64(60000),
			"numConnections": float64(100),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestConnectionExhaustionAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &connectionExhaustionAttack{}
	state := ConnectionExhaustionState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":       float64(30000), // 30 seconds in ms
			"numConnections": float64(50),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 50, state.NumConnections)
	assert.Greater(t, state.EndTime, int64(0))
	assert.Equal(t, 0, state.ConnectionCount)
}

func TestConnectionExhaustionAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &connectionExhaustionAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, ConnectionExhaustionState{}, state)
}

func TestActiveConnections_MapInitialized(t *testing.T) {
	// Verify the global map is initialized
	assert.NotNil(t, activeConnections)
}
