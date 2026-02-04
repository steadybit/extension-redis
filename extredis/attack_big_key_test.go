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

func TestBigKeyAttack_Describe(t *testing.T) {
	// Given
	action := &bigKeyAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.big-key", desc.Id)
	assert.Equal(t, "Create Big Keys", desc.Label)
	assert.Contains(t, desc.Description, "large keys")
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
	assert.Contains(t, paramNames, "keySize")
	assert.Contains(t, paramNames, "numKeys")
}

func TestBigKeyAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &bigKeyAttack{}
	state := BigKeyState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration": float64(60000),
			"keySize":  float64(10),
			"numKeys":  float64(1),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestBigKeyAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &bigKeyAttack{}
	state := BigKeyState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration": float64(60000),
			"keySize":  float64(5),
			"numKeys":  float64(2),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, 5*1024*1024, state.KeySize) // 5 MB in bytes
	assert.Equal(t, 2, state.NumKeys)
	assert.Contains(t, state.KeyPrefix, "steadybit-bigkey-")
	assert.Empty(t, state.CreatedKeys)
	assert.WithinDuration(t, time.Now().Add(60*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestBigKeyAttack_Prepare_MinimumValues(t *testing.T) {
	// Given - keySize and numKeys less than 1
	action := &bigKeyAttack{}
	state := BigKeyState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration": float64(30000),
			"keySize":  float64(0),
			"numKeys":  float64(0),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then - should default to 1
	require.NoError(t, err)
	assert.Equal(t, 1*1024*1024, state.KeySize) // 1 MB minimum
	assert.Equal(t, 1, state.NumKeys)           // 1 key minimum
}

func TestBigKeyAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &bigKeyAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, BigKeyState{}, state)
}

func TestGenerateBigValue(t *testing.T) {
	// Given
	size := 100

	// When
	value := generateBigValue(size)

	// Then
	assert.Len(t, value, size)
	// Verify it contains only expected charset characters
	for _, c := range value {
		assert.True(t, (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9'))
	}
}

func TestBigKeyAttack_StartStatusStop(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &bigKeyAttack{}
	executionId := uuid.New().String()[:8]
	state := BigKeyState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		KeyPrefix:   fmt.Sprintf("steadybit-bigkey-%s-", executionId),
		KeySize:     1024, // 1KB for faster test
		NumKeys:     1,
		CreatedKeys: []string{},
		EndTime:     time.Now().Add(2 * time.Second).Unix(),
		ExecutionId: executionId,
	}

	// When - Start
	startResult, err := action.Start(context.Background(), &state)

	// Then - Start
	require.NoError(t, err)
	require.NotNil(t, startResult)

	// Wait a bit for the goroutine to create keys
	time.Sleep(100 * time.Millisecond)

	// When - Status
	statusResult, err := action.Status(context.Background(), &state)

	// Then - Status
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	assert.False(t, statusResult.Completed) // Should not be completed yet

	// When - Stop
	stopResult, err := action.Stop(context.Background(), &state)

	// Then - Stop
	require.NoError(t, err)
	require.NotNil(t, stopResult)
}

func TestBigKeyAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &bigKeyAttack{}
	state := BigKeyState{
		RedisURL:    "redis://nonexistent:6379",
		DB:          0,
		KeyPrefix:   "test-",
		KeySize:     1024,
		NumKeys:     1,
		ExecutionId: "test",
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestNewBigKeyAttack(t *testing.T) {
	// When
	action := NewBigKeyAttack()

	// Then
	require.NotNil(t, action)
}
