/*
 * Copyright 2026 steadybit GmbH. All rights reserved.
 */

package extredis

import (
	"context"
	"encoding/base64"
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

type KeyBackupEntry struct {
	DumpValue  string `json:"dumpValue"`  // Serialized key data from DUMP command
	TTLSeconds int64  `json:"ttlSeconds"` // Original TTL in seconds (-1 = no TTL)
	KeyType    string `json:"keyType"`    // Key type (string, list, hash, set, zset, stream)
}

type KeyDeleteState struct {
	RedisURL      string                    `json:"redisUrl"`
	Password      string                    `json:"password"`
	DB            int                       `json:"db"`
	Pattern       string                    `json:"pattern"`
	MaxKeys       int                       `json:"maxKeys"`
	DeletedKeys   []string                  `json:"deletedKeys"`
	BackupData    map[string]KeyBackupEntry `json:"backupData"`
	RestoreOnStop bool                      `json:"restoreOnStop"`
}

var _ action_kit_sdk.Action[KeyDeleteState] = (*keyDeleteAttack)(nil)
var _ action_kit_sdk.ActionWithStop[KeyDeleteState] = (*keyDeleteAttack)(nil)

func NewKeyDeleteAttack() action_kit_sdk.Action[KeyDeleteState] {
	return &keyDeleteAttack{}
}

func (a *keyDeleteAttack) NewEmptyState() KeyDeleteState {
	return KeyDeleteState{
		BackupData: make(map[string]KeyBackupEntry),
	}
}

func (a *keyDeleteAttack) Describe() action_kit_api.ActionDescription {
	return action_kit_api.ActionDescription{
		Id:          "com.steadybit.extension_redis.database.key-delete",
		Label:       "Delete Keys",
		Description: "Deletes keys matching a pattern to simulate data loss scenarios. Supports backup and restore of all key types (string, list, hash, set, zset, stream) using Redis DUMP/RESTORE.",
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
				DefaultValue: extutil.Ptr(""),
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
				Description:  extutil.Ptr("Restore deleted keys when attack stops. Uses DUMP/RESTORE to preserve all key types and TTLs."),
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
	state.BackupData = make(map[string]KeyBackupEntry)

	return nil, nil
}

func (a *keyDeleteAttack) Start(ctx context.Context, state *KeyDeleteState) (*action_kit_api.StartResult, error) {
	client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}

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

	// Backup keys using DUMP if restore is enabled
	backupFailures := 0
	if state.RestoreOnStop {
		for _, key := range keysToDelete {
			// Get key type for logging
			keyType, typeErr := client.Type(ctx, key).Result()
			if typeErr != nil {
				keyType = "unknown"
			}

			// DUMP serializes the key value in Redis-internal format (RDB-like)
			// Works for ALL key types: string, list, hash, set, zset, stream
			dumpVal, dumpErr := client.Dump(ctx, key).Result()
			if dumpErr != nil {
				log.Warn().Err(dumpErr).Str("key", key).Str("type", keyType).Msg("Failed to DUMP key for backup")
				backupFailures++
				continue
			}

			// Get original TTL
			ttl, ttlErr := client.TTL(ctx, key).Result()
			var ttlSeconds int64 = -1 // Default: no expiry
			if ttlErr == nil && ttl > 0 {
				ttlSeconds = int64(ttl.Seconds())
			}

			state.BackupData[key] = KeyBackupEntry{
				DumpValue:  base64.StdEncoding.EncodeToString([]byte(dumpVal)),
				TTLSeconds: ttlSeconds,
				KeyType:    keyType,
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

	msg := fmt.Sprintf("Deleted %d keys matching pattern '%s'", deletedCount, state.Pattern)
	if state.RestoreOnStop {
		msg += fmt.Sprintf(". Backed up %d keys for restore.", len(state.BackupData))
		if backupFailures > 0 {
			msg += fmt.Sprintf(" WARNING: %d keys could not be backed up and will be permanently lost.", backupFailures)
		}
	}

	return &action_kit_api.StartResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   extutil.Ptr(action_kit_api.Info),
				Message: msg,
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

	client, err := clients.GetRedisClient(state.RedisURL, state.Password, state.DB)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client for restore: %w", err)
	}

	// Restore backed up keys using RESTORE
	restoredCount := 0
	failedCount := 0
	for key, backup := range state.BackupData {
		// Calculate TTL for RESTORE command
		var ttlDuration time.Duration
		if backup.TTLSeconds > 0 {
			ttlDuration = time.Duration(backup.TTLSeconds) * time.Second
		}
		// TTLSeconds == -1 or 0 means no expiry, ttlDuration stays 0

		// Decode base64 DUMP value
		dumpBytes, decErr := base64.StdEncoding.DecodeString(backup.DumpValue)
		if decErr != nil {
			log.Warn().Err(decErr).Str("key", key).Msg("Failed to decode backup data")
			failedCount++
			continue
		}

		// RestoreReplace overwrites the key if it already exists (idempotent)
		err := client.RestoreReplace(ctx, key, ttlDuration, string(dumpBytes)).Err()
		if err != nil {
			log.Warn().Err(err).Str("key", key).Str("type", backup.KeyType).Msg("Failed to restore key")
			failedCount++
			continue
		}
		restoredCount++
	}

	level := extutil.Ptr(action_kit_api.Info)
	if failedCount > 0 {
		level = extutil.Ptr(action_kit_api.Warn)
	}

	return &action_kit_api.StopResult{
		Messages: extutil.Ptr([]action_kit_api.Message{
			{
				Level:   level,
				Message: fmt.Sprintf("Restored %d of %d deleted keys (all types via DUMP/RESTORE). %d failures.", restoredCount, len(state.BackupData), failedCount),
			},
		}),
	}, nil
}
