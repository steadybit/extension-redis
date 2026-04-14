/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type connectionCountCheck struct{}

type ConnectionCountCheckState struct {
	RedisURL          string  `json:"redisUrl"`
	Password          string  `json:"password"`
	DB                int     `json:"db"`
	MaxConnections    int     `json:"maxConnections"`
	MaxConnectionsPct float64 `json:"maxConnectionsPct"`
	EndTime           int64   `json:"endTime"`
	ThresholdExceeded bool    `json:"thresholdExceeded"`
	MaxObserved       int     `json:"maxObserved"`
}

var _ action_kit_sdk.Action[ConnectionCountCheckState] = (*connectionCountCheck)(nil)
var _ action_kit_sdk.ActionWithStatus[ConnectionCountCheckState] = (*connectionCountCheck)(nil)

func NewConnectionCountCheck() action_kit_sdk.Action[ConnectionCountCheckState] {
	return &connectionCountCheck{}
}

func (a *connectionCountCheck) NewEmptyState() ConnectionCountCheckState {
	return ConnectionCountCheckState{}
}

func (a *connectionCountCheck) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.check-connections",
		Label:       "Connection Count Check",
		Description: "Monitors Redis connected clients and fails if threshold is exceeded",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        new(redisIcon),
		TargetSelection: new(action_kit_api.TargetSelection{
			TargetType: TargetTypeInstance,
			SelectionTemplates: new([]action_kit_api.TargetSelectionTemplate{
				{
					Label:       "by host and port",
					Description: new("Find Redis instance by host and port"),
					Query:       "redis.host=\"\" AND redis.port=\"\"",
				},
			}),
		}),
		Technology:  new("Redis"),
		Category:    new("monitoring"),
		Kind:        action_kit_api.Check,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  new("How long to monitor connection count"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: new("60s"),
				Required:     new(true),
			},
			{
				Name:         "maxConnectionsPct",
				Label:        "Max Connections (%)",
				Description:  new("Maximum allowed connections as percentage of maxclients (0 to disable)"),
				Type:         action_kit_api.ActionParameterTypePercentage,
				DefaultValue: new("80"),
				Required:     new(false),
			},
			{
				Name:         "maxConnections",
				Label:        "Max Connections (absolute)",
				Description:  new("Maximum allowed number of connections (0 to disable)"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: new("0"),
				Required:     new(false),
				Advanced:     new(true),
			},
		},
		Status: new(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: new("2s"),
		}),
		Widgets: new([]action_kit_api.Widget{
			action_kit_api.LineChartWidget{
				Type:  action_kit_api.ComSteadybitWidgetLineChart,
				Title: "Redis Connections",
				Identity: action_kit_api.LineChartWidgetIdentityConfig{
					MetricName: "redis_connected_clients",
					From:       "redis.host",
					Mode:       action_kit_api.ComSteadybitWidgetLineChartIdentityModeSelect,
				},
				Tooltip: new(action_kit_api.LineChartWidgetTooltipConfig{
					MetricValueTitle: new("Connected Clients"),
					AdditionalContent: []action_kit_api.LineChartWidgetTooltipContent{
						{From: "redis.host", Title: "Host"},
					},
				}),
			},
		}),
	}
}

func (a *connectionCountCheck) Prepare(ctx context.Context, state *ConnectionCountCheckState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000
	maxConnections := int(extutil.ToInt64(request.Config["maxConnections"]))
	maxConnectionsPct := float64(extutil.ToInt64(request.Config["maxConnectionsPct"]))

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.MaxConnections = maxConnections
	state.MaxConnectionsPct = maxConnectionsPct
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ThresholdExceeded = false
	state.MaxObserved = 0

	return nil, nil
}

func (a *connectionCountCheck) Start(ctx context.Context, state *ConnectionCountCheckState) (*action_kit_api.StartResult, error) {
	client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	return &action_kit_api.StartResult{
		Messages: new([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: "Started monitoring Redis connection count",
			},
		}),
	}, nil
}

func (a *connectionCountCheck) Status(ctx context.Context, state *ConnectionCountCheckState) (*action_kit_api.StatusResult, error) {
	now := time.Now()
	completed := now.Unix() >= state.EndTime

	client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: completed,
			Error: &action_kit_api.ActionKitError{
				Title:  "Failed to connect to Redis",
				Detail: new(err.Error()),
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}

	// Get clients info
	clientsInfo, err := clients.GetRedisInfo(ctx, client, "clients")
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: completed,
			Error: &action_kit_api.ActionKitError{
				Title:  "Failed to get clients info",
				Detail: new(err.Error()),
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}

	connectedClients := parseIntValue(clientsInfo, "connected_clients")
	maxClients := parseIntValue(clientsInfo, "maxclients")

	// Track max observed
	if connectedClients > state.MaxObserved {
		state.MaxObserved = connectedClients
	}

	// Check thresholds
	var thresholdViolation string

	// Check percentage threshold
	if state.MaxConnectionsPct > 0 && maxClients > 0 {
		usagePct := float64(connectedClients) / float64(maxClients) * 100
		if usagePct > state.MaxConnectionsPct {
			state.ThresholdExceeded = true
			thresholdViolation = fmt.Sprintf("Connection usage %.1f%% exceeds threshold %.1f%%", usagePct, state.MaxConnectionsPct)
		}
	}

	// Check absolute threshold
	if state.MaxConnections > 0 && connectedClients > state.MaxConnections {
		state.ThresholdExceeded = true
		thresholdViolation = fmt.Sprintf("Connected clients %d exceeds threshold %d", connectedClients, state.MaxConnections)
	}

	// Create metrics
	metrics := []action_kit_api.Metric{
		{
			Name: new("redis_connected_clients"),
			Metric: map[string]string{
				"redis.host": state.RedisURL,
			},
			Value:     float64(connectedClients),
			Timestamp: now,
		},
	}

	if maxClients > 0 {
		metrics = append(metrics, action_kit_api.Metric{
			Name: new("redis_max_clients"),
			Metric: map[string]string{
				"redis.host": state.RedisURL,
			},
			Value:     float64(maxClients),
			Timestamp: now,
		})
	}

	result := &action_kit_api.StatusResult{
		Completed: completed,
		Metrics:   new(metrics),
	}

	if completed && state.ThresholdExceeded {
		result.Error = &action_kit_api.ActionKitError{
			Title:  "Connection threshold exceeded",
			Detail: new(thresholdViolation),
			Status: extutil.Ptr(action_kit_api.Failed),
		}
	} else if thresholdViolation != "" {
		result.Messages = new([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Warn),
				Message: thresholdViolation,
			},
		})
	}

	return result, nil
}

func parseIntValue(info map[string]string, key string) int {
	if val, ok := info[key]; ok {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return 0
}
