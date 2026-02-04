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

func TestBlockedClientsCheck_Describe(t *testing.T) {
	// Given
	action := &blockedClientsCheck{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.check-blocked-clients", desc.Id)
	assert.Equal(t, "Blocked Clients Check", desc.Label)
	assert.Contains(t, desc.Description, "blocked clients")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Check, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check status endpoint
	require.NotNil(t, desc.Status)
	require.NotNil(t, desc.Status.CallInterval)
	assert.Equal(t, "2s", *desc.Status.CallInterval)

	// Check widgets
	require.NotNil(t, desc.Widgets)
	require.GreaterOrEqual(t, len(*desc.Widgets), 1)

	// Check parameters
	require.NotNil(t, desc.Parameters)
	require.Len(t, desc.Parameters, 2)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "duration")
	assert.Contains(t, paramNames, "maxBlockedClients")
}

func TestBlockedClientsCheck_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":          float64(60000),
			"maxBlockedClients": float64(10),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestBlockedClientsCheck_Prepare_SetsState(t *testing.T) {
	// Given
	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":          float64(120000),
			"maxBlockedClients": float64(5),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, 5, state.MaxBlockedClients)
	assert.False(t, state.ThresholdExceeded)
	assert.Equal(t, 0, state.MaxObserved)
	assert.WithinDuration(t, time.Now().Add(120*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestBlockedClientsCheck_NewEmptyState(t *testing.T) {
	// Given
	action := &blockedClientsCheck{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, BlockedClientsCheckState{}, state)
}

func TestBlockedClientsCheck_Describe_WidgetConfiguration(t *testing.T) {
	// Given
	action := &blockedClientsCheck{}

	// When
	desc := action.Describe()

	// Then - verify widget is a LineChartWidget
	require.NotNil(t, desc.Widgets)
	widgets := *desc.Widgets
	require.Len(t, widgets, 1)

	// Type assert to LineChartWidget
	lineChart, ok := widgets[0].(action_kit_api.LineChartWidget)
	require.True(t, ok, "expected LineChartWidget")

	assert.Equal(t, "Redis Blocked Clients", lineChart.Title)
	assert.Equal(t, action_kit_api.ComSteadybitWidgetLineChart, lineChart.Type)
	assert.Equal(t, "redis_blocked_clients", lineChart.Identity.MetricName)
	assert.Equal(t, "redis.host", lineChart.Identity.From)
}

func TestParseBlockedClientsInt(t *testing.T) {
	tests := []struct {
		name     string
		info     map[string]string
		key      string
		expected int
	}{
		{
			name:     "valid integer",
			info:     map[string]string{"blocked_clients": "5"},
			key:      "blocked_clients",
			expected: 5,
		},
		{
			name:     "zero value",
			info:     map[string]string{"blocked_clients": "0"},
			key:      "blocked_clients",
			expected: 0,
		},
		{
			name:     "missing key",
			info:     map[string]string{},
			key:      "blocked_clients",
			expected: 0,
		},
		{
			name:     "invalid value",
			info:     map[string]string{"blocked_clients": "invalid"},
			key:      "blocked_clients",
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseBlockedClientsInt(tt.info, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBlockedClientsCheck_Start(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		MaxBlockedClients: 10,
		EndTime:           time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestBlockedClientsCheck_Status(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		MaxBlockedClients: 10,
		EndTime:           time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
	require.NotNil(t, result.Metrics)
}

func TestBlockedClientsCheck_Start_ConnectionError(t *testing.T) {
	// Given
	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          "redis://nonexistent:6379",
		DB:                0,
		MaxBlockedClients: 10,
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestNewBlockedClientsCheck(t *testing.T) {
	// When
	action := NewBlockedClientsCheck()

	// Then
	require.NotNil(t, action)
}

func TestBlockedClientsCheck_Status_Completed(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		MaxBlockedClients: 10,
		EndTime:           time.Now().Add(-1 * time.Second).Unix(), // Already expired
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestBlockedClientsCheck_Status_ConnectionError(t *testing.T) {
	// Given
	action := &blockedClientsCheck{}
	state := BlockedClientsCheckState{
		RedisURL:          "redis://nonexistent:6379",
		DB:                0,
		MaxBlockedClients: 10,
		EndTime:           time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then - Status returns result with error field, not Go error
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Error)
}
