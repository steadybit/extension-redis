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

func TestReplicationLagCheck_Describe(t *testing.T) {
	// Given
	action := &replicationLagCheck{}

	// When
	desc := action.Describe()

	// Then
	assert.Equal(t, "com.steadybit.extension_redis.instance.check-replication", desc.Id)
	assert.Equal(t, "Replication Lag Check", desc.Label)
	assert.Contains(t, desc.Description, "replication")
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
	assert.Contains(t, paramNames, "maxLagSeconds")
	assert.Contains(t, paramNames, "requireLinkUp")
}

func TestReplicationLagCheck_Prepare_MissingURL(t *testing.T) {
	// Given
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"maxLagSeconds": float64(10),
			"requireLinkUp": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis URL not found")
}

func TestReplicationLagCheck_Prepare_SetsState(t *testing.T) {
	// Given
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":      float64(90000),
			"maxLagSeconds": float64(5),
			"requireLinkUp": true,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, "redis://localhost:6379", state.RedisURL)
	assert.Equal(t, 0, state.DB)
	assert.Equal(t, 5, state.MaxLagSeconds)
	assert.True(t, state.RequireLinkUp)
	assert.False(t, state.ThresholdExceeded)
	assert.Equal(t, 0, state.MaxObservedLag)
	assert.False(t, state.LinkDownDetected)
	assert.WithinDuration(t, time.Now().Add(90*time.Second), time.Unix(state.EndTime, 0), 2*time.Second)
}

func TestReplicationLagCheck_Prepare_LinkUpNotRequired(t *testing.T) {
	// Given
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{}
	req := extutil.JsonMangle(action_kit_api.PrepareActionRequestBody{
		Target: &action_kit_api.Target{
			Attributes: map[string][]string{
				AttrRedisURL: {"redis://localhost:6379"},
			},
		},
		Config: map[string]any{
			"duration":      float64(60000),
			"maxLagSeconds": float64(30),
			"requireLinkUp": false,
		},
		ExecutionId: uuid.New(),
	})

	// When
	_, err := action.Prepare(context.Background(), &state, req)

	// Then
	require.NoError(t, err)
	assert.Equal(t, 30, state.MaxLagSeconds)
	assert.False(t, state.RequireLinkUp)
}

func TestReplicationLagCheck_NewEmptyState(t *testing.T) {
	// Given
	action := &replicationLagCheck{}

	// When
	state := action.NewEmptyState()

	// Then
	assert.Equal(t, ReplicationLagCheckState{}, state)
}

func TestReplicationLagCheck_Describe_WidgetConfiguration(t *testing.T) {
	// Given
	action := &replicationLagCheck{}

	// When
	desc := action.Describe()

	// Then - verify widget is a LineChartWidget
	require.NotNil(t, desc.Widgets)
	widgets := *desc.Widgets
	require.Len(t, widgets, 1)

	// Type assert to LineChartWidget
	lineChart, ok := widgets[0].(action_kit_api.LineChartWidget)
	require.True(t, ok, "expected LineChartWidget")

	assert.Equal(t, "Redis Replication Lag", lineChart.Title)
	assert.Equal(t, action_kit_api.ComSteadybitWidgetLineChart, lineChart.Type)
	assert.Equal(t, "redis_replication_lag", lineChart.Identity.MetricName)
	assert.Equal(t, "redis.host", lineChart.Identity.From)

	// Check grouping
	require.NotNil(t, lineChart.Grouping)
	require.NotNil(t, lineChart.Grouping.ShowSummary)
	assert.True(t, *lineChart.Grouping.ShowSummary)
	require.Len(t, lineChart.Grouping.Groups, 2)
	assert.Equal(t, "Link Up", lineChart.Grouping.Groups[0].Title)
	assert.Equal(t, "Link Down", lineChart.Grouping.Groups[1].Title)
}

func TestReplicationLagCheck_Start_ConnectionError(t *testing.T) {
	// Given - miniredis doesn't support INFO replication, so we test connection error
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{
		RedisURL:      "redis://nonexistent:6379",
		DB:            0,
		MaxLagSeconds: 10,
	}

	// When
	_, err := action.Start(context.Background(), &state)

	// Then
	require.Error(t, err)
}

func TestReplicationLagCheck_Status_ConnectionError(t *testing.T) {
	// Given
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{
		RedisURL:      "redis://nonexistent:6379",
		DB:            0,
		MaxLagSeconds: 10,
		RequireLinkUp: true,
		EndTime:       time.Now().Add(60 * time.Second).Unix(),
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then - Status returns result with error field, not Go error
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Error)
}

func TestNewReplicationLagCheck(t *testing.T) {
	// When
	action := NewReplicationLagCheck()

	// Then
	require.NotNil(t, action)
}

func TestReplicationLagCheck_Status_Completed(t *testing.T) {
	// Given
	action := &replicationLagCheck{}
	state := ReplicationLagCheckState{
		RedisURL:      "redis://nonexistent:6379",
		DB:            0,
		MaxLagSeconds: 10,
		RequireLinkUp: true,
		EndTime:       time.Now().Add(-1 * time.Second).Unix(), // Already expired
	}

	// When
	result, err := action.Status(context.Background(), &state)

	// Then
	require.NoError(t, err)
	require.NotNil(t, result)
	// Even with connection error, should report as completed since EndTime has passed
}
