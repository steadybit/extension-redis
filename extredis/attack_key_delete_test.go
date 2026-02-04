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

func TestKeyDeleteAttack_Describe(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.database.key-delete", desc.Id)
	assert.Equal(t, "Delete Keys", desc.Label)
	assert.Contains(t, desc.Description, "Deletes keys matching a pattern")
	assert.Equal(t, TargetTypeDatabase, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.GreaterOrEqual(t, len(desc.Parameters), 2)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "pattern")
	assert.Contains(t, paramNames, "maxKeys")
	assert.Contains(t, paramNames, "restoreOnStop")
}

func TestKeyDeleteAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(100),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestKeyDeleteAttack_Prepare_MissingPattern(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"0"},
			},
		},
		Config: map[string]any{
			"pattern":       "",
			"maxKeys":       float64(100),
			"restoreOnStop": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern is required")
}

func TestKeyDeleteAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL:      {"redis://localhost:6379"},
				AttrDatabaseIndex: {"2"},
			},
		},
		Config: map[string]any{
			"pattern":       "cache:*",
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
	assert.Equal(t, 2, state.DB)
	assert.Equal(t, "cache:*", state.Pattern)
	assert.Equal(t, 50, state.MaxKeys)
	assert.True(t, state.RestoreOnStop)
	assert.Empty(t, state.DeletedKeys)
	assert.NotNil(t, state.BackupData)
}

func TestKeyDeleteAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.NotNil(t, state.BackupData)
	assert.Empty(t, state.DeletedKeys)
}

func TestKeyDeleteAttack_Prepare_DefaultDB(t *testing.T) {
	// Given - no database index attribute
	action := &keyDeleteAttack{}
	state := KeyDeleteState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"pattern":       "test:*",
			"maxKeys":       float64(10),
			"restoreOnStop": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 0, state.DB)
	assert.False(t, state.RestoreOnStop)
}

func TestKeyDeleteAttack_StartStatusStop(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	// Create test keys
	mr.Set("test:key1", "value1")
	mr.Set("test:key2", "value2")

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "test:*",
		MaxKeys:       100,
		RestoreOnStop: true,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]string),
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	// When - Start
	startResult, err := action.Start(context.Background(), &state)

	// Then - Start
	require.NoError(t, err)
	require.NotNil(t, startResult)
	assert.Len(t, state.DeletedKeys, 2)

	// When - Status
	statusResult, err := action.Status(context.Background(), &state)

	// Then - Status
	require.NoError(t, err)
	require.NotNil(t, statusResult)

	// When - Stop (restore keys)
	stopResult, err := action.Stop(context.Background(), &state)

	// Then - Stop
	require.NoError(t, err)
	require.NotNil(t, stopResult)

	// Verify keys are restored
	val, err := mr.Get("test:key1")
	require.NoError(t, err)
	assert.Equal(t, "value1", val)
}

func TestKeyDeleteAttack_Start_NoMatchingKeys(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "nonexistent:*",
		MaxKeys:       100,
		RestoreOnStop: false,
		DeletedKeys:   []string{},
		BackupData:    make(map[string]string),
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, state.DeletedKeys)
}

func TestKeyDeleteAttack_Start_ConnectionError(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:    "redis://nonexistent:6379",
		DB:          0,
		Pattern:     "test:*",
		MaxKeys:     100,
		DeletedKeys: []string{},
		BackupData:  make(map[string]string),
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestNewKeyDeleteAttack(t *testing.T) {
	// When
	action := NewKeyDeleteAttack()

	// Then
	require.NotNil(t, action)
}

func TestKeyDeleteAttack_Status_Completed(t *testing.T) {
	// Given
	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:    "redis://localhost:6379",
		DB:          0,
		Pattern:     "test:*",
		DeletedKeys: []string{"key1"},
		EndTime:     time.Now().Add(-1 * time.Second).Unix(), // Already expired
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestKeyDeleteAttack_Stop_NoKeysToRestore(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &keyDeleteAttack{}
	state := KeyDeleteState{
		RedisURL:      fmt.Sprintf("redis://%s", mr.Addr()),
		DB:            0,
		Pattern:       "test:*",
		RestoreOnStop: true,
		DeletedKeys:   []string{}, // No keys to restore
		BackupData:    map[string]string{},
	}

	// When
	result, err := action.Stop(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
}
