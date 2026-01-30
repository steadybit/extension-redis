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
