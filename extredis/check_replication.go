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

type replicationLagCheck struct{}

type ReplicationLagCheckState struct {
	RedisURL          string `json:"redisUrl"`
	Password          string `json:"password"`
	DB                int    `json:"db"`
	MaxLagSeconds     int    `json:"maxLagSeconds"`
	RequireLinkUp     bool   `json:"requireLinkUp"`
	EndTime           int64  `json:"endTime"`
	ThresholdExceeded bool   `json:"thresholdExceeded"`
	MaxObservedLag    int    `json:"maxObservedLag"`
	LinkDownDetected  bool   `json:"linkDownDetected"`
}

var _ action_kit_sdk.Action[ReplicationLagCheckState] = (*replicationLagCheck)(nil)
var _ action_kit_sdk.ActionWithStatus[ReplicationLagCheckState] = (*replicationLagCheck)(nil)

func NewReplicationLagCheck() action_kit_sdk.Action[ReplicationLagCheckState] {
	return &replicationLagCheck{}
}

func (a *replicationLagCheck) NewEmptyState() ReplicationLagCheckState {
	return ReplicationLagCheckState{}
}

func (a *replicationLagCheck) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.check-replication",
		Label:       "Replication Lag Check",
		Description: "Monitors Redis replication status and lag for replicas",
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
				Description:  extutil.Ptr("How long to monitor replication"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "maxLagSeconds",
				Label:        "Max Lag (seconds)",
				Description:  extutil.Ptr("Maximum allowed replication lag in seconds"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("10"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "requireLinkUp",
				Label:        "Require Link Up",
				Description:  extutil.Ptr("Fail if master link is down"),
				Type:         action_kit_api.ActionParameterTypeBoolean,
				DefaultValue: extutil.Ptr("true"),
				Required:     extutil.Ptr(false),
			},
		},
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("2s"),
		}),
		Widgets: extutil.Ptr([]action_kit_api.Widget{
			action_kit_api.LineChartWidget{
				Type:  action_kit_api.ComSteadybitWidgetLineChart,
				Title: "Redis Replication Lag",
				Identity: action_kit_api.LineChartWidgetIdentityConfig{
					MetricName: "redis_replication_lag",
					From:       "redis.host",
					Mode:       action_kit_api.ComSteadybitWidgetLineChartIdentityModeSelect,
				},
				Grouping: extutil.Ptr(action_kit_api.LineChartWidgetGroupingConfig{
					ShowSummary: extutil.Ptr(true),
					Groups: []action_kit_api.LineChartWidgetGroup{
						{
							Title: "Link Up",
							Color: "success",
							Matcher: action_kit_api.LineChartWidgetGroupMatcherKeyEqualsValue{
								Type:  action_kit_api.ComSteadybitWidgetLineChartGroupMatcherKeyEqualsValue,
								Key:   "master_link_status",
								Value: "up",
							},
						},
						{
							Title: "Link Down",
							Color: "danger",
							Matcher: action_kit_api.LineChartWidgetGroupMatcherFallback{
								Type: action_kit_api.ComSteadybitWidgetLineChartGroupMatcherFallback,
							},
						},
					},
				}),
				Tooltip: extutil.Ptr(action_kit_api.LineChartWidgetTooltipConfig{
					MetricValueTitle: extutil.Ptr("Lag (seconds)"),
					AdditionalContent: []action_kit_api.LineChartWidgetTooltipContent{
						{From: "redis.host", Title: "Host"},
						{From: "master_link_status", Title: "Link Status"},
					},
				}),
			},
		}),
	}
}

func (a *replicationLagCheck) Prepare(ctx context.Context, state *ReplicationLagCheckState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000
	maxLagSeconds := int(extutil.ToInt64(request.Config["maxLagSeconds"]))
	requireLinkUp := extutil.ToBool(request.Config["requireLinkUp"])

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.MaxLagSeconds = maxLagSeconds
	state.RequireLinkUp = requireLinkUp
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ThresholdExceeded = false
	state.MaxObservedLag = 0
	state.LinkDownDetected = false

	return nil, nil
}

func (a *replicationLagCheck) Start(ctx context.Context, state *ReplicationLagCheckState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Check if this is a replica
	replInfo, err := clients.GetRedisInfo(ctx, client, "replication")
	if err != nil {
		return nil, fmt.Errorf("failed to get replication info: %w", err)
	}

	role := replInfo["role"]
	if role == "master" {
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: "This instance is a master, not a replica. Replication lag monitoring will show connected replicas count instead.",
				},
			}),
		}, nil
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Started monitoring replication lag (max: %ds, require link up: %t)", state.MaxLagSeconds, state.RequireLinkUp),
			},
		}),
	}, nil
}

func (a *replicationLagCheck) Status(ctx context.Context, state *ReplicationLagCheckState) (*action_kit_api.StatusResult, error) {
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

	replInfo, err := clients.GetRedisInfo(ctx, client, "replication")
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: completed,
			Error: &action_kit_api.ActionKitError{
				Title:  "Failed to get replication info",
				Detail: extutil.Ptr(err.Error()),
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}

	role := replInfo["role"]
	var thresholdViolation string
	var metrics []action_kit_api.Metric

	if role == "slave" || role == "replica" {
		// This is a replica - check lag to master
		masterLinkStatus := replInfo["master_link_status"]
		masterLastIOSecondsAgo, _ := strconv.Atoi(replInfo["master_last_io_seconds_ago"])
		masterSyncInProgress := replInfo["master_sync_in_progress"]

		// Track max observed lag
		if masterLastIOSecondsAgo > state.MaxObservedLag {
			state.MaxObservedLag = masterLastIOSecondsAgo
		}

		// Check link status
		if state.RequireLinkUp && masterLinkStatus != "up" {
			state.LinkDownDetected = true
			state.ThresholdExceeded = true
			thresholdViolation = fmt.Sprintf("Master link is %s", masterLinkStatus)
		}

		// Check lag threshold
		if masterLastIOSecondsAgo > state.MaxLagSeconds {
			state.ThresholdExceeded = true
			thresholdViolation = fmt.Sprintf("Replication lag %ds exceeds threshold %ds", masterLastIOSecondsAgo, state.MaxLagSeconds)
		}

		// Check if sync in progress
		if masterSyncInProgress == "1" {
			thresholdViolation = "Master sync in progress"
		}

		metrics = []action_kit_api.Metric{
			{
				Name: extutil.Ptr("redis_replication_lag"),
				Metric: map[string]string{
					"redis.host":         state.RedisURL,
					"role":               role,
					"master_link_status": masterLinkStatus,
				},
				Value:     float64(masterLastIOSecondsAgo),
				Timestamp: now,
			},
		}
	} else {
		// This is a master - report connected replicas
		connectedSlaves, _ := strconv.Atoi(replInfo["connected_slaves"])

		metrics = []action_kit_api.Metric{
			{
				Name: extutil.Ptr("redis_connected_replicas"),
				Metric: map[string]string{
					"redis.host": state.RedisURL,
					"role":       role,
				},
				Value:     float64(connectedSlaves),
				Timestamp: now,
			},
		}
	}

	result := &action_kit_api.StatusResult{
		Completed: completed,
		Metrics:   extutil.Ptr(metrics),
	}

	if completed && state.ThresholdExceeded {
		result.Error = &action_kit_api.ActionKitError{
			Title:  "Replication check failed",
			Detail: extutil.Ptr(thresholdViolation),
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
