/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type maxmemoryLimitAttack struct{}

type MaxmemoryLimitState struct {
	RedisURL          string `json:"redisUrl"`
	Password          string `json:"password"`
	DB                int    `json:"db"`
	OriginalMaxmemory string `json:"originalMaxmemory"`
	OriginalPolicy    string `json:"originalPolicy"`
	NewMaxmemory      string `json:"newMaxmemory"`
	NewPolicy         string `json:"newPolicy"`
	EndTime           int64  `json:"endTime"`
}

var _ action_kit_sdk.Action[MaxmemoryLimitState] = (*maxmemoryLimitAttack)(nil)
var _ action_kit_sdk.ActionWithStop[MaxmemoryLimitState] = (*maxmemoryLimitAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[MaxmemoryLimitState] = (*maxmemoryLimitAttack)(nil)

func NewMaxmemoryLimitAttack() action_kit_sdk.Action[MaxmemoryLimitState] {
	return &maxmemoryLimitAttack{}
}

func (a *maxmemoryLimitAttack) NewEmptyState() MaxmemoryLimitState {
	return MaxmemoryLimitState{}
}

func (a *maxmemoryLimitAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.maxmemory-limit",
		Label:       "Limit MaxMemory",
		Description: "Reduces Redis maxmemory to force evictions or OOM errors",
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
		Category:    extutil.Ptr("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long to apply the memory limit"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "maxmemory",
				Label:        "Max Memory",
				Description:  extutil.Ptr("Maximum memory limit (e.g., '10mb', '1gb', or bytes). Set to '1mb' for aggressive limiting."),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: extutil.Ptr("10mb"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "evictionPolicy",
				Label:        "Eviction Policy",
				Description:  extutil.Ptr("Memory eviction policy to apply"),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: extutil.Ptr("noeviction"),
				Required:     extutil.Ptr(true),
				Options: extutil.Ptr([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "No Eviction (return errors on write)",
						Value: "noeviction",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "AllKeys-LRU (evict least recently used)",
						Value: "allkeys-lru",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "AllKeys-LFU (evict least frequently used)",
						Value: "allkeys-lfu",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "AllKeys-Random (evict random keys)",
						Value: "allkeys-random",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Volatile-LRU (evict LRU keys with TTL)",
						Value: "volatile-lru",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Volatile-TTL (evict shortest TTL first)",
						Value: "volatile-ttl",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Keep Original Policy",
						Value: "keep",
					},
				}),
			},
		},
	}
}

func (a *maxmemoryLimitAttack) Prepare(ctx context.Context, state *MaxmemoryLimitState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	maxmemory := extutil.ToString(request.Config["maxmemory"])
	evictionPolicy := extutil.ToString(request.Config["evictionPolicy"])

	if maxmemory == "" {
		return nil, fmt.Errorf("maxmemory is required")
	}

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.NewMaxmemory = maxmemory
	state.NewPolicy = evictionPolicy
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()

	return nil, nil
}

func (a *maxmemoryLimitAttack) Start(ctx context.Context, state *MaxmemoryLimitState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Get current configuration to restore later
	configResult, err := client.ConfigGet(ctx, "maxmemory").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get current maxmemory: %w", err)
	}
	if len(configResult) >= 2 {
		state.OriginalMaxmemory = configResult["maxmemory"]
	}

	configResult, err = client.ConfigGet(ctx, "maxmemory-policy").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get current maxmemory-policy: %w", err)
	}
	if len(configResult) >= 2 {
		state.OriginalPolicy = configResult["maxmemory-policy"]
	}

	log.Info().
		Str("originalMaxmemory", state.OriginalMaxmemory).
		Str("originalPolicy", state.OriginalPolicy).
		Str("newMaxmemory", state.NewMaxmemory).
		Str("newPolicy", state.NewPolicy).
		Msg("Applying maxmemory limit")

	// Set new maxmemory
	err = client.ConfigSet(ctx, "maxmemory", state.NewMaxmemory).Err()
	if err != nil {
		return nil, fmt.Errorf("failed to set maxmemory: %w", err)
	}

	// Set new eviction policy if not "keep"
	if state.NewPolicy != "keep" && state.NewPolicy != "" {
		err = client.ConfigSet(ctx, "maxmemory-policy", state.NewPolicy).Err()
		if err != nil {
			// Try to restore maxmemory on failure
			_ = client.ConfigSet(ctx, "maxmemory", state.OriginalMaxmemory).Err()
			return nil, fmt.Errorf("failed to set maxmemory-policy: %w", err)
		}
	}

	policyMsg := state.NewPolicy
	if state.NewPolicy == "keep" {
		policyMsg = state.OriginalPolicy + " (unchanged)"
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Set maxmemory to %s (was: %s), policy: %s", state.NewMaxmemory, state.OriginalMaxmemory, policyMsg),
			},
		}),
	}, nil
}

func (a *maxmemoryLimitAttack) Status(ctx context.Context, state *MaxmemoryLimitState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	var memoryInfo string
	if err == nil {
		defer client.Close()
		info, err := clients.GetRedisInfo(ctx, client, "memory")
		if err == nil {
			if used, ok := info["used_memory_human"]; ok {
				memoryInfo = used
			}
		}
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("MaxMemory limit active: %s, current usage: %s", state.NewMaxmemory, memoryInfo),
			},
		}),
	}, nil
}

func (a *maxmemoryLimitAttack) Stop(ctx context.Context, state *MaxmemoryLimitState) (*action_kit_api.StopResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client for restore: %w", err)
	}
	defer client.Close()

	var restoreErrors []string

	// Restore original maxmemory
	if state.OriginalMaxmemory != "" {
		err = client.ConfigSet(ctx, "maxmemory", state.OriginalMaxmemory).Err()
		if err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("maxmemory: %v", err))
			log.Warn().Err(err).Str("value", state.OriginalMaxmemory).Msg("Failed to restore maxmemory")
		}
	}

	// Restore original policy
	if state.OriginalPolicy != "" && state.NewPolicy != "keep" {
		err = client.ConfigSet(ctx, "maxmemory-policy", state.OriginalPolicy).Err()
		if err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("policy: %v", err))
			log.Warn().Err(err).Str("value", state.OriginalPolicy).Msg("Failed to restore maxmemory-policy")
		}
	}

	if len(restoreErrors) > 0 {
		return &action_kit_api.StopResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("Restore completed with errors: %v", restoreErrors),
				},
			}),
		}, nil
	}

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Restored maxmemory to %s, policy to %s", state.OriginalMaxmemory, state.OriginalPolicy),
			},
		}),
	}, nil
}
