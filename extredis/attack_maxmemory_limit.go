/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
)

type maxmemoryLimitAttack struct{}

type MaxmemoryLimitState struct {
	RedisURL          string            `json:"redisUrl"`
	Password          string            `json:"password"`
	DB                int               `json:"db"`
	OriginalMaxmemory string            `json:"originalMaxmemory"`
	OriginalPolicy    string            `json:"originalPolicy"`
	NewMaxmemory      string            `json:"newMaxmemory"`
	NewPolicy         string            `json:"newPolicy"`
	EndTime           int64             `json:"endTime"`
	ClusterMode       bool              `json:"clusterMode"`
	PerNodeOrigMaxmem map[string]string `json:"perNodeOrigMaxmem,omitempty"`
	PerNodeOrigPolicy map[string]string `json:"perNodeOrigPolicy,omitempty"`
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
		Category:    new("resource"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  new("How long to apply the memory limit"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: new("60s"),
				Required:     new(true),
			},
			{
				Name:         "maxmemory",
				Label:        "Max Memory",
				Description:  new("Maximum memory limit (e.g., '10mb', '1gb', or bytes). Set to '1mb' for aggressive limiting."),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: new("10mb"),
				Required:     new(true),
			},
			{
				Name:         "evictionPolicy",
				Label:        "Eviction Policy",
				Description:  new("Memory eviction policy to apply"),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: new("noeviction"),
				Required:     new(true),
				Options: new([]action_kit_api.ParameterOption{
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
	state.PerNodeOrigMaxmem = make(map[string]string)
	state.PerNodeOrigPolicy = make(map[string]string)

	endpoint := config.GetEndpointByURL(state.RedisURL)
	if endpoint != nil {
		isCluster, err := clients.DetectClusterMode(ctx, endpoint)
		if err == nil {
			state.ClusterMode = isCluster
		}
	}

	// Validate connectivity and CONFIG access before Start
	client, err := clients.GetRedisClient(state.RedisURL, "", state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}
	_, err = client.ConfigGet(ctx, "maxmemory").Result()
	if err != nil {
		return nil, fmt.Errorf("CONFIG GET is not available on this Redis instance (may be disabled or require admin privileges): %w", err)
	}

	return nil, nil
}

func (a *maxmemoryLimitAttack) Start(ctx context.Context, state *MaxmemoryLimitState) (*action_kit_api.StartResult, error) {
	endpoint := config.GetEndpointByURL(state.RedisURL)

	applyToNode := func(ctx context.Context, nodeClient *redis.Client, addr string) error {
		if err := clients.PingRedis(ctx, nodeClient); err != nil {
			return fmt.Errorf("failed to ping Redis: %w", err)
		}

		// Save original config per node
		configResult, err := nodeClient.ConfigGet(ctx, "maxmemory").Result()
		if err != nil {
			return fmt.Errorf("failed to get current maxmemory: %w", err)
		}
		if len(configResult) >= 2 {
			state.PerNodeOrigMaxmem[addr] = configResult["maxmemory"]
			if state.OriginalMaxmemory == "" {
				state.OriginalMaxmemory = configResult["maxmemory"]
			}
		}

		configResult, err = nodeClient.ConfigGet(ctx, "maxmemory-policy").Result()
		if err != nil {
			return fmt.Errorf("failed to get current maxmemory-policy: %w", err)
		}
		if len(configResult) >= 2 {
			state.PerNodeOrigPolicy[addr] = configResult["maxmemory-policy"]
			if state.OriginalPolicy == "" {
				state.OriginalPolicy = configResult["maxmemory-policy"]
			}
		}

		log.Info().Str("addr", addr).
			Str("originalMaxmemory", state.PerNodeOrigMaxmem[addr]).
			Str("newMaxmemory", state.NewMaxmemory).
			Msg("Applying maxmemory limit")

		if err := nodeClient.ConfigSet(ctx, "maxmemory", state.NewMaxmemory).Err(); err != nil {
			return fmt.Errorf("failed to set maxmemory: %w", err)
		}

		if state.NewPolicy != "keep" && state.NewPolicy != "" {
			if err := nodeClient.ConfigSet(ctx, "maxmemory-policy", state.NewPolicy).Err(); err != nil {
				_ = nodeClient.ConfigSet(ctx, "maxmemory", state.PerNodeOrigMaxmem[addr]).Err()
				return fmt.Errorf("failed to set maxmemory-policy: %w", err)
			}
		}

		return nil
	}

	if state.ClusterMode && endpoint != nil {
		if err := clients.ForEachMaster(ctx, endpoint, applyToNode); err != nil {
			return nil, err
		}
	} else {
		client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
		if err != nil {
			return nil, fmt.Errorf("failed to create Redis client: %w", err)
		}
		if err := applyToNode(ctx, client, client.Options().Addr); err != nil {
			return nil, err
		}
	}

	policyMsg := state.NewPolicy
	if state.NewPolicy == "keep" {
		policyMsg = state.OriginalPolicy + " (unchanged)"
	}

	nodeCount := len(state.PerNodeOrigMaxmem)
	if nodeCount == 0 {
		nodeCount = 1
	}

	messages := []action_kit_api.Message{
		{
			Level:   extutil.Ptr(action_kit_api.Info),
			Message: fmt.Sprintf("Set maxmemory to %s (was: %s), policy: %s on %d node(s)", state.NewMaxmemory, state.OriginalMaxmemory, policyMsg, nodeCount),
		},
	}

	activePolicy := state.NewPolicy
	if activePolicy == "keep" || activePolicy == "" {
		activePolicy = state.OriginalPolicy
	}
	switch activePolicy {
	case "allkeys-lru", "allkeys-lfu", "allkeys-random":
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: fmt.Sprintf("WARNING: Eviction policy '%s' will PERMANENTLY DELETE keys when memory limit is reached. Evicted data cannot be recovered. Consider using 'noeviction' to return OOM errors instead.", activePolicy),
		})
	case "volatile-lru", "volatile-lfu", "volatile-random", "volatile-ttl":
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: fmt.Sprintf("WARNING: Eviction policy '%s' will permanently delete keys with TTL when memory limit is reached.", activePolicy),
		})
	}

	return &action_kit_api.StartResult{
		Messages: new(messages),
	}, nil
}

