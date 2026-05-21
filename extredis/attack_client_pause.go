/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
	"github.com/steadybit/extension-redis/config"
)

// pauseMode is the only mode this attack issues. We never use CLIENT PAUSE ALL
// because Redis applies the pause check to every non-master command — including
// CLIENT UNPAUSE itself — so an ALL pause cannot be aborted early and the
// extension's own discovery probes also time out for the duration of the
// attack. WRITE mode stalls only `is_may_replicate_command` data writes, which
// leaves connection-management commands (UNPAUSE) and read probes (PING, INFO)
// working: the attack stays reversible and discovery is unaffected.
const pauseMode = "WRITE"

type clientPauseAttack struct{}

type ClientPauseState struct {
	RedisURL    string `json:"redisUrl"`
	Password    string `json:"password"`
	DB          int    `json:"db"`
	EndTime     int64  `json:"endTime"`
	ClusterMode bool   `json:"clusterMode"`
}

var _ action_kit_sdk.Action[ClientPauseState] = (*clientPauseAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[ClientPauseState] = (*clientPauseAttack)(nil)
var _ action_kit_sdk.ActionWithStop[ClientPauseState] = (*clientPauseAttack)(nil)

func NewClientPauseAttack() action_kit_sdk.Action[ClientPauseState] {
	return &clientPauseAttack{}
}

func (a *clientPauseAttack) NewEmptyState() ClientPauseState {
	return ClientPauseState{}
}

func (a *clientPauseAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.client-pause",
		Label:       "Pause Write Clients",
		Description: "Pauses write commands on the Redis instance using CLIENT PAUSE WRITE.",
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
		Category:    new("network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  new("How long to pause write commands"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: new("30s"),
				Required:     new(true),
			},
		},
	}
}

func (a *clientPauseAttack) Prepare(ctx context.Context, state *ClientPauseState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()

	endpoint := config.GetEndpointByURL(state.RedisURL)
	if endpoint != nil {
		isCluster, err := clients.DetectClusterMode(ctx, endpoint)
		if err == nil {
			state.ClusterMode = isCluster
		}
	}

	// Validate connectivity before Start
	client, err := clients.GetRedisClient(state.RedisURL, "", state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	return nil, nil
}

func (a *clientPauseAttack) Start(ctx context.Context, state *ClientPauseState) (*action_kit_api.StartResult, error) {
	pauseDurationMs := (state.EndTime - time.Now().Unix()) * 1000
	if pauseDurationMs <= 0 {
		return nil, fmt.Errorf("pause duration must be positive")
	}

	pauseNode := func(ctx context.Context, nodeClient *redis.Client, addr string) error {
		if err := clients.PingRedis(ctx, nodeClient); err != nil {
			return fmt.Errorf("failed to ping Redis: %w", err)
		}

		if err := nodeClient.Do(ctx, "CLIENT", "PAUSE", pauseDurationMs, pauseMode).Err(); err != nil {
			return fmt.Errorf("failed to execute CLIENT PAUSE: %w", err)
		}
		return nil
	}

	endpoint := config.GetEndpointByURL(state.RedisURL)
	nodeCount := 1
	if state.ClusterMode && endpoint != nil {
		if err := clients.ForEachMaster(ctx, endpoint, pauseNode); err != nil {
			return nil, err
		}
		masters, _, _ := clients.GetMasterNodes(ctx, endpoint)
		nodeCount = len(masters)
	} else {
		client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
		if err != nil {
			return nil, fmt.Errorf("failed to create Redis client: %w", err)
		}
		if err := pauseNode(ctx, client, client.Options().Addr); err != nil {
			return nil, err
		}
	}

	return &action_kit_api.StartResult{
		Messages: new([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Paused Redis write commands for %d ms on %d node(s)", pauseDurationMs, nodeCount),
			},
		}),
	}, nil
}

func (a *clientPauseAttack) Status(ctx context.Context, state *ClientPauseState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	remainingSeconds := max(state.EndTime-now, 0)

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: new([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Write-pause active, %d seconds remaining", remainingSeconds),
			},
		}),
	}, nil
}

func (a *clientPauseAttack) Stop(ctx context.Context, state *ClientPauseState) (*action_kit_api.StopResult, error) {
	unpauseNode := func(ctx context.Context, nodeClient *redis.Client, addr string) error {
		return nodeClient.Do(ctx, "CLIENT", "UNPAUSE").Err()
	}

	endpoint := config.GetEndpointByURL(state.RedisURL)
	if state.ClusterMode && endpoint != nil {
		if err := clients.ForEachMaster(ctx, endpoint, unpauseNode); err != nil {
			return nil, fmt.Errorf("failed to execute CLIENT UNPAUSE on cluster: %w", err)
		}
	} else {
		client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
		if err != nil {
			return nil, fmt.Errorf("failed to create Redis client: %w", err)
		}
		if err := unpauseNode(ctx, client, ""); err != nil {
			return nil, fmt.Errorf("failed to execute CLIENT UNPAUSE: %w", err)
		}
	}

	return &action_kit_api.StopResult{
		Messages: new([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: "Executed CLIENT UNPAUSE, write commands resumed",
			},
		}),
	}, nil
}
