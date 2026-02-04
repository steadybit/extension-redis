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

func TestLatencyCheck_Describe(t *testing.T) {
	// Given
	action := &latencyCheck{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.check-latency", desc.Id)
	assert.Equal(t, "Latency Check", desc.Label)
	assert.Contains(t, desc.Description, "Monitors Redis response latency")
	assert.Equal(t, TargetTypeInstance, desc.TargetSelection.TargetType)
	assert.Equal(t, action_kit_api.Check, desc.Kind)
	assert.Equal(t, action_kit_api.TimeControlExternal, desc.TimeControl)

	// Check status endpoint
	require.NotNil(t, desc.Status)
	require.NotNil(t, desc.Status.CallInterval)
	assert.Equal(t, "1s", *desc.Status.CallInterval)

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
	assert.Contains(t, paramNames, "maxLatencyMs")
}

func TestLatencyCheck_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &latencyCheck{}
	state := LatencyCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":     float64(60000),
			"maxLatencyMs": float64(100),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestLatencyCheck_Prepare_SetsState(t *testing.T) {
	// Given
	action := &latencyCheck{}
	state := LatencyCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":     float64(30000),
			"maxLatencyMs": float64(50),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, float64(50), state.MaxLatencyMs)
	assert.False(t, state.ThresholdExceeded)
	assert.Equal(t, float64(0), state.MaxObservedMs)
	assert.Equal(t, 0, state.TotalPings)
	assert.Equal(t, 0, state.FailedPings)
	assert.WithinDuration(t, time.Now().Add(30*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestLatencyCheck_NewEmptyState(t *testing.T) {
	// Given
	action := &latencyCheck{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, LatencyCheckState{}, state)
}

func TestLatencyCheck_Describe_WidgetConfiguration(t *testing.T) {
	// Given
	action := &latencyCheck{}

	// When
	desc := action.Describe()

	// Then - verify widget is a LineChartWidget
	require.NotNil(t, desc.Widgets)
	widgets := *desc.Widgets
	require.Len(t, widgets, 1)

	// Type assert to LineChartWidget
	lineChart, ok := widgets[0].(action_kit_api.LineChartWidget)
	require.True(t, ok, "expected LineChartWidget")

	assert.Equal(t, "Redis Latency", lineChart.Title)
	assert.Equal(t, action_kit_api.ComSteadybitWidgetLineChart, lineChart.Type)
	assert.Equal(t, "redis_latency_ms", lineChart.Identity.MetricName)
	assert.Equal(t, "redis.host", lineChart.Identity.From)

	// Check grouping
	require.NotNil(t, lineChart.Grouping)
	require.NotNil(t, lineChart.Grouping.ShowSummary)
	assert.True(t, *lineChart.Grouping.ShowSummary)
	require.Len(t, lineChart.Grouping.Groups, 2)
	assert.Equal(t, "Under Threshold", lineChart.Grouping.Groups[0].Title)
	assert.Equal(t, "Threshold Violated", lineChart.Grouping.Groups[1].Title)
}

func TestLatencyCheck_Start(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		MaxLatencyMs: 100,
		EndTime:      time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestLatencyCheck_Status(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		MaxLatencyMs: 100,
		EndTime:      time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
	require.NotNil(t, result.Metrics)
}

func TestLatencyCheck_Start_ConnectionError(t *testing.T) {
	// Given
	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     "redis://nonexistent:6379",
		MaxLatencyMs: 100,
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestNewLatencyCheck(t *testing.T) {
	// When
	action := NewLatencyCheck()

	// Then
	require.NotNil(t, action)
}

func TestLatencyCheck_Status_Completed(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     fmt.Sprintf("redis://%s", mr.Addr()),
		MaxLatencyMs: 100,
		EndTime:      time.Now().Add(-1 * time.Second).Unix(), // Already expired
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestLatencyCheck_Status_ConnectionError(t *testing.T) {
	// Given
	action := &latencyCheck{}
	state := LatencyCheckState{
		RedisURL:     "redis://nonexistent:6379",
		MaxLatencyMs: 100,
		EndTime:      time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then - Status may return either error field or warning message
	require.NoError(t, err)
	require.NotNil(t, result)
	// The connection will fail on ping, resulting in either Error or Messages
	assert.True(t, result.Error != nil || result.Messages != nil)
}
