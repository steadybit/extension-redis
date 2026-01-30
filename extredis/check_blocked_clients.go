/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
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

type blockedClientsCheck struct{}

type BlockedClientsCheckState struct {
	RedisURL          string `json:"redisUrl"`
	Password          string `json:"password"`
	DB                int    `json:"db"`
	MaxBlockedClients int    `json:"maxBlockedClients"`
	EndTime           int64  `json:"endTime"`
	ThresholdExceeded bool   `json:"thresholdExceeded"`
	MaxObserved       int    `json:"maxObserved"`
}

var _ action_kit_sdk.Action[BlockedClientsCheckState] = (*blockedClientsCheck)(nil)
var _ action_kit_sdk.ActionWithStatus[BlockedClientsCheckState] = (*blockedClientsCheck)(nil)

func NewBlockedClientsCheck() action_kit_sdk.Action[BlockedClientsCheckState] {
	return &blockedClientsCheck{}
}

func (a *blockedClientsCheck) NewEmptyState() BlockedClientsCheckState {
	return BlockedClientsCheckState{}
}

func (a *blockedClientsCheck) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.check-blocked-clients",
		Label:       "Blocked Clients Check",
		Description: "Monitors Redis blocked clients (waiting on BLPOP, BRPOP, etc.) and fails if threshold is exceeded",
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
				Description:  extutil.Ptr("How long to monitor blocked clients"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "maxBlockedClients",
				Label:        "Max Blocked Clients",
				Description:  extutil.Ptr("Maximum allowed number of blocked clients"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("10"),
				Required:     extutil.Ptr(true),
			},
		},
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("2s"),
		}),
		Widgets: extutil.Ptr([]action_kit_api.Widget{
			action_kit_api.LineChartWidget{
				Type:  action_kit_api.ComSteadybitWidgetLineChart,
				Title: "Redis Blocked Clients",
				Identity: action_kit_api.LineChartWidgetIdentityConfig{
					MetricName: "redis_blocked_clients",
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
							Title: "Threshold Exceeded",
							Color: "warn",
							Matcher: action_kit_api.LineChartWidgetGroupMatcherKeyEqualsValue{
								Type:  action_kit_api.ComSteadybitWidgetLineChartGroupMatcherKeyEqualsValue,
								Key:   "blocked_constraint_fulfilled",
								Value: "false",
							},
						},
					},
				}),
				Tooltip: extutil.Ptr(action_kit_api.LineChartWidgetTooltipConfig{
					MetricValueTitle: extutil.Ptr("Blocked Clients"),
					AdditionalContent: []action_kit_api.LineChartWidgetTooltipContent{
						{From: "redis.host", Title: "Host"},
					},
				}),
			},
		}),
	}
}

func (a *blockedClientsCheck) Prepare(ctx context.Context, state *BlockedClientsCheckState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000
	maxBlockedClients := int(extutil.ToInt64(request.Config["maxBlockedClients"]))

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.MaxBlockedClients = maxBlockedClients
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ThresholdExceeded = false
	state.MaxObserved = 0

	return nil, nil
}

func (a *blockedClientsCheck) Start(ctx context.Context, state *BlockedClientsCheckState) (*action_kit_api.StartResult, error) {
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
				Message: fmt.Sprintf("Started monitoring blocked clients (max: %d)", state.MaxBlockedClients),
			},
		}),
	}, nil
}

func (a *blockedClientsCheck) Status(ctx context.Context, state *BlockedClientsCheckState) (*action_kit_api.StatusResult, error) {
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

	clientsInfo, err := clients.GetRedisInfo(ctx, client, "clients")
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: completed,
			Error: &action_kit_api.ActionKitError{
				Title:  "Failed to get clients info",
				Detail: extutil.Ptr(err.Error()),
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}

	blockedClients := parseBlockedClientsInt(clientsInfo, "blocked_clients")

	// Track max observed
	if blockedClients > state.MaxObserved {
		state.MaxObserved = blockedClients
	}

	// Check threshold
	var thresholdViolation string
	if blockedClients > state.MaxBlockedClients {
		state.ThresholdExceeded = true
		thresholdViolation = fmt.Sprintf("Blocked clients %d exceeds threshold %d", blockedClients, state.MaxBlockedClients)
	}

	// Create metrics
	metrics := []action_kit_api.Metric{
		{
			Name: extutil.Ptr("redis_blocked_clients"),
			Metric: map[string]string{
				"redis.host":                 state.RedisURL,
				"blocked_constraint_fulfilled": fmt.Sprintf("%t", blockedClients <= state.MaxBlockedClients),
			},
			Value:     float64(blockedClients),
			Timestamp: now,
		},
	}

	result := &action_kit_api.StatusResult{
		Completed: completed,
		Metrics:   extutil.Ptr(metrics),
	}

	if completed && state.ThresholdExceeded {
		result.Error = &action_kit_api.ActionKitError{
			Title:  "Blocked clients threshold exceeded",
			Detail: extutil.Ptr(fmt.Sprintf("Max observed blocked clients: %d (threshold: %d)", state.MaxObserved, state.MaxBlockedClients)),
			Status: extutil.Ptr(action_kit_api.Failed),
		}
	} else if thresholdViolation != "" {
		result.Messages = extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Warn),
				Message: thresholdViolation,
			},
		})
	}

	return result, nil
}

func parseBlockedClientsInt(info map[string]string, key string) int {
	if val, ok := info[key]; ok {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return 0
}
