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

type cacheHitRateCheck struct{}

type CacheHitRateCheckState struct {
	RedisURL          string  `json:"redisUrl"`
	Password          string  `json:"password"`
	DB                int     `json:"db"`
	MinHitRate        float64 `json:"minHitRate"`
	EndTime           int64   `json:"endTime"`
	ThresholdExceeded bool    `json:"thresholdExceeded"`
	MinObservedRate   float64 `json:"minObservedRate"`
	LastHits          int64   `json:"lastHits"`
	LastMisses        int64   `json:"lastMisses"`
	FirstCheck        bool    `json:"firstCheck"`
}

var _ action_kit_sdk.Action[CacheHitRateCheckState] = (*cacheHitRateCheck)(nil)
var _ action_kit_sdk.ActionWithStatus[CacheHitRateCheckState] = (*cacheHitRateCheck)(nil)

func NewCacheHitRateCheck() action_kit_sdk.Action[CacheHitRateCheckState] {
	return &cacheHitRateCheck{}
}

func (a *cacheHitRateCheck) NewEmptyState() CacheHitRateCheckState {
	return CacheHitRateCheckState{
		MinObservedRate: 100.0,
		FirstCheck:      true,
	}
}

func (a *cacheHitRateCheck) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.check-hitrate",
		Label:       "Cache Hit Rate Check",
		Description: "Monitors Redis cache hit rate and fails if it drops below threshold",
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
				Description:  extutil.Ptr("How long to monitor cache hit rate"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "minHitRate",
				Label:        "Min Hit Rate (%)",
				Description:  extutil.Ptr("Minimum acceptable cache hit rate percentage"),
				Type:         action_kit_api.ActionParameterTypePercentage,
				DefaultValue: extutil.Ptr("80"),
				Required:     extutil.Ptr(true),
			},
		},
		Status: extutil.Ptr(action_kit_api.MutatingEndpointReferenceWithCallInterval{
			CallInterval: extutil.Ptr("2s"),
		}),
		Widgets: extutil.Ptr([]action_kit_api.Widget{
			action_kit_api.LineChartWidget{
				Type:  action_kit_api.ComSteadybitWidgetLineChart,
				Title: "Redis Cache Hit Rate",
				Identity: action_kit_api.LineChartWidgetIdentityConfig{
					MetricName: "redis_cache_hit_rate",
					From:       "redis.host",
					Mode:       action_kit_api.ComSteadybitWidgetLineChartIdentityModeSelect,
				},
				Grouping: extutil.Ptr(action_kit_api.LineChartWidgetGroupingConfig{
					ShowSummary: extutil.Ptr(true),
					Groups: []action_kit_api.LineChartWidgetGroup{
						{
							Title: "Above Threshold",
							Color: "success",
							Matcher: action_kit_api.LineChartWidgetGroupMatcherFallback{
								Type: action_kit_api.ComSteadybitWidgetLineChartGroupMatcherFallback,
							},
						},
						{
							Title: "Below Threshold",
							Color: "warn",
							Matcher: action_kit_api.LineChartWidgetGroupMatcherKeyEqualsValue{
								Type:  action_kit_api.ComSteadybitWidgetLineChartGroupMatcherKeyEqualsValue,
								Key:   "hitrate_constraint_fulfilled",
								Value: "false",
							},
						},
					},
				}),
				Tooltip: extutil.Ptr(action_kit_api.LineChartWidgetTooltipConfig{
					MetricValueTitle: extutil.Ptr("Hit Rate (%)"),
					AdditionalContent: []action_kit_api.LineChartWidgetTooltipContent{
						{From: "redis.host", Title: "Host"},
					},
				}),
			},
		}),
	}
}

func (a *cacheHitRateCheck) Prepare(ctx context.Context, state *CacheHitRateCheckState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000
	minHitRate := float64(extutil.ToInt64(request.Config["minHitRate"]))

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.MinHitRate = minHitRate
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()
	state.ThresholdExceeded = false
	state.MinObservedRate = 100.0
	state.FirstCheck = true

	return nil, nil
}

