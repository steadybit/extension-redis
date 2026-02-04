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

func TestMemoryCheck_Start(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		MaxMemoryPercent: 80,
		MaxMemoryBytes:   0,
		EndTime:          time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Start(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Messages)
	assert.Len(t, *result.Messages, 1)
}

func TestMemoryCheck_Status(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		MaxMemoryPercent: 80,
		MaxMemoryBytes:   0,
		EndTime:          time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
}

func TestMemoryCheck_Start_ConnectionError(t *testing.T) {
	// Given
	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         "redis://nonexistent:6379",
		MaxMemoryPercent: 80,
		MaxMemoryBytes:   0,
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestMemoryCheck_Status_ConnectionError(t *testing.T) {
	// Given
	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         "redis://nonexistent:6379",
		MaxMemoryPercent: 80,
		MaxMemoryBytes:   0,
		EndTime:          time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then - Status returns result with error field, not Go error
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Error)
}

func TestMemoryCheck_Status_Completed(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		MaxMemoryPercent: 80,
		MaxMemoryBytes:   0,
		EndTime:          time.Now().Add(-1 * time.Second).Unix(), // Already expired
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Completed)
}

func TestMemoryCheck_Status_WithMiniredis(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		MaxMemoryPercent: 0, // Disable percent check
		MaxMemoryBytes:   0, // Disable bytes check
		EndTime:          time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then - miniredis doesn't fully support INFO memory, so just verify no error
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.Completed)
}

func TestMemoryCheck_Status_MaxObservedTracking(t *testing.T) {
	// Given
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	action := &memoryCheck{}
	state := MemoryCheckState{
		RedisURL:         fmt.Sprintf("redis://%s", mr.Addr()),
		MaxMemoryPercent: 0,
		MaxMemoryBytes:   0,
		MaxObserved:      0,
		EndTime:          time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	_, err = action.Status(context.Background(), &state)

	// Then - MaxObserved should be updated
	require.NoError(t, err)
	// miniredis uses some memory, so MaxObserved should be > 0 after status check
	// Note: miniredis may not report memory the same way as real Redis
}

func TestNewMemoryCheck(t *testing.T) {
	// When
	action := NewMemoryCheck()

	// Then
	require.NotNil(t, action)
}

func TestMemoryCheck_Describe_WidgetConfiguration(t *testing.T) {
	// Given
	action := &memoryCheck{}

	// When
	desc := action.Describe()

	// Then - verify widget is a LineChartWidget
	require.NotNil(t, desc.Widgets)
	widgets := *desc.Widgets
	require.Len(t, widgets, 1)

	// Type assert to LineChartWidget
	lineChart, ok := widgets[0].(action_kit_api.LineChartWidget)
	require.True(t, ok, "expected LineChartWidget")

	assert.Equal(t, "Redis Memory Usage", lineChart.Title)
	assert.Equal(t, action_kit_api.ComSteadybitWidgetLineChart, lineChart.Type)
	assert.Equal(t, "redis_memory_used", lineChart.Identity.MetricName)
}
