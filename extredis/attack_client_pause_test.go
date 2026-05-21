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

func TestClientPauseAttack_Describe(t *testing.T) {
	// Given
	action := &clientPauseAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.client-pause", desc.Id)
	assert.Equal(t, "Pause Write Clients", desc.Label)
	assert.Contains(t, desc.Description, "CLIENT PAUSE WRITE")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Only the duration parameter is exposed — the attack always pauses writes
	// only, so there is no mode selection.
	require.NotNil(t, desc.Parameters)
	require.Len(t, desc.Parameters, 1)
	assert.Equal(t, "duration", desc.Parameters[0].Name)
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
			"duration": float64(30000),
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
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &clientPauseAttack{}
	state := ClientPauseState{}
	redisURL := fmt.Sprintf("redis://%s", mr.Addr())
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {redisURL},
			},
		},
		Config: map[string]any{
			"duration": float64(45000),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err = action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, redisURL, state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.WithinDuration(t, time.Now().Add(45*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestClientPauseAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &clientPauseAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, ClientPauseState{}, state)
}

func TestClientPauseAttack_Status(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: fmt.Sprintf("redis://%s", mr.Addr()),
		DB:       0,
		EndTime:  time.Now().Add(30 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
}

func TestClientPauseAttack_Status_Completed(t *testing.T) {
	// Given
	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: "redis://localhost:6379",
		DB:       0,
		EndTime:  time.Now().Add(-10 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestClientPauseAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: "redis://nonexistent:6379",
		DB:       0,
		EndTime:  time.Now().Add(30 * time.Second).Unix(),
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestClientPauseAttack_Start_WithMiniredis(t *testing.T) {
	// Given - miniredis may not implement CLIENT PAUSE WRITE; either way the
	// connection/ping path is exercised.
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: fmt.Sprintf("redis://%s", mr.Addr()),
		DB:       0,
		EndTime:  time.Now().Add(5 * time.Second).Unix(),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	if err != nil {
		assert.Contains(t, err.Error(), "CLIENT PAUSE")
	} else {
		require.NotNil(t, result)
	}
}

func TestClientPauseAttack_Start_ExpiredDuration(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: fmt.Sprintf("redis://%s", mr.Addr()),
		DB:       0,
		EndTime:  time.Now().Add(-10 * time.Second).Unix(),
	}

	// When
	_, err = action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "positive")
}

func TestClientPauseAttack_Stop_ConnectionError(t *testing.T) {
	// Given
	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: "redis://nonexistent:6379",
		DB:       0,
		EndTime:  time.Now().Add(30 * time.Second).Unix(),
	}

	// When
	_, err := action.Stop(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestClientPauseAttack_Stop_WithMiniredis(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: fmt.Sprintf("redis://%s", mr.Addr()),
		DB:       0,
		EndTime:  time.Now().Add(30 * time.Second).Unix(),
	}

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then - miniredis may not support CLIENT UNPAUSE
	if err != nil {
		assert.Contains(t, err.Error(), "UNPAUSE")
	} else {
		require.NotNil(t, result)
	}
}

func TestClientPauseAttack_Status_RemainingNegative(t *testing.T) {
	// Given
	action := &clientPauseAttack{}
	state := ClientPauseState{
		RedisURL: "redis://localhost:6379",
		DB:       0,
		EndTime:  time.Now().Add(-100 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
	assert.Contains(t, (*result.Messages)[0].Message, "0 seconds remaining")
}

func TestNewClientPauseAttack(t *testing.T) {
	// When
	action := NewClientPauseAttack()

	// Then
	require.NotNil(t, action)
}
