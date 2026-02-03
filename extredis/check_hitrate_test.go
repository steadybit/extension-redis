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

func TestCacheHitRateCheck_Describe(t *testing.T) {
	// Given
	action := &cacheHitRateCheck{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.check-hitrate", desc.Id)
	assert.Equal(t, "Cache Hit Rate Check", desc.Label)
	assert.Contains(t, desc.Description, "hit rate")
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
	assert.Contains(t, paramNames, "minHitRate")
}

func TestCacheHitRateCheck_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &cacheHitRateCheck{}
	state := CacheHitRateCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":   float64(60000),
			"minHitRate": float64(80),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestCacheHitRateCheck_Prepare_SetsState(t *testing.T) {
	// Given
	action := &cacheHitRateCheck{}
	state := CacheHitRateCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":   float64(120000),
			"minHitRate": float64(90),
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, float64(90), state.MinHitRate)
	assert.False(t, state.ThresholdExceeded)
	assert.Equal(t, float64(100), state.MinObservedRate)
	assert.True(t, state.FirstCheck)
	assert.WithinDuration(t, time.Now().Add(120*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestCacheHitRateCheck_NewEmptyState(t *testing.T) {
	// Given
	action := &cacheHitRateCheck{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, float64(100), state.MinObservedRate)
	assert.True(t, state.FirstCheck)
}

func TestCacheHitRateCheck_Describe_WidgetConfiguration(t *testing.T) {
	// Given
	action := &cacheHitRateCheck{}

	// When
	desc := action.Describe()

	// Then - verify widget is a LineChartWidget
	require.NotNil(t, desc.Widgets)
	widgets := *desc.Widgets
	require.Len(t, widgets, 1)

	// Type assert to LineChartWidget
	lineChart, ok := widgets[0].(action_kit_api.LineChartWidget)
	require.True(t, ok, "expected LineChartWidget")

	assert.Equal(t, "Redis Cache Hit Rate", lineChart.Title)
	assert.Equal(t, action_kit_api.ComSteadybitWidgetLineChart, lineChart.Type)
	assert.Equal(t, "redis_cache_hit_rate", lineChart.Identity.MetricName)
	assert.Equal(t, "redis.host", lineChart.Identity.From)

	// Check grouping
	require.NotNil(t, lineChart.Grouping)
	require.NotNil(t, lineChart.Grouping.ShowSummary)
	assert.True(t, *lineChart.Grouping.ShowSummary)
	require.Len(t, lineChart.Grouping.Groups, 2)
	assert.Equal(t, "Above Threshold", lineChart.Grouping.Groups[0].Title)
	assert.Equal(t, "Below Threshold", lineChart.Grouping.Groups[1].Title)
}

func TestParseStatsInt64(t *testing.T) {
	tests := []struct {
		name     string
		info     map[string]string
		key      string
		expected int64
	}{
		{
			name:     "valid keyspace hits",
			info:     map[string]string{"keyspace_hits": "1000000"},
			key:      "keyspace_hits",
			expected: 1000000,
		},
		{
			name:     "valid keyspace misses",
			info:     map[string]string{"keyspace_misses": "50000"},
			key:      "keyspace_misses",
			expected: 50000,
		},
		{
			name:     "zero value",
			info:     map[string]string{"keyspace_hits": "0"},
			key:      "keyspace_hits",
			expected: 0,
		},
		{
			name:     "missing key",
			info:     map[string]string{},
			key:      "keyspace_hits",
			expected: 0,
		},
		{
			name:     "invalid value",
			info:     map[string]string{"keyspace_hits": "invalid"},
			key:      "keyspace_hits",
			expected: 0,
		},
		{
			name:     "large number",
			info:     map[string]string{"keyspace_hits": "9223372036854775807"},
			key:      "keyspace_hits",
			expected: 9223372036854775807,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseStatsInt64(tt.info, tt.key)
			assert.Equal(t, tt.expected, result)
		})
	}
}
