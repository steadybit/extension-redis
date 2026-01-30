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

func TestMemoryCheck_Describe(t *testing.T) {
	// Given
	action := &memoryCheck{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.check-memory", desc.Id)
	assert.Equal(t, "Memory Usage Check", desc.Label)
	assert.Contains(t, desc.Description, "Monitors Redis memory usage")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Check, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check status endpoint
	require.NotNil(t, desc.Status)
	require.NotNil(t, desc.Status.CallInterval)

	// Check widgets
	require.NotNil(t, desc.Widgets)
	require.GreaterOrEqual(t, len(*desc.Widgets), 1)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "duration")
	assert.Contains(t, paramNames, "maxMemoryPercent")
	assert.Contains(t, paramNames, "maxMemoryBytes")
}

func TestMemoryCheck_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &memoryCheck{}
	state := MemoryCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":         float64(60000),
			"maxMemoryPercent": float64(80),
			"maxMemoryBytes":   float64(0),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestMemoryCheck_Prepare_SetsState(t *testing.T) {
	// Given
	action := &memoryCheck{}
	state := MemoryCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":         float64(30000),
			"maxMemoryPercent": float64(75),
			"maxMemoryBytes":   float64(100), // 100 MB
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, float64(75), state.MaxMemoryPercent)
	assert.Equal(t, int64(100*1024*1024), state.MaxMemoryBytes) // Converted to bytes
	assert.False(t, state.ThresholdExceeded)
	assert.Equal(t, int64(0), state.MaxObserved)
	assert.WithinDuration(t, time.Now().Add(30*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestMemoryCheck_NewEmptyState(t *testing.T) {
	// Given
	action := &memoryCheck{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, MemoryCheckState{}, state)
}

func TestParseMemoryValue_ValidValues(t *testing.T) {
	tests := []struct {
		name string
		info map[string]string
		key  string
		want int64
	}{
		{
			name: "valid integer",
			info: map[string]string{"used_memory": "1048576"},
			key:  "used_memory",
			want: 1048576,
		},
		{
			name: "zero",
			info: map[string]string{"maxmemory": "0"},
			key:  "maxmemory",
			want: 0,
		},
		{
			name: "large value",
			info: map[string]string{"used_memory": "1073741824"},
			key:  "used_memory",
			want: 1073741824,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMemoryValue(tt.info, tt.key)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseMemoryValue_InvalidValues(t *testing.T) {
	tests := []struct {
		name string
		info map[string]string
		key  string
	}{
		{
			name: "key not found",
			info: map[string]string{"other": "123"},
			key:  "used_memory",
		},
		{
			name: "invalid format",
			info: map[string]string{"used_memory": "not-a-number"},
			key:  "used_memory",
		},
		{
			name: "empty map",
			info: map[string]string{},
			key:  "used_memory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMemoryValue(tt.info, tt.key)
			assert.Equal(t, int64(0), got)
		})
	}
}
