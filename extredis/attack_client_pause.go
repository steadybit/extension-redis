/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"time"

	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type clientPauseAttack struct{}

type ClientPauseState struct {
	RedisURL  string `json:"redisUrl"`
	Password  string `json:"password"`
	DB        int    `json:"db"`
	PauseMode string `json:"pauseMode"`
	EndTime   int64  `json:"endTime"`
}

var _ action_kit_sdk.Action[ClientPauseState] = (*clientPauseAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[ClientPauseState] = (*clientPauseAttack)(nil)

func NewClientPauseAttack() action_kit_sdk.Action[ClientPauseState] {
	return &clientPauseAttack{}
}

func (a *clientPauseAttack) NewEmptyState() ClientPauseState {
	return ClientPauseState{}
}

func (a *clientPauseAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.client-pause",
		Label:       "Pause Clients",
		Description: "Suspends all client command processing for a duration using CLIENT PAUSE",
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
		Category:    extutil.Ptr("network"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long to pause client connections"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "pauseMode",
				Label:        "Pause Mode",
				Description:  extutil.Ptr("WRITE pauses only write commands, ALL pauses all commands"),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: extutil.Ptr("ALL"),
				Required:     extutil.Ptr(true),
				Options: extutil.Ptr([]action_kit_api.ParameterOption{
					action_kit_api.ExplicitParameterOption{
						Label: "All Commands",
						Value: "ALL",
					},
					action_kit_api.ExplicitParameterOption{
						Label: "Write Commands Only",
						Value: "WRITE",
					},
				}),
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
	pauseMode := extutil.ToString(request.Config["pauseMode"])

	if pauseMode != "ALL" && pauseMode != "WRITE" {
		pauseMode = "ALL"
	}

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.PauseMode = pauseMode
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()

	return nil, nil
}

func (a *clientPauseAttack) Start(ctx context.Context, state *ClientPauseState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Calculate pause duration in milliseconds
	pauseDurationMs := (state.EndTime - time.Now().Unix()) * 1000
	if pauseDurationMs <= 0 {
		return nil, fmt.Errorf("pause duration must be positive")
	}

	// Execute CLIENT PAUSE command
	// CLIENT PAUSE timeout [WRITE | ALL]
	args := []interface{}{"PAUSE", pauseDurationMs}
	if state.PauseMode == "WRITE" {
		args = append(args, "WRITE")
	} else {
		args = append(args, "ALL")
	}

	err = client.Do(ctx, append([]interface{}{"CLIENT"}, args...)...).Err()
	if err != nil {
		return nil, fmt.Errorf("failed to execute CLIENT PAUSE: %w", err)
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Paused Redis clients (mode: %s) for %d ms", state.PauseMode, pauseDurationMs),
			},
		}),
	}, nil
}

func (a *clientPauseAttack) Status(ctx context.Context, state *ClientPauseState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	remainingSeconds := state.EndTime - now
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Client pause active (mode: %s), %d seconds remaining", state.PauseMode, remainingSeconds),
			},
		}),
	}, nil
}
