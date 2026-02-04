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

func TestConnectionCountCheck_Describe(t *testing.T) {
	// Given
	action := &connectionCountCheck{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.check-connections", desc.Id)
	assert.Equal(t, "Connection Count Check", desc.Label)
	assert.Contains(t, desc.Description, "connected clients")
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
	require.Len(t, desc.Parameters, 3)

	paramNames := make([]string, len(desc.Parameters))
	for i, p := range desc.Parameters {
		paramNames[i] = p.Name
	}
	assert.Contains(t, paramNames, "duration")
	assert.Contains(t, paramNames, "maxConnectionsPct")
	assert.Contains(t, paramNames, "maxConnections")
}

func TestConnectionCountCheck_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":          float64(60000),
			"maxConnectionsPct": float64(80),
			"maxConnections":    float64(0),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestConnectionCountCheck_Prepare_SetsState(t *testing.T) {
	// Given
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":          float64(90000),
			"maxConnectionsPct": float64(75),
			"maxConnections":    float64(100),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, float64(75), state.MaxConnectionsPct)
	assert.Equal(t, 100, state.MaxConnections)
	assert.False(t, state.ThresholdExceeded)
	assert.Equal(t, 0, state.MaxObserved)
	assert.WithinDuration(t, time.Now().Add(90*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestConnectionCountCheck_Prepare_OnlyPercentageThreshold(t *testing.T) {
	// Given - only percentage threshold set
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":          float64(60000),
			"maxConnectionsPct": float64(90),
			"maxConnections":    float64(0),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, float64(90), state.MaxConnectionsPct)
	assert.Equal(t, 0, state.MaxConnections)
}

func TestConnectionCountCheck_Prepare_OnlyAbsoluteThreshold(t *testing.T) {
	// Given - only absolute threshold set
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":          float64(60000),
			"maxConnectionsPct": float64(0),
			"maxConnections":    float64(500),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, float64(0), state.MaxConnectionsPct)
	assert.Equal(t, 500, state.MaxConnections)
}

func TestConnectionCountCheck_NewEmptyState(t *testing.T) {
	// Given
	action := &connectionCountCheck{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, ConnectionCountCheckState{}, state)
}

func TestConnectionCountCheck_Describe_WidgetConfiguration(t *testing.T) {
	// Given
	action := &connectionCountCheck{}

	// When
	desc := action.Describe()

	// Then - verify widget is a LineChartWidget
	require.NotNil(t, desc.Widgets)
	widgets := *desc.Widgets
	require.Len(t, widgets, 1)

	// Type assert to LineChartWidget
	lineChart, ok := widgets[0].(action_kit_api.LineChartWidget)
	require.True(t, ok, "expected LineChartWidget")

	assert.Equal(t, "Redis Connections", lineChart.Title)
	assert.Equal(t, action_kit_api.ComSteadybitWidgetLineChart, lineChart.Type)
	assert.Equal(t, "redis_connected_clients", lineChart.Identity.MetricName)
	assert.Equal(t, "redis.host", lineChart.Identity.From)
}

func TestParseIntValue(t *testing.T) {
	tests := []struct {
		name     string
		info     map[string]string
		key      string
		expected int
	}{
		{
			name:     "valid integer",
			info:     map[string]string{"connected_clients": "50"},
			key:      "connected_clients",
			expected: 50,
		},
		{
			name:     "zero value",
			info:     map[string]string{"connected_clients": "0"},
			key:      "connected_clients",
			expected: 0,
		},
		{
			name:     "missing key",
			info:     map[string]string{},
			key:      "connected_clients",
			expected: 0,
		},
		{
			name:     "invalid value",
			info:     map[string]string{"connected_clients": "not-a-number"},
			key:      "connected_clients",
			expected: 0,
		},
		{
			name:     "maxclients value",
			info:     map[string]string{"maxclients": "10000"},
			key:      "maxclients",
			expected: 10000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseIntValue(tt.info, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConnectionCountCheck_Start(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		MaxConnectionsPct: 80,
		MaxConnections:    100,
		EndTime:           time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestConnectionCountCheck_Status(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		MaxConnectionsPct: 80,
		MaxConnections:    100,
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

func TestConnectionCountCheck_Start_ConnectionError(t *testing.T) {
	// Given
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          "redis://nonexistent:6379",
		DB:                0,
		MaxConnectionsPct: 80,
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestNewConnectionCountCheck(t *testing.T) {
	// When
	action := NewConnectionCountCheck()

	// Then
	require.NotNil(t, action)
}

func TestConnectionCountCheck_Status_Completed(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          fmt.Sprintf("redis://%s", mr.Addr()),
		DB:                0,
		MaxConnectionsPct: 80,
		MaxConnections:    100,
		EndTime:           time.Now().Add(-1 * time.Second).Unix(), // Already expired
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestConnectionCountCheck_Status_ConnectionError(t *testing.T) {
	// Given
	action := &connectionCountCheck{}
	state := ConnectionCountCheckState{
		RedisURL:          "redis://nonexistent:6379",
		DB:                0,
		MaxConnectionsPct: 80,
		EndTime:           time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then - Status returns result with error field, not Go error
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Error)
}