func (a *maxmemoryLimitAttack) Status(ctx context.Context, state *MaxmemoryLimitState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
	var memoryInfo string
	if err == nil {
		info, err := clients.GetRedisInfo(ctx, client, "memory")
		if err == nil {
			if used, ok := info["used_memory_human"]; ok {
				memoryInfo = used
			}
		}
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: new([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("MaxMemory limit active: %s, current usage: %s", state.NewMaxmemory, memoryInfo),
			},
		}),
	}, nil
}

func (a *maxmemoryLimitAttack) Stop(ctx context.Context, state *MaxmemoryLimitState) (*action_kit_api.StopResult, error) {
	endpoint := config.GetEndpointByURL(state.RedisURL)
	var restoreErrors []string

	restoreNode := func(ctx context.Context, nodeClient *redis.Client, addr string) error {
		origMaxmem := state.OriginalMaxmemory
		origPolicy := state.OriginalPolicy
		if v, ok := state.PerNodeOrigMaxmem[addr]; ok {
			origMaxmem = v
		}
		if v, ok := state.PerNodeOrigPolicy[addr]; ok {
			origPolicy = v
		}

		if err := nodeClient.ConfigSet(ctx, "maxmemory", origMaxmem).Err(); err != nil {
			restoreErrors = append(restoreErrors, fmt.Sprintf("maxmemory on %s: %v", addr, err))
			log.Warn().Err(err).Str("addr", addr).Str("value", origMaxmem).Msg("Failed to restore maxmemory")
		}

		if state.NewPolicy != "keep" {
			if err := nodeClient.ConfigSet(ctx, "maxmemory-policy", origPolicy).Err(); err != nil {
				restoreErrors = append(restoreErrors, fmt.Sprintf("policy on %s: %v", addr, err))
				log.Warn().Err(err).Str("addr", addr).Str("value", origPolicy).Msg("Failed to restore maxmemory-policy")
			}
		}
		return nil
	}

	if state.ClusterMode && endpoint != nil {
		_ = clients.ForEachMaster(ctx, endpoint, restoreNode)
	} else {
		client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
		if err != nil {
			return nil, fmt.Errorf("failed to create Redis client for restore: %w", err)
		}
		_ = restoreNode(ctx, client, client.Options().Addr)
	}

	if len(restoreErrors) > 0 {
		log.Error().Strs("errors", restoreErrors).Msg("Failed to restore maxmemory settings")
		return nil, fmt.Errorf("restore failed: %v", restoreErrors)
	}

	return &action_kit_api.StopResult{
		Messages: new([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Restored maxmemory to %s, policy to %s", state.OriginalMaxmemory, state.OriginalPolicy),
			},
		}),
	}, nil
}
