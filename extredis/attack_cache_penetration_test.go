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

func TestCachePenetrationAttack_Describe(t *testing.T) {
	// Given
	action := &cachePenetrationAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.cache-penetration", desc.Id)
	assert.Equal(t, "Cache Penetration", desc.Label)
	assert.Contains(t, desc.Description, "non-existing keys")
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
	assert.Contains(t, paramNames, "concurrency")

	// Check concurrency has min/max
	for _, p := range desc.Parameters {
		if p.Name == "concurrency" {
			require.NotNil(t, p.MinValue)
			require.NotNil(t, p.MaxValue)
			assert.Equal(t, 1, *p.MinValue)
			assert.Equal(t, 100, *p.MaxValue)
		}
	}
}

func TestCachePenetrationAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":    float64(60000),
			"concurrency": float64(10),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestCachePenetrationAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":    float64(90000),
			"concurrency": float64(20),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, 20, state.Concurrency)
	assert.Contains(t, state.KeyPrefix, "steadybit-penetration-miss-")
	assert.NotEmpty(t, state.AttackKey)
	assert.WithinDuration(t, time.Now().Add(90*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestCachePenetrationAttack_Prepare_DefaultConcurrency(t *testing.T) {
	// Given - concurrency of 0
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":    float64(60000),
			"concurrency": float64(0),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then - should default to 10
	require.NoError(t, err)
	assert.Equal(t, 10, state.Concurrency)
}

func TestCachePenetrationAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &cachePenetrationAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, CachePenetrationState{}, state)
}

func TestCachePenetrationAttack_StartStatusStop(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		Concurrency: 3,
		KeyPrefix:   "steadybit-penetration-miss-test-",
		EndTime:     time.Now().Add(2 * time.Second).Unix(),
		AttackKey:   fmt.Sprintf("redis://%s-test-%d", mr.Addr(), time.Now().UnixNano()),
	}

	// When - Start
	startResult, err := action.Start(context.Background(), &state)

	// Then - Start
	require.NoError(t, err)
	require.NotNil(t, startResult)
	require.NotNil(t, startResult.Messages)

	// Let workers send some requests
	time.Sleep(500 * time.Millisecond)

	// When - Status
	statusResult, err := action.Status(context.Background(), &state)

	// Then - Status
	require.NoError(t, err)
	require.NotNil(t, statusResult)
	assert.False(t, statusResult.Completed)
	require.NotNil(t, statusResult.Messages)
	assert.Contains(t, (*statusResult.Messages)[0].Message, "requests sent")

	// Wait for completion
	time.Sleep(2 * time.Second)

	// When - Stop
	stopResult, err := action.Stop(context.Background(), &state)

	// Then - Stop
	require.NoError(t, err)
	require.NotNil(t, stopResult)
	require.NotNil(t, stopResult.Messages)
	assert.Contains(t, (*stopResult.Messages)[0].Message, "Total requests sent")
}

func TestCachePenetrationAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    "redis://nonexistent:6379",
		DB:          0,
		Concurrency: 5,
		KeyPrefix:   "steadybit-penetration-miss-test-",
		EndTime:     time.Now().Add(60 * time.Second).Unix(),
		AttackKey:   "nonexistent-test",
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestCachePenetrationAttack_Status_Completed(t *testing.T) {
	// Given
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    "redis://localhost:6379",
		DB:          0,
		Concurrency: 5,
		AttackKey:   "completed-test",
		EndTime:     time.Now().Add(-10 * time.Second).Unix(), // Already past
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestCachePenetrationAttack_Stop_NoActiveWorkers(t *testing.T) {
	// Given - stop without start
	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    "redis://localhost:6379",
		DB:          0,
		Concurrency: 5,
		AttackKey:   "no-workers-test",
	}

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then - should not error
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Messages)
	assert.Contains(t, (*result.Messages)[0].Message, "Total requests sent: 0")
}

func TestCachePenetrationAttack_RequestsIncrement(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &cachePenetrationAttack{}
	state := CachePenetrationState{
		RedisURL:    fmt.Sprintf("redis://%s", mr.Addr()),
		DB:          0,
		Concurrency: 2,
		KeyPrefix:   "steadybit-penetration-miss-incr-",
		EndTime:     time.Now().Add(5 * time.Second).Unix(),
		AttackKey:   fmt.Sprintf("redis://%s-incr-%d", mr.Addr(), time.Now().UnixNano()),
	}

	// Start
	_, err = action.Start(context.Background(), &state)
	require.NoError(t, err)

	// Wait for workers to send requests
	time.Sleep(500 * time.Millisecond)

	// Check that requests are being counted
	cachePenetrationMutex.Lock()
	counter := cachePenetrationCounts[state.AttackKey]
	cachePenetrationMutex.Unlock()

	require.NotNil(t, counter)
	assert.Greater(t, counter.Load(), int64(0))

	// Cleanup
	_, _ = action.Stop(context.Background(), &state)
}

func TestNewCachePenetrationAttack(t *testing.T) {
	// When
	action := NewCachePenetrationAttack()

	// Then
	require.NotNil(t, action)
}

func TestCachePenetrationGlobals_Initialized(t *testing.T) {
	// Verify global maps are initialized
	assert.NotNil(t, cachePenetrationCancels)
	assert.NotNil(t, cachePenetrationCounts)
}
