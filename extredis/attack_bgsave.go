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

type bgsaveAttack struct{}

type BgsaveState struct {
	RedisURL       string `json:"redisUrl"`
	Password       string `json:"password"`
	DB             int    `json:"db"`
	LastSaveBefore int64  `json:"lastSaveBefore"`
}

var _ action_kit_sdk.Action[BgsaveState] = (*bgsaveAttack)(nil)

func NewBgsaveAttack() action_kit_sdk.Action[BgsaveState] {
	return &bgsaveAttack{}
}

func (a *bgsaveAttack) NewEmptyState() BgsaveState {
	return BgsaveState{}
}

func (a *bgsaveAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.instance.trigger-bgsave",
		Label:       "Trigger Background Save",
		Description: "Triggers a Redis BGSAVE command to initiate a background save (RDB snapshot). This can cause performance degradation during the save process, especially on large datasets.",
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
		Category:    extutil.Ptr("state"),
		TimeControl: action_kit_api.TimeControlInstantaneous,
		Kind:        action_kit_api.Attack,
		Parameters:  []action_kit_api.ActionParameter{},
	}
}

func (a *bgsaveAttack) Prepare(ctx context.Context, state *BgsaveState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	state.RedisURL = redisURL[0]
	state.DB = 0

	return nil, nil
}

func (a *bgsaveAttack) Start(ctx context.Context, state *BgsaveState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Get last save time before triggering (returns Unix timestamp)
	lastSaveTimestamp, err := client.LastSave(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get last save time: %w", err)
	}
	state.LastSaveBefore = lastSaveTimestamp
	lastSaveTime := time.Unix(lastSaveTimestamp, 0)

	// Check if a background save is already in progress
	info, err := clients.GetRedisInfo(ctx, client, "persistence")
	if err != nil {
		return nil, fmt.Errorf("failed to get persistence info: %w", err)
	}

	if bgsaveInProgress, ok := info["rdb_bgsave_in_progress"]; ok && bgsaveInProgress == "1" {
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: "A background save is already in progress",
				},
			}),
		}, nil
	}

	// Trigger BGSAVE
	result, err := client.BgSave(ctx).Result()
	if err != nil {
		// Redis returns an error if BGSAVE is already in progress
		if err.Error() == "ERR Background save already in progress" {
			return &action_kit_api.StartResult{
				Messages: extutil.Ptr([]action_kit_api.Message{
					{
						Level:   extutil.Ptr(action_kit_api.Warn),
						Message: "A background save is already in progress",
					},
				}),
			}, nil
		}
		return nil, fmt.Errorf("failed to trigger BGSAVE: %w", err)
	}

	// Get current memory usage for context
	memoryInfo, _ := clients.GetRedisInfo(ctx, client, "memory")
	usedMemory := "unknown"
	if val, ok := memoryInfo["used_memory_human"]; ok {
		usedMemory = val
	}

	// Get key count for context
	keyspaceInfo, _ := clients.GetRedisInfo(ctx, client, "keyspace")
	keyCount := "unknown"
	for key, val := range keyspaceInfo {
		if len(key) > 2 && key[:2] == "db" {
			// Parse keys=X from the value
			keyCount = val
			break
		}
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Triggered background save (BGSAVE). Response: %s. Redis memory: %s, Keyspace: %s. Last save was at: %s", result, usedMemory, keyCount, lastSaveTime.Format(time.RFC3339)),
			},
		}),
	}, nil
}
