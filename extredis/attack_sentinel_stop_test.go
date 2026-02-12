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

func TestSentinelStopAttack_Describe(t *testing.T) {
	// Given
	action := &sentinelStopAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.sentinel-stop", desc.Id)
	assert.Equal(t, "Stop Sentinel", desc.Label)
	assert.Contains(t, desc.Description, "DEBUG SLEEP")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.Len(t, desc.Parameters, 1)
	assert.Equal(t, "duration", desc.Parameters[0].Name)
}

func TestSentinelStopAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &sentinelStopAttack{}
	state := SentinelStopState{}
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

func TestSentinelStopAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &sentinelStopAttack{}
	state := SentinelStopState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:26379"},
			},
		},
		Config: map[string]any{
			"duration": float64(45000),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:26379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.WithinDuration(t, time.Now().Add(45*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestSentinelStopAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &sentinelStopAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, SentinelStopState{}, state)
}

func TestSentinelStopAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &sentinelStopAttack{}
	state := SentinelStopState{
		RedisURL: "redis://nonexistent:26379",
		DB:       0,
		EndTime:  time.Now().Add(30 * time.Second).Unix(),
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestSentinelStopAttack_Start_NotSentinelMode(t *testing.T) {
	// Given - miniredis runs in standalone mode, not sentinel
	// miniredis doesn't support INFO server or DEBUG SLEEP, so the attack
	// will fail at the DEBUG SLEEP step with an error
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &sentinelStopAttack{}
	state := SentinelStopState{
		RedisURL: fmt.Sprintf("redis://%s", mr.Addr()),
		DB:       0,
		EndTime:  time.Now().Add(5 * time.Second).Unix(),
	}

	// When
	_, err = action.Start(context.Background(), &state)

	// Then - miniredis doesn't support DEBUG SLEEP, so we expect an error
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DEBUG SLEEP")
}

func TestSentinelStopAttack_Status_NotCompleted(t *testing.T) {
	// Given
	action := &sentinelStopAttack{}
	state := SentinelStopState{
		RedisURL: "redis://localhost:26379",
		DB:       0,
		EndTime:  time.Now().Add(30 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
	require.NotNil(t, result.Messages)
	assert.Contains(t, (*result.Messages)[0].Message, "sleeping")
}

func TestSentinelStopAttack_Status_Completed(t *testing.T) {
	// Given
	action := &sentinelStopAttack{}
	state := SentinelStopState{
		RedisURL: "redis://localhost:26379",
		DB:       0,
		EndTime:  time.Now().Add(-10 * time.Second).Unix(), // Already past
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
	require.NotNil(t, result.Messages)
	assert.Contains(t, (*result.Messages)[0].Message, "completed")
}

func TestNewSentinelStopAttack(t *testing.T) {
	// When
	action := NewSentinelStopAttack()

	// Then
	require.NotNil(t, action)
}