func (a *cacheHitRateCheck) Start(ctx context.Context, state *CacheHitRateCheckState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Get initial stats
	statsInfo, err := clients.GetRedisInfo(ctx, client, "stats")
	if err != nil {
		return nil, fmt.Errorf("failed to get stats info: %w", err)
	}

	state.LastHits = parseStatsInt64(statsInfo, "keyspace_hits")
	state.LastMisses = parseStatsInt64(statsInfo, "keyspace_misses")

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Started monitoring cache hit rate (min: %.0f%%)", state.MinHitRate),
			},
		}),
	}, nil
}

func (a *cacheHitRateCheck) Status(ctx context.Context, state *CacheHitRateCheckState) (*action_kit_api.StatusResult, error) {
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

	statsInfo, err := clients.GetRedisInfo(ctx, client, "stats")
	if err != nil {
		return &action_kit_api.StatusResult{
			Completed: completed,
			Error: &action_kit_api.ActionKitError{
				Title:  "Failed to get stats info",
				Detail: extutil.Ptr(err.Error()),
				Status: extutil.Ptr(action_kit_api.Failed),
			},
		}, nil
	}

	currentHits := parseStatsInt64(statsInfo, "keyspace_hits")
	currentMisses := parseStatsInt64(statsInfo, "keyspace_misses")

	// Calculate hit rate for the interval (delta-based)
	var hitRate float64
	var intervalHits, intervalMisses int64

	if state.FirstCheck {
		// First check - use cumulative stats
		totalRequests := currentHits + currentMisses
		if totalRequests > 0 {
			hitRate = float64(currentHits) / float64(totalRequests) * 100
		} else {
			hitRate = 100.0 // No requests yet, consider it 100%
		}
		state.FirstCheck = false
	} else {
		// Calculate delta
		intervalHits = currentHits - state.LastHits
		intervalMisses = currentMisses - state.LastMisses
		intervalTotal := intervalHits + intervalMisses

		if intervalTotal > 0 {
			hitRate = float64(intervalHits) / float64(intervalTotal) * 100
		} else {
			hitRate = 100.0 // No requests in interval
		}
	}

	// Update last values
	state.LastHits = currentHits
	state.LastMisses = currentMisses

	// Track minimum observed
	if hitRate < state.MinObservedRate {
		state.MinObservedRate = hitRate
	}

	// Check threshold
	var thresholdViolation string
	if hitRate < state.MinHitRate {
		state.ThresholdExceeded = true
		thresholdViolation = fmt.Sprintf("Cache hit rate %.1f%% is below threshold %.0f%%", hitRate, state.MinHitRate)
	}

	// Create metrics
	metrics := []action_kit_api.Metric{
		{
			Name: extutil.Ptr("redis_cache_hit_rate"),
			Metric: map[string]string{
				"redis.host":                  state.RedisURL,
				"hitrate_constraint_fulfilled": fmt.Sprintf("%t", hitRate >= state.MinHitRate),
			},
			Value:     hitRate,
			Timestamp: now,
		},
		{
			Name: extutil.Ptr("redis_keyspace_hits"),
			Metric: map[string]string{
				"redis.host": state.RedisURL,
			},
			Value:     float64(currentHits),
			Timestamp: now,
		},
		{
			Name: extutil.Ptr("redis_keyspace_misses"),
			Metric: map[string]string{
				"redis.host": state.RedisURL,
			},
			Value:     float64(currentMisses),
			Timestamp: now,
		},
	}

	result := &action_kit_api.StatusResult{
		Completed: completed,
		Metrics:   extutil.Ptr(metrics),
	}

	if completed && state.ThresholdExceeded {
		result.Error = &action_kit_api.ActionKitError{
			Title:  "Cache hit rate below threshold",
			Detail: extutil.Ptr(fmt.Sprintf("Minimum observed hit rate: %.1f%% (threshold: %.0f%%)", state.MinObservedRate, state.MinHitRate)),
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

func parseStatsInt64(info map[string]string, key string) int64 {
	if val, ok := info[key]; ok {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return 0
}
