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
	"strconv"
	"time"
)

type memoryCheck struct{}

type MemoryCheckState struct {
	RedisURL          string  `json:"redisUrl"`
	Password          string  `json:"password"`
	DB                int     `json:"db"`
	MaxMemoryPercent  float64 `json:"maxMemoryPercent"`
	MaxMemoryBytes    int64   `json:"maxMemoryBytes"`
	EndTime           int64   `json:"endTime"`
	ThresholdExceeded bool    `json:"thresholdExceeded"`
	MaxObserved       int64   `json:"maxObserved"`
}

var _ action_kit_sdk.Action[MemoryCheckState] = (*memoryCheck)(nil)
var _ action_kit_sdk.ActionWithStatus[MemoryCheckState] = (*memoryCheck)(nil)

func NewMemoryCheck() action_kit_sdk.Action[MemoryCheckState] {
	return &memoryCheck{}
}

func (a *memoryCheck) NewEmptyState() MemoryCheckState {
	return MemoryCheckState{}
}

func (a *memoryCheck) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.check-memory",
		Label:       "Memory Usage Check",
		Description: "Monitors Redis memory usage and fails if threshold is exceeded",
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
				Description:  extutil.Ptr("How long to monitor memory usage"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "maxMemoryPercent",
				Label:        "Max Memory Percent",
				Description:  extutil.Ptr("Maximum allowed memory usage as percentage of maxmemory (0 to disable)"),
				Type:         action_kit_api.ActionParameterTypePercentage,
				DefaultValue: extutil.Ptr("80"),
				Required:     extutil.Ptr(false),
			},
			{
				Name:         "maxMemoryBytes",
				Label:        "Max Memory (MB)",
				Description:  extutil.Ptr("Maximum allowed memory usage in MB (0 to disable)"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("0"),
				Required:     extutil.Ptr(false),
				Advanced:     extutil.Ptr(true),
			},
		},
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("2s"),
		}),
		Widgets: extutil.Ptr([]action_kit_api.Widget{
			action_kit_api.LineChartWidget{
				Type:  action_kit_api.ComSteadybitWidgetLineChart,
				Title: "Redis Memory Usage",
				Identity: action_kit_api.LineChartWidgetIdentityConfig{
					MetricName: "redis_memory_used",
					From:       "redis.host",
					Mode:       action_kit_api.ComSteadybitWidgetLineChartIdentityModeSelect,
				},
				Tooltip: extutil.Ptr(action_kit_api.LineChartWidgetTooltipConfig{
					MetricValueTitle: extutil.Ptr("Memory Used"),
					AdditionalContent: []action_kit_api.LineChartWidgetTooltipContent{
						{From: "redis.host", Title: "Host"},
					},
				}),
			},
		}),
	}
}

func (a *memoryCheck) Prepare(ctx context.Context, state *MemoryCheckState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	maxMemoryPercent := float64(extutil.ToInt64(request.Config["maxMemoryPercent"]))
	maxMemoryBytes := extutil.ToInt64(request.Config["maxMemoryBytes"]) * 1024 * 1024 // Convert MB to bytes

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.MaxMemoryPercent = maxMemoryPercent
	state.MaxMemoryBytes = maxMemoryBytes
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ThresholdExceeded = false
	state.MaxObserved = 0

	return nil, nil
}

func (a *memoryCheck) Start(ctx context.Context, state *MemoryCheckState) (*action_kit_api.StartResult, error) {
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
				Message: "Started monitoring Redis memory usage",
			},
		}),
	}, nil
}

func (a *memoryCheck) Status(ctx context.Context, state *MemoryCheckState) (*action_kit_api.StatusResult, error) {
	now := time.Now()
	completed := now.Unix() >= state.EndTime

	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
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

	// Get memory info
	memoryInfo, err := clients.GetRedisInfo(ctx, client, "memory")
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: completed,
			Error: &action_kit_api.ActionKitError{
				Title:  "Failed to get memory info",
				Detail: extutil.Ptr(err.Error()),
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}

	usedMemory := parseMemoryValue(memoryInfo, "used_memory")
	maxMemory := parseMemoryValue(memoryInfo, "maxmemory")

	// Track max observed
	if usedMemory > state.MaxObserved {
		state.MaxObserved = usedMemory
	}

	// Check thresholds
	var thresholdViolation string

	// Check percentage threshold
	if state.MaxMemoryPercent > 0 && maxMemory > 0 {
		usagePercent := float64(usedMemory) / float64(maxMemory) * 100
		if usagePercent > state.MaxMemoryPercent {
			state.ThresholdExceeded = true
			thresholdViolation = fmt.Sprintf("Memory usage %.1f%% exceeds threshold %.1f%%", usagePercent, state.MaxMemoryPercent)
		}
	}

	// Check absolute threshold
	if state.MaxMemoryBytes > 0 && usedMemory > state.MaxMemoryBytes {
		state.ThresholdExceeded = true
		thresholdViolation = fmt.Sprintf("Memory usage %d bytes exceeds threshold %d bytes", usedMemory, state.MaxMemoryBytes)
	}

	// Create metrics
	metrics := []action_kit_api.Metric{
		{
			Name: extutil.Ptr("redis_memory_used"),
			Metric: map[string]string{
				"redis.host": state.RedisURL,
			},
			Value:     float64(usedMemory),
			Timestamp: now,
		},
	}

	if maxMemory > 0 {
		metrics = append(metrics, action_kit_api.Metric{
			Name: extutil.Ptr("redis_memory_max"),
			Metric: map[string]string{
				"redis.host": state.RedisURL,
			},
			Value:     float64(maxMemory),
			Timestamp: now,
		})
	}

	result := &action_kit_api.StatusResult{
		Completed: completed,
		Metrics:   extutil.Ptr(metrics),
	}

	// Set error if threshold exceeded at end
	if completed && state.ThresholdExceeded {
		result.Error = &action_kit_api.ActionKitError{
			Title:  "Memory threshold exceeded",
			Detail: extutil.Ptr(thresholdViolation),
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

func parseMemoryValue(info map[string]string, key string) int64 {
	if val, ok := info[key]; ok {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return 0
}
