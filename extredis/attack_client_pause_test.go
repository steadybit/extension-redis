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

func TestClientPauseAttack_Describe(t *testing.T) {
	// Given
	action := &clientPauseAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.client-pause", desc.Id)
	assert.Equal(t, "Pause Clients", desc.Label)
	assert.Contains(t, desc.Description, "CLIENT PAUSE")
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
	assert.Contains(t, paramNames, "pauseMode")
}

func TestClientPauseAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &clientPauseAttack{}
	state := ClientPauseState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":  float64(30000),
			"pauseMode": "ALL",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestClientPauseAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &clientPauseAttack{}
	state := ClientPauseState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":  float64(45000),
			"pauseMode": "WRITE",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, "WRITE", state.PauseMode)
	assert.WithinDuration(t, time.Now().Add(45*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestClientPauseAttack_Prepare_DefaultPauseMode(t *testing.T) {
	// Given - invalid pause mode
	action := &clientPauseAttack{}
	state := ClientPauseState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":  float64(30000),
			"pauseMode": "INVALID",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then - should default to ALL
	require.NoError(t, err)
	assert.Equal(t, "ALL", state.PauseMode)
}

func TestClientPauseAttack_Prepare_AllMode(t *testing.T) {
	// Given
	action := &clientPauseAttack{}
	state := ClientPauseState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":  float64(30000),
			"pauseMode": "ALL",
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "ALL", state.PauseMode)
}

func TestClientPauseAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &clientPauseAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, ClientPauseState{}, state)
}
