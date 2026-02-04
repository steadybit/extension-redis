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

func TestBgsaveAttack_Describe(t *testing.T) {
	// Given
	action := &bgsaveAttack{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.trigger-bgsave", desc.Id)
	assert.Equal(t, "Trigger Background Save", desc.Label)
	assert.Contains(t, desc.Description, "BGSAVE")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Attack, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlInstantaneous, desc.TimeControl)
}

func TestBgsaveAttack_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &bgsaveAttack{}
	state := BgsaveState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config:      map[string]any{},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestBgsaveAttack_Prepare_SetsState(t *testing.T) {
	// Given
	action := &bgsaveAttack{}
	state := BgsaveState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config:      map[string]any{},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
}

func TestBgsaveAttack_NewEmptyState(t *testing.T) {
	// Given
	action := &bgsaveAttack{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, BgsaveState{}, state)
}

func TestBgsaveAttack_Start_ConnectionError(t *testing.T) {
	// Note: miniredis doesn't support LASTSAVE command, so we test connection error case
	// The actual Start functionality would need integration tests with real Redis
	action := &bgsaveAttack{}
	state := BgsaveState{
		RedisURL: "redis://nonexistent:6379",
		DB:       0,
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then - expect connection error
	require.Error(t, err)
}

func TestNewBgsaveAttack(t *testing.T) {
	// When
	action := NewBgsaveAttack()

	// Then
	require.NotNil(t, action)
}
