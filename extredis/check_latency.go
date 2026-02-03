/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"time"
)

type latencyCheck struct{}

type LatencyCheckState struct {
	RedisURL          string  `json:"redisUrl"`
	Password          string  `json:"password"`
	DB                int     `json:"db"`
	MaxLatencyMs      float64 `json:"maxLatencyMs"`
	EndTime           int64   `json:"endTime"`
	ThresholdExceeded bool    `json:"thresholdExceeded"`
	MaxObservedMs     float64 `json:"maxObservedMs"`
	TotalPings        int     `json:"totalPings"`
	FailedPings       int     `json:"failedPings"`
}

var _ action_kit_sdk.Action[LatencyCheckState] = (*latencyCheck)(nil)
var _ action_kit_sdk.ActionWithStatus[LatencyCheckState] = (*latencyCheck)(nil)

func NewLatencyCheck() action_kit_sdk.Action[LatencyCheckState] {
	return &latencyCheck{}
}

func (a *latencyCheck) NewEmptyState() LatencyCheckState {
	return LatencyCheckState{}
}

func (a *latencyCheck) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.check-latency",
		Label:       "Latency Check",
		Description: "Monitors Redis response latency and fails if threshold is exceeded",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(redisIcon),
		TargetSelection: extutil.Ptr(action_kit_api.TargetSelection{
			TargetType: TargetTypeInstance,
			SelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
				{
					Label:       "by host and port",
					Description: extutil.Ptr("Find Redis instance by host and port"),
					Query:       "redis.host=\"\" AND redis.port=\"\"",
				},
			}),
		}),
		Technology:  extutil.Ptr("Redis"),
		Category:    extutil.Ptr("monitoring"),
		Kind:        action_kit_api.Check,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long to monitor latency"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "maxLatencyMs",
				Label:        "Max Latency (ms)",
				Description:  extutil.Ptr("Maximum allowed response latency in milliseconds"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("100"),
				Required:     extutil.Ptr(true),
			},
		},
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("1s"),
		}),
		Widgets: extutil.Ptr([]action_kit_api.Widget{
			action_kit_api.LineChartWidget{
				Type:  action_kit_api.ComSteadybitWidgetLineChart,
				Title: "Redis Latency",
				Identity: action_kit_api.LineChartWidgetIdentityConfig{
					MetricName: "redis_latency_ms",
					From:       "redis.host",
					Mode:       action_kit_api.ComSteadybitWidgetLineChartIdentityModeSelect,
				},
				Grouping: extutil.Ptr(action_kit_api.LineChartWidgetGroupingConfig{
					ShowSummary: extutil.Ptr(true),
					Groups: []action_kit_api.LineChartWidgetGroup{
						{
							Title: "Under Threshold",
							Color: "success",
							Matcher: action_kit_api.LineChartWidgetGroupMatcherFallback{
								Type: action_kit_api.ComSteadybitWidgetLineChartGroupMatcherFallback,
							},
						},
						{
							Title: "Threshold Violated",
							Color: "warn",
							Matcher: action_kit_api.LineChartWidgetGroupMatcherKeyEqualsValue{
								Type:  action_kit_api.ComSteadybitWidgetLineChartGroupMatcherKeyEqualsValue,
								Key:   "latency_constraint_fulfilled",
								Value: "false",
							},
						},
					},
				}),
				Tooltip: extutil.Ptr(action_kit_api.LineChartWidgetTooltipConfig{
					MetricValueTitle: extutil.Ptr("Latency (ms)"),
					AdditionalContent: []action_kit_api.LineChartWidgetTooltipContent{
						{From: "redis.host", Title: "Host"},
					},
				}),
			},
		}),
	}
}

func (a *latencyCheck) Prepare(ctx context.Context, state *LatencyCheckState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	maxLatencyMs := float64(extutil.ToInt64(request.Config["maxLatencyMs"]))

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.MaxLatencyMs = maxLatencyMs
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ThresholdExceeded = false
	state.MaxObservedMs = 0
	state.TotalPings = 0
	state.FailedPings = 0

	return nil, nil
}

func (a *latencyCheck) Start(ctx context.Context, state *LatencyCheckState) (*action_kit_api.StartResult, error) {
	// Verify connection
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Started monitoring Redis latency (max: %.0fms)", state.MaxLatencyMs),
			},
		}),
	}, nil
}

func (a *latencyCheck) Status(ctx context.Context, state *LatencyCheckState) (*action_kit_api.StatusResult, error) {
	now := time.Now()
	completed := now.Unix() >= state.EndTime

	// Measure latency
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		state.FailedPings++
		return &action_kit_api.StatusResult{
			Completed: completed,
			Error: &action_kit_api.ActionKitError{
				Title:  "Failed to connect to Redis",
				Detail: extutil.Ptr(err.Error()),
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}
	defer client.Close()

	// Measure ping latency
	start := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	_, err = client.Ping(pingCtx).Result()
	cancel()
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	state.TotalPings++

	if err != nil {
		state.FailedPings++
		return &action_kit_api.StatusResult{
			Completed: completed,
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("Ping failed: %v", err),
				},
			}),
		}, nil
	}

	// Track max observed latency
	if latencyMs > state.MaxObservedMs {
		state.MaxObservedMs = latencyMs
	}

	// Check threshold
	thresholdViolation := ""
	if latencyMs > state.MaxLatencyMs {
		state.ThresholdExceeded = true
		thresholdViolation = fmt.Sprintf("Latency %.2fms exceeds threshold %.0fms", latencyMs, state.MaxLatencyMs)
	}

	// Create metrics
	metrics := []action_kit_api.Metric{
		{
			Name: extutil.Ptr("redis_latency_ms"),
			Metric: map[string]string{
				"redis.host":                   state.RedisURL,
				"latency_constraint_fulfilled": fmt.Sprintf("%t", latencyMs <= state.MaxLatencyMs),
			},
			Value:     latencyMs,
			Timestamp: now,
		},
	}

	result := &action_kit_api.StatusResult{
		Completed: completed,
		Metrics:   extutil.Ptr(metrics),
	}

	// Set error if threshold exceeded at end
	if completed && state.ThresholdExceeded {
		result.Error = &action_kit_api.ActionKitError{
			Title:  "Latency threshold exceeded",
			Detail: extutil.Ptr(fmt.Sprintf("Max observed latency: %.2fms (threshold: %.0fms)", state.MaxObservedMs, state.MaxLatencyMs)),
			Status: extutil.Ptr(action_kit_api.Failed),
		}
	} else if thresholdViolation != "" {
		// Add warning message but don't fail yet
		result.Messages = extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Warn),
				Message: thresholdViolation,
			},
		})
	}

	return result, nil
}
