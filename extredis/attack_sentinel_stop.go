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

type sentinelStopAttack struct{}

type SentinelStopState struct {
	RedisURL string `json:"redisUrl"`
	Password string `json:"password"`
	DB       int    `json:"db"`
	EndTime  int64  `json:"endTime"`
}

var _ action_kit_sdk.Action[SentinelStopState] = (*sentinelStopAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[SentinelStopState] = (*sentinelStopAttack)(nil)

func NewSentinelStopAttack() action_kit_sdk.Action[SentinelStopState] {
	return &sentinelStopAttack{}
}

func (a *sentinelStopAttack) NewEmptyState() SentinelStopState {
	return SentinelStopState{}
}

func (a *sentinelStopAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.sentinel-stop",
		Label:       "Stop Sentinel",
		Description: "Stops the Redis Sentinel server for a specific duration using DEBUG SLEEP, making it unresponsive to all clients and other Sentinels",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(redisIcon),
		TargetSelection: extutil.Ptr(action_kit_api.TargetSelection{
			TargetType: TargetTypeInstance,
			SelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
				{
					Label:       "by host and port",
					Description: extutil.Ptr("Find Redis Sentinel by host and port"),
					Query:       "redis.host=\"\" AND redis.port=\"\"",
				},
			}),
		}),
		Technology:  extutil.Ptr("Redis"),
		Category:    extutil.Ptr("availability"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long the Sentinel should be unresponsive. The Sentinel automatically recovers after this duration."),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("30s"),
				Required:     extutil.Ptr(true),
			},
		},
	}
}

func (a *sentinelStopAttack) Prepare(ctx context.Context, state *SentinelStopState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000

	state.RedisURL = redisURL[0]
	state.DB = 0
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()

	return nil, nil
}

func (a *sentinelStopAttack) Start(ctx context.Context, state *SentinelStopState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis Sentinel: %w", err)
	}

	// Verify this is a Sentinel instance
	var messages []action_kit_api.Message
	info, err := clients.GetRedisInfo(ctx, client, "server")
	if err != nil {
		log.Warn().Err(err).Msg("Could not verify redis_mode, proceeding with DEBUG SLEEP")
		messages = append(messages, action_kit_api.Message{
			Level:   extutil.Ptr(action_kit_api.Warn),
			Message: "Could not verify redis_mode (INFO server failed). Proceeding with DEBUG SLEEP anyway.",
		})
	} else {
		redisMode := info["redis_mode"]
		if redisMode != "sentinel" {
			log.Warn().Str("redis_mode", redisMode).Msg("Target instance is not running in sentinel mode")
			messages = append(messages, action_kit_api.Message{
				Level:   extutil.Ptr(action_kit_api.Warn),
				Message: fmt.Sprintf("Warning: Target instance reports redis_mode=%q (expected 'sentinel'). Proceeding with DEBUG SLEEP anyway.", redisMode),
			})
		}
	}

	// Calculate sleep duration in seconds
	sleepSeconds := state.EndTime - time.Now().Unix()
	if sleepSeconds <= 0 {
		return nil, fmt.Errorf("sleep duration must be positive")
	}

	// DEBUG SLEEP blocks the entire Redis event loop, making the Sentinel completely unresponsive
	// The Sentinel will automatically recover after the sleep duration
	err = client.Do(ctx, "DEBUG", "SLEEP", sleepSeconds).Err()
	if err != nil {
		return nil, fmt.Errorf("failed to execute DEBUG SLEEP: %w", err)
	}

	messages = append(messages, action_kit_api.Message{
		Level:   extutil.Ptr(action_kit_api.Info),
		Message: fmt.Sprintf("Redis Sentinel stopped via DEBUG SLEEP for %d seconds. It will automatically recover.", sleepSeconds),
	})

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr(messages),
	}, nil
}

func (a *sentinelStopAttack) Status(ctx context.Context, state *SentinelStopState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	remainingSeconds := state.EndTime - now
	if remainingSeconds < 0 {
		remainingSeconds = 0
	}

	level := action_kit_api.Info
	msg := fmt.Sprintf("Sentinel is sleeping, %d seconds remaining until automatic recovery", remainingSeconds)
	if completed {
		msg = "Sentinel sleep completed, instance should be responsive again"
	}

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(level),
				Message: msg,
			},
		}),
	}, nil
}
