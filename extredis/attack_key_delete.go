/*
 * Copyright 2024 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/steadybit/action-kit/go/action_kit_api/v2"
	"github.com/steadybit/action-kit/go/action_kit_sdk"
	"github.com/steadybit/extension-kit/extbuild"
	"github.com/steadybit/extension-kit/extutil"
	"github.com/steadybit/extension-redis/clients"
)

type keyDeleteAttack struct{}

type KeyDeleteState struct {
	RedisURL      string            `json:"redisUrl"`
	Password      string            `json:"password"`
	DB            int               `json:"db"`
	Pattern       string            `json:"pattern"`
	MaxKeys       int               `json:"maxKeys"`
	DeletedKeys   []string          `json:"deletedKeys"`
	BackupData    map[string]string `json:"backupData"` // For restore on stop
	RestoreOnStop bool              `json:"restoreOnStop"`
	EndTime       int64             `json:"endTime"`
}

var _ action_kit_sdk.Action[KeyDeleteState] = (*keyDeleteAttack)(nil)
var _ action_kit_sdk.ActionWithStop[KeyDeleteState] = (*keyDeleteAttack)(nil)
var _ action_kit_sdk.ActionWithStatus[KeyDeleteState] = (*keyDeleteAttack)(nil)

func NewKeyDeleteAttack() action_kit_sdk.Action[KeyDeleteState] {
	return &keyDeleteAttack{}
}

func (a *keyDeleteAttack) NewEmptyState() KeyDeleteState {
	return KeyDeleteState{
		BackupData: make(map[string]string),
	}
}

func (a *keyDeleteAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.database.key-delete",
		Label:       "Delete Keys",
		Description: "Deletes keys matching a pattern to simulate data loss scenarios",
		Version:     extbuild.GetSemverVersionStringOrUnknown(),
		Icon:        extutil.Ptr(redisIcon),
		TargetSelection: extutil.Ptr(action_kit_api.TargetSelection{
			TargetType: TargetTypeDatabase,
			SelectionTemplates: extutil.Ptr([]action_kit_api.TargetSelectionTemplate{
				{
					Label:       "by host and database",
					Description: extutil.Ptr("Find Redis database by host and index"),
					Query:       "redis.host=\"\" AND redis.database.index=\"\"",
				},
			}),
		}),
		Technology:  extutil.Ptr("Redis"),
		Category:    extutil.Ptr("state"),
		Kind:        action_kit_api.Attack,
		TimeControl: action_kit_api.TimeControlExternal,
		Parameters: []action_kit_api.ActionParameter{
			{
				Name:         "duration",
				Label:        "Duration",
				Description:  extutil.Ptr("How long the attack should last"),
				Type:         action_kit_api.ActionParameterTypeDuration,
				DefaultValue: extutil.Ptr("60s"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "pattern",
				Label:        "Key Pattern",
				Description:  extutil.Ptr("Pattern to match keys for deletion (e.g., 'user:*', 'cache:*')"),
				Type:         action_kit_api.ActionParameterTypeString,
				DefaultValue: extutil.Ptr("steadybit-test:*"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "maxKeys",
				Label:        "Max Keys",
				Description:  extutil.Ptr("Maximum number of keys to delete (0 = unlimited, use with caution)"),
				Type:         action_kit_api.ActionParameterTypeInteger,
				DefaultValue: extutil.Ptr("100"),
				Required:     extutil.Ptr(true),
			},
			{
				Name:         "restoreOnStop",
				Label:        "Restore on Stop",
				Description:  extutil.Ptr("Restore deleted keys when attack stops (only works for string values)"),
				Type:         action_kit_api.ActionParameterTypeBoolean,
				DefaultValue: extutil.Ptr("true"),
				Required:     extutil.Ptr(false),
				Advanced:     extutil.Ptr(true),
			},
		},
	}
}

func (a *keyDeleteAttack) Prepare(ctx context.Context, state *KeyDeleteState, request action_kit_api.PrepareActionRequestBody) (*action_kit_api.PrepareResult, error) {
	redisURL := request.Target.Attributes[AttrRedisURL]
	if len(redisURL) == 0 {
		return nil, fmt.Errorf("redis URL not found in target attributes")
	}

	dbIndex := request.Target.Attributes[AttrDatabaseIndex]
	db := 0
	if len(dbIndex) > 0 {
		db, _ = strconv.Atoi(dbIndex[0])
	}

	duration := extutil.ToInt64(request.Config["duration"]) / 1000 // Convert ms to seconds
	pattern := extutil.ToString(request.Config["pattern"])
	maxKeys := int(extutil.ToInt64(request.Config["maxKeys"]))
	restoreOnStop := extutil.ToBool(request.Config["restoreOnStop"])

	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}

	state.RedisURL = redisURL[0]
	state.DB = db
	state.Pattern = pattern
	state.MaxKeys = maxKeys
	state.RestoreOnStop = restoreOnStop
	state.DeletedKeys = []string{}
	state.BackupData = make(map[string]string)
	state.EndTime = time.Now().Add(time.Duration(duration) * time.Second).Unix()

	return nil, nil
}

func (a *keyDeleteAttack) Start(ctx context.Context, state *KeyDeleteState) (*action_kit_api.StartResult, error) {
	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}
	defer client.Close()

	// Verify connection
	if err := clients.PingRedis(ctx, client); err != nil {
		return nil, fmt.Errorf("failed to ping Redis: %w", err)
	}

	// Find keys matching pattern using SCAN (non-blocking)
	var keysToDelete []string
	var cursor uint64 = 0

	for {
		var keys []string
		var err error
		keys, cursor, err = client.Scan(ctx, cursor, state.Pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to scan keys: %w", err)
		}

		keysToDelete = append(keysToDelete, keys...)

		// Check max keys limit
		if state.MaxKeys > 0 && len(keysToDelete) >= state.MaxKeys {
			keysToDelete = keysToDelete[:state.MaxKeys]
			break
		}

		if cursor == 0 {
			break
		}
	}

	if len(keysToDelete) == 0 {
		return &action_kit_api.StartResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Warn),
					Message: fmt.Sprintf("No keys found matching pattern '%s'", state.Pattern),
				},
			}),
		}, nil
	}

	// Backup keys if restore is enabled
	if state.RestoreOnStop {
		for _, key := range keysToDelete {
			// Only backup string values (other types would need more complex handling)
			val, err := client.Get(ctx, key).Result()
			if err == nil {
				state.BackupData[key] = val
			}
		}
	}

	// Delete keys
	deletedCount := 0
	for _, key := range keysToDelete {
		err := client.Del(ctx, key).Err()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to delete key")
			continue
		}
		state.DeletedKeys = append(state.DeletedKeys, key)
		deletedCount++
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Deleted %d keys matching pattern '%s'", deletedCount, state.Pattern),
			},
		}),
	}, nil
}

func (a *keyDeleteAttack) Status(ctx context.Context, state *KeyDeleteState) (*action_kit_api.StatusResult, error) {
	now := time.Now().Unix()
	completed := now >= state.EndTime

	return &action_kit_api.StatusResult{
		Completed: completed,
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Deleted %d keys matching pattern '%s'", len(state.DeletedKeys), state.Pattern),
			},
		}),
	}, nil
}

func (a *keyDeleteAttack) Stop(ctx context.Context, state *KeyDeleteState) (*action_kit_api.StopResult, error) {
	if !state.RestoreOnStop || len(state.BackupData) == 0 {
		return &action_kit_api.StopResult{
			Messages: extutil.Ptr([]action_kit_api.Message{
				{
					Level:   extutil.Ptr(action_kit_api.Info),
					Message: fmt.Sprintf("Key delete attack completed. %d keys were deleted.", len(state.DeletedKeys)),
				},
			}),
		}, nil
	}

	client, err := clients.CreateRedisClientFromURL(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client for restore: %w", err)
	}
	defer client.Close()

	// Restore backed up keys
	restoredCount := 0
	for key, value := range state.BackupData {
		err := client.Set(ctx, key, value, 0).Err()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Msg("Failed to restore key")
			continue
		}
		restoredCount++
	}

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: fmt.Sprintf("Restored %d of %d deleted keys", restoredCount, len(state.BackupData)),
			},
		}),
	}, nil
}
